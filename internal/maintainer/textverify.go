package maintainer

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type textRecord map[string]any

type logicalRecordIndex struct {
	Rows       map[string]textRecord
	Collisions []map[string]any
}

func jsonInt(v any) (int, error) {
	switch x := v.(type) {
	case json.Number:
		i, err := strconv.Atoi(string(x))
		return i, err
	case float64:
		return int(x), nil
	case int:
		return x, nil
	case int64:
		return int(x), nil
	case nil:
		return 0, fmt.Errorf("missing integer")
	default:
		return 0, fmt.Errorf("expected integer, got %T", v)
	}
}

func jsonIntDefault(v any, def int) int {
	if v == nil {
		return def
	}
	i, err := jsonInt(v)
	if err != nil {
		return def
	}
	return i
}

func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func nestedMap(rec textRecord, key string) map[string]any {
	if m, ok := rec[key].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func readTextRecords(path string, archives []int) ([]textRecord, error) {
	wanted := map[int]bool{}
	for _, a := range archives {
		wanted[a] = true
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	out := []textRecord{}
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var rec textRecord
		if err := dec.Decode(&rec); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		archive, err := jsonInt(rec["archive_number"])
		if err != nil {
			return nil, fmt.Errorf("%s:%d archive_number: %w", path, lineNo, err)
		}
		if len(wanted) > 0 && !wanted[archive] {
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func pyReprString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return "'" + s + "'"
}

func logicalBase(rec textRecord) (archive, gdat, cl3 int, err error) {
	archive, err = jsonInt(rec["archive_number"])
	if err != nil {
		return
	}
	gdat, err = jsonInt(rec["gdat_index"])
	if err != nil {
		return
	}
	cl3 = -1
	if rec["cl3_inner_index"] != nil {
		cl3, err = jsonInt(rec["cl3_inner_index"])
	}
	return
}

type rankKey struct {
	Archive int
	GDAT    int
	Third   int
}

func rankOffsets(rows []textRecord) (map[rankKey]map[int]int, map[rankKey]map[int]int, map[rankKey]map[int]int, error) {
	blobSets := map[rankKey]map[int]bool{}
	encSets := map[rankKey]map[int]bool{}
	strSets := map[rankKey]map[int]bool{}
	add := func(m map[rankKey]map[int]bool, k rankKey, off int) {
		if m[k] == nil {
			m[k] = map[int]bool{}
		}
		m[k][off] = true
	}
	for _, rec := range rows {
		archive, gdat, cl3, err := logicalBase(rec)
		if err != nil {
			return nil, nil, nil, err
		}
		role := jsonString(rec["field_role"])
		meta := nestedMap(rec, "repack_metadata")
		switch role {
		case "gnl_str":
			if meta["blob_offset"] != nil {
				off, err := jsonInt(meta["blob_offset"])
				if err != nil {
					return nil, nil, nil, err
				}
				add(blobSets, rankKey{archive, gdat, cl3}, off)
			}
		case "stcm_string":
			if meta["blob_offset"] != nil {
				off, err := jsonInt(meta["blob_offset"])
				if err != nil {
					return nil, nil, nil, err
				}
				add(strSets, rankKey{archive, gdat, cl3}, off)
			}
		case "enc_string":
			row, err := jsonInt(meta["row"])
			if err != nil {
				return nil, nil, nil, err
			}
			off, err := jsonInt(meta["str_offset"])
			if err != nil {
				return nil, nil, nil, err
			}
			add(encSets, rankKey{archive, gdat, row}, off)
		}
	}
	makeRanks := func(sets map[rankKey]map[int]bool) map[rankKey]map[int]int {
		out := map[rankKey]map[int]int{}
		for k, set := range sets {
			offs := make([]int, 0, len(set))
			for off := range set {
				offs = append(offs, off)
			}
			sort.Ints(offs)
			r := map[int]int{}
			for i, off := range offs {
				r[off] = i
			}
			out[k] = r
		}
		return out
	}
	return makeRanks(blobSets), makeRanks(encSets), makeRanks(strSets), nil
}

func logicalIndex(rows []textRecord, rankRows []textRecord) (logicalRecordIndex, error) {
	if rankRows == nil {
		rankRows = rows
	}
	blobRank, encRank, stringRank, err := rankOffsets(rankRows)
	if err != nil {
		return logicalRecordIndex{}, err
	}
	out := logicalRecordIndex{Rows: map[string]textRecord{}}
	for _, rec := range rows {
		archive, gdat, cl3, err := logicalBase(rec)
		if err != nil {
			return logicalRecordIndex{}, err
		}
		role := jsonString(rec["field_role"])
		meta := nestedMap(rec, "repack_metadata")
		var key string
		switch role {
		case "gnl_str":
			ordinal := 0
			if meta["blob_offset"] != nil {
				off, err := jsonInt(meta["blob_offset"])
				if err != nil {
					return logicalRecordIndex{}, err
				}
				r, ok := blobRank[rankKey{archive, gdat, cl3}][off]
				if !ok {
					return logicalRecordIndex{}, fmt.Errorf("gnl rank missing for offset %d", off)
				}
				ordinal = r
			}
			ridx, err := jsonInt(rec["record_index"])
			if err != nil {
				return logicalRecordIndex{}, err
			}
			fidx, err := jsonInt(rec["field_index"])
			if err != nil {
				return logicalRecordIndex{}, err
			}
			key = fmt.Sprintf("(%d, %d, %d, %s, %d, %d, %d)", archive, gdat, cl3, pyReprString(role), ordinal, ridx, fidx)
		case "stcm_string":
			ordinal := 0
			if meta["blob_offset"] != nil {
				off, err := jsonInt(meta["blob_offset"])
				if err != nil {
					return logicalRecordIndex{}, err
				}
				r, ok := stringRank[rankKey{archive, gdat, cl3}][off]
				if !ok {
					return logicalRecordIndex{}, fmt.Errorf("stcm-string rank missing for offset %d", off)
				}
				ordinal = r
			}
			key = fmt.Sprintf("(%d, %d, %d, %s, %d)", archive, gdat, cl3, pyReprString(role), ordinal)
		case "enc_string":
			row, err := jsonInt(meta["row"])
			if err != nil {
				return logicalRecordIndex{}, err
			}
			off, err := jsonInt(meta["str_offset"])
			if err != nil {
				return logicalRecordIndex{}, err
			}
			ordinal, ok := encRank[rankKey{archive, gdat, row}][off]
			if !ok {
				return logicalRecordIndex{}, fmt.Errorf("enc rank missing for row %d offset %d", row, off)
			}
			key = fmt.Sprintf("(%d, %d, %d, %s, %d, %d)", archive, gdat, cl3, pyReprString(role), row, ordinal)
		case "bin_string":
			idx := jsonIntDefault(meta["string_index"], jsonIntDefault(rec["record_index"], 0))
			key = fmt.Sprintf("(%d, %d, %d, %s, %d)", archive, gdat, cl3, pyReprString(role), idx)
		default:
			ridx, err := jsonInt(rec["record_index"])
			if err != nil {
				return logicalRecordIndex{}, err
			}
			fidx, err := jsonInt(rec["field_index"])
			if err != nil {
				return logicalRecordIndex{}, err
			}
			key = fmt.Sprintf("(%d, %d, %d, %s, %s, %d, %d)", archive, gdat, cl3, pyReprString(role), pyReprString(jsonString(rec["container_key"])), ridx, fidx)
		}
		if first, ok := out.Rows[key]; ok {
			out.Collisions = append(out.Collisions, map[string]any{
				"logical_key": key,
				"first":       first["record_key"],
				"second":      rec["record_key"],
			})
		} else {
			out.Rows[key] = rec
		}
	}
	return out, nil
}

func encodeRecordText(rec textRecord, encoding string) ([]byte, error) {
	if encoding == "" {
		encoding = jsonString(rec["encoding"])
	}
	switch strings.ToLower(encoding) {
	case "utf-8", "utf8":
		return []byte(jsonString(rec["text"])), nil
	default:
		return nil, fmt.Errorf("native verify candidate does not support encoding %q", encoding)
	}
}

func textRecordEdited(rec textRecord) bool {
	raw, err := hex.DecodeString(jsonString(rec["raw_hex"]))
	if err != nil {
		return true
	}
	enc, err := encodeRecordText(rec, jsonString(rec["encoding"]))
	return err != nil || !bytes.Equal(enc, raw)
}

func archiveFile(master string, archive int) string {
	return filepath.Join(master, fmt.Sprintf("GxArchivedFile%03d.dat", archive))
}

// buildReadOnlyOverlay constructs a disposable, metadata-only view of the two
// masters. Hardlinks are preferred; symlinks support masters on another device.
// Source DAT bytes are never copied or changed. The caller MUST remove the
// returned directory after all reads finish, even when verification fails.
// Unlike the old _go_verify_overlay directory, this never removes or overwrites
// an existing directory in the user's project.
func buildReadOnlyOverlay(rebuiltMaster, sourceMaster, scratchParent string) (_ string, err error) {
	overlay, err := os.MkdirTemp(scratchParent, ".go-verify-overlay-*")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(overlay)
		}
	}()
	linkSource := func(src, dst string) error {
		fi, e := os.Lstat(src)
		if e != nil {
			return e
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("verification source is not a regular file: %s", src)
		}
		// Both hardlinks and symlinks point to existing read-only inputs; the
		// extractor only reads through these paths. Never fall back to a huge
		// copy of the original DAT file.
		if e := os.Link(src, dst); e == nil {
			return nil
		}
		abs, e := filepath.Abs(src)
		if e != nil {
			return e
		}
		if e := os.Symlink(abs, dst); e != nil {
			return fmt.Errorf("cannot link verification source %s: %w", src, e)
		}
		return nil
	}
	source, err := filepath.Glob(filepath.Join(sourceMaster, "GxArchivedFile*.dat"))
	if err != nil {
		return "", err
	}
	sort.Strings(source)
	for _, src := range source {
		if err = linkSource(src, filepath.Join(overlay, filepath.Base(src))); err != nil {
			return "", err
		}
	}
	rebuilt, err := filepath.Glob(filepath.Join(rebuiltMaster, "GxArchivedFile*.dat"))
	if err != nil {
		return "", err
	}
	sort.Strings(rebuilt)
	for _, src := range rebuilt {
		dst := filepath.Join(overlay, filepath.Base(src))
		if e := os.Remove(dst); e != nil && !os.IsNotExist(e) {
			return "", e
		}
		if err = linkSource(src, dst); err != nil {
			return "", err
		}
	}
	extra, err := filepath.Glob(filepath.Join(rebuiltMaster, "*"))
	if err != nil {
		return "", err
	}
	for _, src := range extra {
		if filepath.Ext(src) == ".dat" {
			continue
		}
		fi, e := os.Lstat(src)
		if e != nil {
			return "", e
		}
		if !fi.Mode().IsRegular() {
			continue
		}
		dst := filepath.Join(overlay, filepath.Base(src))
		if _, e := os.Lstat(dst); e == nil {
			continue
		} else if !os.IsNotExist(e) {
			return "", e
		}
		if err = linkSource(src, dst); err != nil {
			return "", err
		}
	}
	return overlay, nil
}

func writePrettyJSON(path string, v any) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, bytes.TrimSuffix(b.Bytes(), []byte("\n")), 0o644)
}

func setDifference(a, b map[string]textRecord) []string {
	out := []string{}
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func setIntersection(a, b map[string]textRecord) []string {
	out := []string{}
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func firstStrings(in []string, n int) []string {
	out := []string{}
	if len(in) <= n {
		return append(out, in...)
	}
	return append(out, in[:n]...)
}

func firstMaps(in []map[string]any, n int) []map[string]any {
	out := []map[string]any{}
	if len(in) <= n {
		return append(out, in...)
	}
	return append(out, in[:n]...)
}

func editedEntrySet(rows []textRecord) map[[2]int]bool {
	out := map[[2]int]bool{}
	for _, rec := range rows {
		if !textRecordEdited(rec) {
			continue
		}
		a, _ := jsonInt(rec["archive_number"])
		g, _ := jsonInt(rec["gdat_index"])
		out[[2]int{a, g}] = true
	}
	return out
}

func verifyUneditedEntries(sourceMaster, rebuiltMaster string, archives []int, editedRows []textRecord) ([]map[string]any, error) {
	edited := editedEntrySet(editedRows)
	seen := map[int]bool{}
	arcs := []int{}
	for _, a := range archives {
		if !seen[a] {
			seen[a] = true
			arcs = append(arcs, a)
		}
	}
	sort.Ints(arcs)
	problems := []map[string]any{}
	for _, archive := range arcs {
		sp, rp := archiveFile(sourceMaster, archive), archiveFile(rebuiltMaster, archive)
		if !file(sp) || !file(rp) {
			problems = append(problems, map[string]any{"archive": archive, "error": "source or rebuilt archive missing"})
			continue
		}
		sg, err := mskit.OpenGDATFile(sp)
		if err != nil {
			return nil, err
		}
		rg, err := mskit.OpenGDATFile(rp)
		if err != nil {
			_ = sg.Close()
			return nil, err
		}
		if sg.Count() != rg.Count() {
			problems = append(problems, map[string]any{"archive": archive, "error": "GDAT entry count changed", "source_count": sg.Count(), "rebuilt_count": rg.Count()})
			_ = sg.Close()
			_ = rg.Close()
			continue
		}
		for i := 0; i < sg.Count(); i++ {
			if edited[[2]int{archive, i}] {
				continue
			}
			sb, err := sg.ReadEntry(i)
			if err != nil {
				_ = sg.Close()
				_ = rg.Close()
				return nil, err
			}
			rb, err := rg.ReadEntry(i)
			if err != nil {
				_ = sg.Close()
				_ = rg.Close()
				return nil, err
			}
			if !bytes.Equal(sb, rb) {
				problems = append(problems, map[string]any{"archive": archive, "gdat_index": i, "error": "unedited GDAT entry changed", "source_size": len(sb), "rebuilt_size": len(rb)})
				if len(problems) >= 50 {
					break
				}
			}
		}
		_ = sg.Close()
		_ = rg.Close()
		if len(problems) >= 50 {
			break
		}
	}
	return problems, nil
}

func verifyENCRows(sourceMaster, rebuiltMaster string, archives []int, editedRows []textRecord) map[string]any {
	type spec struct {
		Old       []byte
		New       []byte
		Capacity  int
		RecordKey string
	}
	type loc struct{ Row, Offset int }
	groups := map[[2]int]map[loc]spec{}
	problem := func(m map[string]any) map[string]any {
		return map[string]any{"ok": false, "entries_checked": 0, "rows_checked": 0, "edits_checked": 0, "problems": []map[string]any{m}}
	}
	for _, rec := range editedRows {
		if jsonString(rec["field_role"]) != "enc_string" {
			continue
		}
		meta := nestedMap(rec, "repack_metadata")
		a, err := jsonInt(rec["archive_number"])
		if err != nil {
			return problem(map[string]any{"error": err.Error()})
		}
		g, err := jsonInt(rec["gdat_index"])
		if err != nil {
			return problem(map[string]any{"error": err.Error()})
		}
		r, err := jsonInt(meta["row"])
		if err != nil {
			return problem(map[string]any{"error": err.Error()})
		}
		o, err := jsonInt(meta["str_offset"])
		if err != nil {
			return problem(map[string]any{"error": err.Error()})
		}
		old, err := hex.DecodeString(jsonString(rec["raw_hex"]))
		if err != nil {
			return problem(map[string]any{"error": err.Error()})
		}
		newb, err := encodeRecordText(rec, jsonString(rec["encoding"]))
		if err != nil {
			return problem(map[string]any{"error": err.Error()})
		}
		capn := jsonIntDefault(meta["slot_capacity"], len(old))
		k := [2]int{a, g}
		l := loc{r, o}
		s := spec{old, newb, capn, jsonString(rec["record_key"])}
		if groups[k] == nil {
			groups[k] = map[loc]spec{}
		}
		if prev, ok := groups[k][l]; ok && (!bytes.Equal(prev.Old, s.Old) || !bytes.Equal(prev.New, s.New) || prev.Capacity != s.Capacity || prev.RecordKey != s.RecordKey) {
			return problem(map[string]any{"archive": a, "gdat_index": g, "row": r, "str_offset": o, "error": "conflicting ENC edit specs"})
		}
		groups[k][l] = s
	}
	wantedArchives := map[int]bool{}
	if archives != nil {
		for _, a := range archives {
			wantedArchives[a] = true
		}
	}
	keys := make([][2]int, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	problems := []map[string]any{}
	entriesChecked, rowsChecked, editsChecked := 0, 0, 0
	for _, k := range keys {
		archive, gidx := k[0], k[1]
		if archives != nil && !wantedArchives[archive] {
			continue
		}
		sg, err := mskit.OpenGDATFile(archiveFile(sourceMaster, archive))
		if err != nil {
			problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "error": fmt.Sprintf("cannot parse ENC entry: %q", err.Error())})
			continue
		}
		rg, err := mskit.OpenGDATFile(archiveFile(rebuiltMaster, archive))
		if err != nil {
			_ = sg.Close()
			problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "error": fmt.Sprintf("cannot parse ENC entry: %q", err.Error())})
			continue
		}
		if gidx < 0 || gidx >= sg.Count() || gidx >= rg.Count() {
			_ = sg.Close()
			_ = rg.Close()
			problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "error": "cannot parse ENC entry: ValueError('GDAT index outside source/rebuilt archive')"})
			continue
		}
		se, _ := sg.ReadEntry(gidx)
		re, _ := rg.ReadEntry(gidx)
		_ = sg.Close()
		_ = rg.Close()
		sf, err := mskit.ParseENCEntry(se)
		if err != nil {
			problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "error": fmt.Sprintf("cannot parse ENC entry: %q", err.Error())})
			continue
		}
		rf, err := mskit.ParseENCEntry(re)
		if err != nil {
			problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "error": fmt.Sprintf("cannot parse ENC entry: %q", err.Error())})
			continue
		}
		entriesChecked++
		if sf.Count != rf.Count {
			problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "error": "ENC row count changed", "source_count": sf.Count, "rebuilt_count": rf.Count})
			continue
		}
		byRow := map[int][]struct {
			Off  int
			Spec spec
		}{}
		for l, s := range groups[k] {
			byRow[l.Row] = append(byRow[l.Row], struct {
				Off  int
				Spec spec
			}{l.Offset, s})
		}
		for r := range byRow {
			sort.Slice(byRow[r], func(i, j int) bool { return byRow[r][i].Off > byRow[r][j].Off })
		}
		for ri := range sf.Rows {
			rowsChecked++
			sr, rr := sf.Rows[ri], rf.Rows[ri]
			if sr.LZOCompressed() != rr.LZOCompressed() {
				problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "row": ri, "error": "ENC row compression flag changed"})
				continue
			}
			sd, err := sr.DecodedPayload()
			if err != nil {
				problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "row": ri, "error": fmt.Sprintf("ENC row decode failed: %q", err.Error())})
				continue
			}
			rd, err := rr.DecodedPayload()
			if err != nil {
				problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "row": ri, "error": fmt.Sprintf("ENC row decode failed: %q", err.Error())})
				continue
			}
			expected := append([]byte(nil), sd...)
			rowFailed := false
			for _, ed := range byRow[ri] {
				o, s := ed.Off, ed.Spec
				end0 := o + len(s.Old)
				slotEnd := o + s.Capacity
				base := map[string]any{"archive": archive, "gdat_index": gidx, "row": ri, "str_offset": o, "record_key": s.RecordKey}
				addProblem := func(msg string) {
					m := map[string]any{}
					for k, v := range base {
						m[k] = v
					}
					m["error"] = msg
					problems = append(problems, m)
					rowFailed = true
				}
				if o < 0 || end0 >= len(expected) || slotEnd >= len(expected) {
					addProblem("ENC edit slot outside decoded row")
					continue
				}
				if !bytes.Equal(expected[o:end0], s.Old) {
					m := map[string]any{}
					for k, v := range base {
						m[k] = v
					}
					m["error"] = "ENC pristine old-byte mismatch"
					m["expected_old_hex"] = hex.EncodeToString(s.Old)
					m["source_hex"] = hex.EncodeToString(expected[o:end0])
					problems = append(problems, m)
					rowFailed = true
					continue
				}
				if expected[end0] != 0 {
					addProblem("ENC pristine literal not NUL-terminated")
					continue
				}
				if s.Capacity < len(s.Old) || len(s.New) > s.Capacity {
					addProblem("ENC slot capacity violation")
					continue
				}
				nonzero := false
				for _, b := range expected[end0 : slotEnd+1] {
					if b != 0 {
						nonzero = true
						break
					}
				}
				if nonzero {
					addProblem("ENC authorized slot crosses nonzero pristine bytes")
					continue
				}
				copy(expected[o:o+len(s.New)], s.New)
				for i := o + len(s.New); i <= slotEnd; i++ {
					expected[i] = 0
				}
				editsChecked++
			}
			if !rowFailed && !bytes.Equal(expected, rd) {
				first := 0
				min := len(expected)
				if len(rd) < min {
					min = len(rd)
				}
				for first < min && expected[first] == rd[first] {
					first++
				}
				problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "row": ri, "error": "ENC decoded row mismatch", "first_diff": first, "expected_size": len(expected), "rebuilt_size": len(rd)})
			}
			if sr.Aux != rr.Aux {
				problems = append(problems, map[string]any{"archive": archive, "gdat_index": gidx, "row": ri, "error": "ENC row aux changed", "source_aux": sr.Aux, "rebuilt_aux": rr.Aux})
			}
			if len(problems) >= 50 {
				break
			}
		}
		if len(problems) >= 50 {
			break
		}
	}
	if len(problems) > 50 {
		problems = problems[:50]
	}
	return map[string]any{"ok": len(problems) == 0, "entries_checked": entriesChecked, "rows_checked": rowsChecked, "edits_checked": editsChecked, "problems": problems}
}

func verifySniff(payload []byte) string {
	if len(payload) >= 4 && string(payload[:3]) == "CL3" && (payload[3] == 'L' || payload[3] == 'B') {
		return "cl3"
	}
	if len(payload) >= 4 && string(payload[:4]) == "GDAT" {
		return "gdat_nested"
	}
	if len(payload) >= 3 && string(payload[:3]) == "GST" {
		return "gstr"
	}
	if (len(payload) >= 3 && string(payload[:3]) == "GBN") || (len(payload) > 0x40 && string(payload[len(payload)-0x40:len(payload)-0x3d]) == "GBN") {
		return "gbin"
	}
	if len(payload) >= 5 && string(payload[:5]) == "STCM2" {
		return "stcm"
	}
	if len(payload) >= 4 && string(payload[:4]) == "BILZ" {
		return "bilz"
	}
	head := make([]byte, 0, 4)
	for i := 0; i < len(payload) && i < 4; i++ {
		c := payload[i]
		if c >= 32 && c < 127 {
			head = append(head, c)
		} else {
			head = append(head, '.')
		}
	}
	return "other:" + string(head)
}

func verifyKinds(master string, archives []int) (map[string]int, error) {
	wanted := map[int]bool{}
	for _, a := range archives {
		wanted[a] = true
	}
	paths, err := filepath.Glob(filepath.Join(master, "GxArchivedFile*.dat"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := map[string]int{}
	for _, path := range paths {
		a, err := sourceArchiveNumber(path)
		if err != nil {
			return nil, err
		}
		if len(wanted) > 0 && !wanted[a] {
			continue
		}
		g, err := mskit.OpenGDATFile(path)
		if err != nil {
			return nil, err
		}
		for i := 0; i < g.Count(); i++ {
			e, err := g.ReadEntry(i)
			if err != nil {
				_ = g.Close()
				return nil, err
			}
			payload, err := sourcePayload(e)
			if err != nil {
				_ = g.Close()
				return nil, err
			}
			out[verifySniff(payload)]++
		}
		_ = g.Close()
	}
	return out, nil
}

func statsMap(s sourceExtractStats, master string, archives []int) map[string]any {
	kinds, err := verifyKinds(master, archives)
	if err != nil {
		kinds = map[string]int{}
	}
	return map[string]any{"entries": s.Entries, "kinds": kinds, "records": s.Records, "by_role": s.ByRole, "decode_errors": s.DecodeErrors, "unparsed": s.Unparsed, "text_resources": s.TextResources}
}

func archiveListJSON(archives []int) any {
	if len(archives) == 0 {
		return nil
	}
	seen := map[int]bool{}
	out := []int{}
	for _, a := range archives {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Ints(out)
	return out
}

func nonENC(rows []textRecord) []textRecord {
	out := []textRecord{}
	for _, r := range rows {
		if jsonString(r["field_role"]) != "enc_string" {
			out = append(out, r)
		}
	}
	return out
}

// VerifyTextDatasetGo implements the read-only text verification contract used
// after a rebuild and writes the canonical verification report.
func VerifyTextDatasetGo(dataset, rebuiltMaster, expectedJSONL, sourceMaster string, archives []int, outReport string) (map[string]any, error) {
	// A verifier must not leave extracts in rebuilt/ or in a linked master view.
	// Earlier releases did precisely that, making the following build reject
	// the previous build's own products. Keep all verification extracts in
	// one disposable scratch directory on the same filesystem as the reports.
	scratchParent := filepath.Dir(filepath.Dir(outReport))
	scratch, err := os.MkdirTemp(scratchParent, ".text-verify-")
	if err != nil {
		return nil, fmt.Errorf("verification scratch: %w", err)
	}
	defer os.RemoveAll(scratch)
	workRoot := rebuiltMaster
	if sourceMaster != "" {
		workRoot, err = buildReadOnlyOverlay(rebuiltMaster, sourceMaster, scratch)
		if err != nil {
			return nil, err
		}
	}
	tmp := filepath.Join(scratch, "rebuilt-records.jsonl")
	stats, err := ExtractSourceDatasetGo(dataset, workRoot, archives, tmp, nil)
	if err != nil {
		return nil, err
	}
	gotRows, err := readTextRecords(tmp, archives)
	if err != nil {
		return nil, err
	}
	expectedRows, err := readTextRecords(expectedJSONL, archives)
	if err != nil {
		return nil, err
	}
	if sourceMaster == "" {
		got, err := logicalIndex(gotRows, nil)
		if err != nil {
			return nil, err
		}
		want, err := logicalIndex(expectedRows, nil)
		if err != nil {
			return nil, err
		}
		missing := setDifference(want.Rows, got.Rows)
		extra := setDifference(got.Rows, want.Rows)
		changed := []map[string]any{}
		rawMismatch := []map[string]any{}
		for _, key := range setIntersection(want.Rows, got.Rows) {
			a, b := want.Rows[key], got.Rows[key]
			if jsonString(a["text"]) != jsonString(b["text"]) {
				changed = append(changed, map[string]any{"logical_key": key, "expected_record_key": a["record_key"], "rebuilt_record_key": b["record_key"], "expected_text": a["text"], "got_text": b["text"]})
				continue
			}
			expectedRaw, e := encodeRecordText(a, jsonString(b["encoding"]))
			gotRaw, e2 := hex.DecodeString(jsonString(b["raw_hex"]))
			if e != nil || e2 != nil {
				msg := e
				if msg == nil {
					msg = e2
				}
				rawMismatch = append(rawMismatch, map[string]any{"logical_key": key, "error": msg.Error()})
				continue
			}
			if !bytes.Equal(expectedRaw, gotRaw) {
				rawMismatch = append(rawMismatch, map[string]any{"logical_key": key, "record_key": b["record_key"], "expected_hex": hex.EncodeToString(expectedRaw), "got_hex": hex.EncodeToString(gotRaw)})
			}
		}
		rep := map[string]any{"dataset": dataset, "rebuilt_master": rebuiltMaster, "archives_checked": archiveListJSON(archives), "expected_records": len(expectedRows), "rebuilt_records": len(gotRows), "expected_logical_records": len(want.Rows), "rebuilt_logical_records": len(got.Rows), "expected_logical_collisions": len(want.Collisions), "rebuilt_logical_collisions": len(got.Collisions), "missing_keys": len(missing), "extra_keys": len(extra), "changed_records": len(changed), "raw_mismatches": len(rawMismatch), "missing_sample": firstStrings(missing, 10), "extra_sample": firstStrings(extra, 10), "collision_sample": firstMaps(append(append([]map[string]any{}, want.Collisions...), got.Collisions...), 10), "raw_mismatch_sample": firstMaps(rawMismatch, 10), "changed": firstMaps(changed, 200), "extract_stats": statsMap(stats, workRoot, archives), "ok": len(missing) == 0 && len(extra) == 0 && len(changed) == 0 && len(rawMismatch) == 0 && len(want.Collisions) == 0 && len(got.Collisions) == 0}
		if err := writePrettyJSON(outReport, rep); err != nil {
			return nil, err
		}
		return rep, nil
	}

	sourceTmp := filepath.Join(scratch, "source-records.jsonl")
	sourceStats, err := ExtractSourceDatasetGo(dataset, sourceMaster, archives, sourceTmp, nil)
	if err != nil {
		return nil, err
	}
	sourceRows, err := readTextRecords(sourceTmp, archives)
	if err != nil {
		return nil, err
	}
	editedRows := []textRecord{}
	for _, r := range expectedRows {
		if textRecordEdited(r) {
			editedRows = append(editedRows, r)
		}
	}
	encAudit := verifyENCRows(sourceMaster, rebuiltMaster, archives, editedRows)
	sourceIdx, err := logicalIndex(nonENC(sourceRows), nil)
	if err != nil {
		return nil, err
	}
	gotIdx, err := logicalIndex(nonENC(gotRows), nil)
	if err != nil {
		return nil, err
	}
	wantIdx, err := logicalIndex(nonENC(editedRows), nonENC(sourceRows))
	if err != nil {
		rep := map[string]any{"dataset": dataset, "rebuilt_master": rebuiltMaster, "archives_checked": archiveListJSON(archives), "expected_records": len(expectedRows), "expected_edited_records": len(editedRows), "source_records": len(sourceRows), "rebuilt_records": len(gotRows), "missing_keys": 1, "extra_keys": 0, "changed_records": 0, "raw_mismatches": 0, "unexpected_source_changes": 0, "missing_sample": []string{fmt.Sprintf("non-ENC edited record rank missing from pristine extraction: %q", err.Error())}, "extra_sample": []string{}, "collision_sample": []map[string]any{}, "raw_mismatch_sample": []map[string]any{}, "unexpected_source_change_sample": []map[string]any{}, "changed": []map[string]any{}, "enc_audit": encAudit, "extract_stats": statsMap(stats, workRoot, archives), "source_extract_stats": statsMap(sourceStats, sourceMaster, archives), "ok": false}
		_ = writePrettyJSON(outReport, rep)
		return rep, nil
	}
	structMissing := setDifference(sourceIdx.Rows, gotIdx.Rows)
	structExtra := setDifference(gotIdx.Rows, sourceIdx.Rows)
	expectedMissing := setDifference(wantIdx.Rows, gotIdx.Rows)
	changed := []map[string]any{}
	rawMismatch := []map[string]any{}
	for _, key := range setIntersection(wantIdx.Rows, gotIdx.Rows) {
		a, b := wantIdx.Rows[key], gotIdx.Rows[key]
		if jsonString(a["text"]) != jsonString(b["text"]) {
			changed = append(changed, map[string]any{"logical_key": key, "expected_record_key": a["record_key"], "rebuilt_record_key": b["record_key"], "expected_text": a["text"], "got_text": b["text"]})
			continue
		}
		expectedRaw, e := encodeRecordText(a, jsonString(b["encoding"]))
		gotRaw, e2 := hex.DecodeString(jsonString(b["raw_hex"]))
		if e != nil || e2 != nil {
			msg := e
			if msg == nil {
				msg = e2
			}
			rawMismatch = append(rawMismatch, map[string]any{"logical_key": key, "error": msg.Error()})
			continue
		}
		if !bytes.Equal(expectedRaw, gotRaw) {
			rawMismatch = append(rawMismatch, map[string]any{"logical_key": key, "record_key": b["record_key"], "expected_hex": hex.EncodeToString(expectedRaw), "got_hex": hex.EncodeToString(gotRaw)})
		}
	}
	unexpected := []map[string]any{}
	for _, key := range setIntersection(sourceIdx.Rows, gotIdx.Rows) {
		if _, authorized := wantIdx.Rows[key]; authorized {
			continue
		}
		a, b := sourceIdx.Rows[key], gotIdx.Rows[key]
		if jsonString(a["text"]) != jsonString(b["text"]) || jsonString(a["raw_hex"]) != jsonString(b["raw_hex"]) {
			unexpected = append(unexpected, map[string]any{"logical_key": key, "source_record_key": a["record_key"], "rebuilt_record_key": b["record_key"], "source_text": a["text"], "rebuilt_text": b["text"]})
		}
	}
	entryChanges, err := verifyUneditedEntries(sourceMaster, rebuiltMaster, archives, editedRows)
	if err != nil {
		return nil, err
	}
	encOK, _ := encAudit["ok"].(bool)
	rep := map[string]any{"dataset": dataset, "rebuilt_master": rebuiltMaster, "archives_checked": archiveListJSON(archives), "expected_records": len(expectedRows), "expected_edited_records": len(editedRows), "source_records": len(sourceRows), "rebuilt_records": len(gotRows), "source_nonenc_logical_records": len(sourceIdx.Rows), "rebuilt_nonenc_logical_records": len(gotIdx.Rows), "expected_edited_nonenc_logical_records": len(wantIdx.Rows), "source_logical_collisions": len(sourceIdx.Collisions), "rebuilt_logical_collisions": len(gotIdx.Collisions), "expected_logical_collisions": len(wantIdx.Collisions), "missing_keys": len(structMissing), "extra_keys": len(structExtra), "expected_missing_keys": len(expectedMissing), "changed_records": len(changed), "raw_mismatches": len(rawMismatch), "unexpected_source_changes": len(unexpected), "unexpected_unedited_entry_changes": len(entryChanges), "missing_sample": firstStrings(structMissing, 10), "extra_sample": firstStrings(structExtra, 10), "expected_missing_sample": firstStrings(expectedMissing, 10), "collision_sample": firstMaps(append(append(append([]map[string]any{}, sourceIdx.Collisions...), wantIdx.Collisions...), gotIdx.Collisions...), 10), "raw_mismatch_sample": firstMaps(rawMismatch, 10), "unexpected_source_change_sample": firstMaps(unexpected, 10), "unexpected_unedited_entry_change_sample": firstMaps(entryChanges, 10), "enc_audit": encAudit, "changed": firstMaps(changed, 200), "extract_stats": statsMap(stats, workRoot, archives), "source_extract_stats": statsMap(sourceStats, sourceMaster, archives), "ok": len(structMissing) == 0 && len(structExtra) == 0 && len(expectedMissing) == 0 && len(changed) == 0 && len(rawMismatch) == 0 && len(unexpected) == 0 && len(entryChanges) == 0 && encOK && len(sourceIdx.Collisions) == 0 && len(wantIdx.Collisions) == 0 && len(gotIdx.Collisions) == 0}
	if err := writePrettyJSON(outReport, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

// VerifyTextDatasetGoStrict runs verification and fails closed when the report
// is not successful. The report is still written before the error is returned.
func VerifyTextDatasetGoStrict(dataset, rebuiltMaster, expectedJSONL, sourceMaster string, archives []int, outReport string) (map[string]any, error) {
	rep, err := VerifyTextDatasetGo(dataset, rebuiltMaster, expectedJSONL, sourceMaster, archives, outReport)
	if err != nil {
		return nil, err
	}
	ok, _ := rep["ok"].(bool)
	if !ok {
		return rep, fmt.Errorf("text verification failed for %s; see %s", dataset, outReport)
	}
	return rep, nil
}

func filesEqual(a, b string) (bool, error) {
	af, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer af.Close()
	bf, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer bf.Close()
	as, err := af.Stat()
	if err != nil {
		return false, err
	}
	bs, err := bf.Stat()
	if err != nil {
		return false, err
	}
	if as.Size() != bs.Size() {
		return false, nil
	}
	ab := make([]byte, 1024*1024)
	bb := make([]byte, len(ab))
	for {
		an, ae := af.Read(ab)
		bn, be := bf.Read(bb)
		if an != bn || !bytes.Equal(ab[:an], bb[:bn]) {
			return false, nil
		}
		if ae == io.EOF && be == io.EOF {
			return true, nil
		}
		if ae != nil && ae != io.EOF {
			return false, ae
		}
		if be != nil && be != io.EOF {
			return false, be
		}
	}
}

// DiffTextDatasetGo implements the read-only rebuilt-vs-source text diff report.
func DiffTextDatasetGo(dataset, rebuiltMaster, originalMaster string, archives []int, outReport string) (map[string]any, error) {
	// Keep the large extracted comparison records outside rebuilt/masterWindows.
	// Previously, this function left its two generated JSONLs in that directory,
	// and the next transactional rebuild refused its *own* prior output.
	// Use isolated owner-local scratch (same build volume) and remove on return.
	buildRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Clean(rebuiltMaster))))
	base, err := os.MkdirTemp(buildRoot, ".text-diff-")
	if err != nil {
		return nil, fmt.Errorf("create text diff scratch: %w", err)
	}
	defer os.RemoveAll(base)
	ap := filepath.Join(base, "rebuilt.jsonl")
	bp := filepath.Join(base, "source.jsonl")
	if _, err := ExtractSourceDatasetGo(dataset, rebuiltMaster, archives, ap, nil); err != nil {
		return nil, err
	}
	if _, err := ExtractSourceDatasetGo(dataset, originalMaster, archives, bp, nil); err != nil {
		return nil, err
	}
	aRows, err := readTextRecords(ap, archives)
	if err != nil {
		return nil, err
	}
	bRows, err := readTextRecords(bp, archives)
	if err != nil {
		return nil, err
	}
	aIdx, err := logicalIndex(nonENC(aRows), nil)
	if err != nil {
		return nil, err
	}
	bIdx, err := logicalIndex(nonENC(bRows), nil)
	if err != nil {
		return nil, err
	}
	diffs := []map[string]any{}
	for _, key := range setIntersection(aIdx.Rows, bIdx.Rows) {
		a, b := aIdx.Rows[key], bIdx.Rows[key]
		if jsonString(a["text"]) != jsonString(b["text"]) || jsonString(a["raw_hex"]) != jsonString(b["raw_hex"]) {
			diffs = append(diffs, map[string]any{"logical_key": key, "source_text": b["text"], "rebuilt_text": a["text"]})
		}
	}
	wanted := []int{}
	if len(archives) > 0 {
		seen := map[int]bool{}
		for _, a := range archives {
			if !seen[a] {
				seen[a] = true
				wanted = append(wanted, a)
			}
		}
		sort.Ints(wanted)
	} else {
		paths, _ := filepath.Glob(filepath.Join(rebuiltMaster, "GxArchivedFile*.dat"))
		for _, p := range paths {
			n, e := sourceArchiveNumber(p)
			if e == nil {
				wanted = append(wanted, n)
			}
		}
		sort.Ints(wanted)
	}
	encChanged := []map[string]any{}
	encErrors := []map[string]any{}
	for _, archive := range wanted {
		rp, sp := archiveFile(rebuiltMaster, archive), archiveFile(originalMaster, archive)
		if !file(rp) || !file(sp) {
			continue
		}
		sg, e := mskit.OpenGDATFile(sp)
		if e != nil {
			return nil, e
		}
		rg, e := mskit.OpenGDATFile(rp)
		if e != nil {
			_ = sg.Close()
			return nil, e
		}
		if sg.Count() != rg.Count() {
			encErrors = append(encErrors, map[string]any{"archive": archive, "error": "GDAT entry count changed"})
			_ = sg.Close()
			_ = rg.Close()
			continue
		}
		for i := 0; i < sg.Count(); i++ {
			se, _ := sg.ReadEntry(i)
			re, _ := rg.ReadEntry(i)
			stag, rtag := mskit.OrochiWrapperTag(se), mskit.OrochiWrapperTag(re)
			if stag != "enc" && rtag != "enc" {
				continue
			}
			if stag != rtag {
				encErrors = append(encErrors, map[string]any{"archive": archive, "gdat_index": i, "error": "ENC tag changed", "source_tag": stag, "rebuilt_tag": rtag})
				continue
			}
			sf, e := mskit.ParseENCEntry(se)
			if e != nil {
				encErrors = append(encErrors, map[string]any{"archive": archive, "gdat_index": i, "error": fmt.Sprintf("%q", e.Error())})
				continue
			}
			rf, e := mskit.ParseENCEntry(re)
			if e != nil {
				encErrors = append(encErrors, map[string]any{"archive": archive, "gdat_index": i, "error": fmt.Sprintf("%q", e.Error())})
				continue
			}
			if sf.Count != rf.Count {
				encErrors = append(encErrors, map[string]any{"archive": archive, "gdat_index": i, "error": "ENC row count changed", "source_count": sf.Count, "rebuilt_count": rf.Count})
				continue
			}
			for ri := range sf.Rows {
				sd, e := sf.Rows[ri].DecodedPayload()
				if e != nil {
					encErrors = append(encErrors, map[string]any{"archive": archive, "gdat_index": i, "row": ri, "error": fmt.Sprintf("%q", e.Error())})
					continue
				}
				rd, e := rf.Rows[ri].DecodedPayload()
				if e != nil {
					encErrors = append(encErrors, map[string]any{"archive": archive, "gdat_index": i, "row": ri, "error": fmt.Sprintf("%q", e.Error())})
					continue
				}
				if !bytes.Equal(sd, rd) {
					encChanged = append(encChanged, map[string]any{"archive": archive, "gdat_index": i, "row": ri, "source_size": len(sd), "rebuilt_size": len(rd)})
				}
			}
		}
		_ = sg.Close()
		_ = rg.Close()
	}
	archiveBytes := map[string]any{}
	paths, _ := filepath.Glob(filepath.Join(rebuiltMaster, "GxArchivedFile*.dat"))
	sort.Strings(paths)
	for _, p := range paths {
		name := filepath.Base(p)
		src := filepath.Join(originalMaster, name)
		rm, _ := os.Stat(p)
		var sourceSize any = nil
		ident := false
		if sm, e := os.Stat(src); e == nil {
			sourceSize = sm.Size()
			ident, _ = filesEqual(src, p)
		}
		archiveBytes[name] = map[string]any{"source_size": sourceSize, "rebuilt_size": rm.Size(), "byte_identical": ident}
	}
	rep := map[string]any{"dataset": dataset, "source_records": len(bRows), "rebuilt_records": len(aRows), "source_nonenc_logical_records": len(bIdx.Rows), "rebuilt_nonenc_logical_records": len(aIdx.Rows), "source_collisions": len(bIdx.Collisions), "rebuilt_collisions": len(aIdx.Collisions), "missing_nonenc": firstStrings(setDifference(bIdx.Rows, aIdx.Rows), 10), "extra_nonenc": firstStrings(setDifference(aIdx.Rows, bIdx.Rows), 10), "changed_nonenc_records": len(diffs), "changed": firstMaps(diffs, 200), "enc_scanner_source_records": countRole(bRows, "enc_string"), "enc_scanner_rebuilt_records": countRole(aRows, "enc_string"), "enc_changed_rows": len(encChanged), "enc_changed_row_sample": firstMaps(encChanged, 20), "enc_errors": firstMaps(encErrors, 20), "archive_bytes": archiveBytes}
	if err := writePrettyJSON(outReport, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func countRole(rows []textRecord, role string) int {
	n := 0
	for _, r := range rows {
		if jsonString(r["field_role"]) == role {
			n++
		}
	}
	return n
}
