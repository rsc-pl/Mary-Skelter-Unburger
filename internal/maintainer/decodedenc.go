package maintainer

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"maryskelter-unburger/internal/mskit"
)

type decodedENCLocator struct {
	Dataset string
	Archive int
	GDAT    int
	Row     int
	Offset  int
}

type decodedENCTarget struct {
	WorkingID string
	Kind      string
	Localized string
	RecordKey string
	Field     int
}

type decodedENCSpec struct {
	Locator  decodedENCLocator
	Capacity int
	Target   decodedENCTarget
}

var decodedENCFieldRE = regexp.MustCompile(`/f(\d+)$`)

func loadDecodedENCSlots(path string) (map[decodedENCLocator]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, k := range header {
		idx[k] = i
	}
	required := []string{"dataset", "archive", "gdat_index", "row", "decoded_offset", "slot_capacity"}
	for _, k := range required {
		if _, ok := idx[k]; !ok {
			return nil, fmt.Errorf("decoded-ENC slot manifest missing column %q", k)
		}
	}
	out := map[decodedENCLocator]int{}
	line := 1
	for {
		row, err := r.Read()
		if err != nil {
			if err.Error() == "EOF" { // csv returns io.EOF but avoid another import solely here
				break
			}
			return nil, err
		}
		line++
		parse := func(k string) (int, error) {
			v, err := strconv.Atoi(row[idx[k]])
			if err != nil {
				return 0, fmt.Errorf("%s:%d: invalid %s: %w", path, line, k, err)
			}
			return v, nil
		}
		a, err := parse("archive")
		if err != nil {
			return nil, err
		}
		g, err := parse("gdat_index")
		if err != nil {
			return nil, err
		}
		rn, err := parse("row")
		if err != nil {
			return nil, err
		}
		off, err := parse("decoded_offset")
		if err != nil {
			return nil, err
		}
		cap, err := parse("slot_capacity")
		if err != nil {
			return nil, err
		}
		loc := decodedENCLocator{row[idx["dataset"]], a, g, rn, off}
		if _, exists := out[loc]; exists {
			return nil, fmt.Errorf("duplicate decoded-ENC slot manifest locator: %+v", loc)
		}
		out[loc] = cap
	}
	return out, nil
}

func loadDecodedENCTargets(translationsDir string) (map[decodedENCLocator]decodedENCTarget, error) {
	paths, err := filepath.Glob(filepath.Join(translationsDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := map[decodedENCLocator]decodedENCTarget{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			var row struct {
				ID     string `json:"id"`
				Source struct {
					LocalizedArchive int  `json:"localized_archive"`
					LocalizedGDAT    int  `json:"localized_gdat_index"`
					Row              int  `json:"row"`
					DecodedOffset    *int `json:"decoded_offset"`
				} `json:"source"`
				Fields []struct {
					Kind          string `json:"kind"`
					TargetDataset string `json:"target_dataset"`
					Localized     string `json:"LOCALIZED"`
					RecordKey     string `json:"localized_record_key"`
				} `json:"fields"`
			}
			if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
				f.Close()
				return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			if row.Source.DecodedOffset == nil {
				continue
			}
			for _, fld := range row.Fields {
				m := decodedENCFieldRE.FindStringSubmatch(fld.RecordKey)
				if len(m) != 2 {
					f.Close()
					return nil, fmt.Errorf("cannot recover field ordinal from %q", fld.RecordKey)
				}
				fi, err := strconv.Atoi(m[1])
				if err != nil {
					f.Close()
					return nil, err
				}
				loc := decodedENCLocator{fld.TargetDataset, row.Source.LocalizedArchive, row.Source.LocalizedGDAT, row.Source.Row, *row.Source.DecodedOffset}
				if _, exists := out[loc]; exists {
					f.Close()
					return nil, fmt.Errorf("duplicate decoded-ENC translation locator: %+v", loc)
				}
				out[loc] = decodedENCTarget{row.ID, fld.Kind, fld.Localized, fld.RecordKey, fi}
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func loadDecodedENCSpecs(root string) ([]decodedENCSpec, error) {
	slots, err := loadDecodedENCSlots(filepath.Join(root, "toolchain", "manifests", "decoded_enc_slots.tsv"))
	if err != nil {
		return nil, err
	}
	targets, err := loadDecodedENCTargets(filepath.Join(root, "translations"))
	if err != nil {
		return nil, err
	}
	if len(slots) != len(targets) {
		return nil, fmt.Errorf("decoded-ENC slot coverage mismatch: slots=%d targets=%d", len(slots), len(targets))
	}
	out := make([]decodedENCSpec, 0, len(slots))
	for loc, cap := range slots {
		t, ok := targets[loc]
		if !ok {
			return nil, fmt.Errorf("decoded-ENC slot has no translation target: %+v", loc)
		}
		out = append(out, decodedENCSpec{loc, cap, t})
	}
	for loc := range targets {
		if _, ok := slots[loc]; !ok {
			return nil, fmt.Errorf("decoded-ENC target has no pinned slot: %+v", loc)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Locator, out[j].Locator
		if a.Dataset != b.Dataset {
			return a.Dataset < b.Dataset
		}
		if a.Archive != b.Archive {
			return a.Archive < b.Archive
		}
		if a.GDAT != b.GDAT {
			return a.GDAT < b.GDAT
		}
		if a.Row != b.Row {
			return a.Row < b.Row
		}
		return a.Offset < b.Offset
	})
	return out, nil
}

func detectRecordLanguage(s string) any {
	kana, hangul, han, latin := false, false, false, false
	for _, r := range s {
		switch {
		case r >= 0x3040 && r <= 0x30ff:
			kana = true
		case (r >= 0xac00 && r <= 0xd7a3) || (r >= 0x1100 && r <= 0x11ff):
			hangul = true
		case (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf):
			han = true
		case r < 128 && unicode.IsLetter(r):
			latin = true
		}
	}
	if kana {
		return "ja"
	}
	if hangul {
		return "ko"
	}
	if han {
		return "zh"
	}
	if latin {
		return "en"
	}
	return nil
}

func decodedENCDatasetMeta(dataset string) (game, platform string, language any, err error) {
	switch dataset {
	case "MS2_PC_LOCALIZED":
		return "MS2", "PC", "en", nil
	case "MSF_PC_LOCALIZED":
		return "MSF", "PC", "en", nil
	case "MSF_PC_SYSTEM":
		return "MSF", "PC", nil, nil
	default:
		return "", "", nil, fmt.Errorf("unsupported decoded-ENC dataset %q", dataset)
	}
}

func decodedENCRecord(spec decodedENCSpec, sourceFile, sourceSHA string, ef mskit.ENCFile) (map[string]any, error) {
	loc := spec.Locator
	if loc.Row < 0 || loc.Row >= len(ef.Rows) {
		return nil, fmt.Errorf("%s: row %d outside ENC row table", spec.Target.RecordKey, loc.Row)
	}
	row := ef.Rows[loc.Row]
	decoded, err := row.DecodedPayload()
	if err != nil {
		return nil, err
	}
	return decodedENCRecordWithRow(spec, sourceFile, sourceSHA, ef, decoded)
}

func decodedENCRecordWithRow(spec decodedENCSpec, sourceFile, sourceSHA string, ef mskit.ENCFile, decoded []byte) (map[string]any, error) {
	loc := spec.Locator
	if loc.Row < 0 || loc.Row >= len(ef.Rows) {
		return nil, fmt.Errorf("%s: row %d outside ENC row table", spec.Target.RecordKey, loc.Row)
	}
	row := ef.Rows[loc.Row]
	old := []byte(spec.Target.Localized)
	end0 := loc.Offset + len(old)
	slotEnd := loc.Offset + spec.Capacity
	label := spec.Target.RecordKey
	if loc.Offset < 0 || end0 >= len(decoded) || slotEnd >= len(decoded) {
		return nil, fmt.Errorf("%s: decoded-ENC target/slot outside pristine row", label)
	}
	if !bytes.Equal(decoded[loc.Offset:end0], old) {
		return nil, fmt.Errorf("%s: LOCALIZED bytes do not match pristine decoded row", label)
	}
	if decoded[end0] != 0 {
		return nil, fmt.Errorf("%s: pristine decoded-ENC literal is not NUL-terminated", label)
	}
	if spec.Capacity < len(old) {
		return nil, fmt.Errorf("%s: pinned slot capacity is shorter than localized literal", label)
	}
	for _, b := range decoded[end0 : slotEnd+1] {
		if b != 0 {
			return nil, fmt.Errorf("%s: pinned slot capacity crosses nonzero pristine row bytes", label)
		}
	}
	game, platform, fallbackLang, err := decodedENCDatasetMeta(loc.Dataset)
	if err != nil {
		return nil, err
	}
	lang := detectRecordLanguage(spec.Target.Localized)
	if lang == nil {
		lang = fallbackLang
	}
	chain := []string{"GDAT", "BILZ", "ENC(row table)"}
	if row.LZOCompressed() {
		chain = append(chain, "LZO1X(decoded row)")
	}
	return map[string]any{
		"record_key":         label,
		"schema_version":     1,
		"source_dataset":     loc.Dataset,
		"game":               game,
		"platform":           platform,
		"language":           lang,
		"source_file":        sourceFile,
		"source_file_sha256": sourceSHA,
		"archive_number":     loc.Archive,
		"gdat_index":         loc.GDAT,
		"format_chain":       chain,
		"cl3_inner_index":    nil,
		"cl3_inner_name":     nil,
		"container_key":      "top",
		"table_key":          fmt.Sprintf("row%04d", loc.Row),
		"record_index":       loc.Row,
		"field_index":        spec.Target.Field,
		"field_role":         "enc_string",
		"encoding":           "utf-8",
		"text":               spec.Target.Localized,
		"raw_hex":            hex.EncodeToString(old),
		"control_metadata": map[string]any{
			"terminated":            true,
			"row_count":             ef.Count,
			"string_ordinal_in_row": spec.Target.Field,
			"decoded_row":           true,
			"inner_lzo1x":           row.LZOCompressed(),
		},
		"repack_metadata": map[string]any{
			"blob_type":     "enc_row",
			"row":           loc.Row,
			"str_offset":    loc.Offset,
			"str_len":       len(old),
			"slot_capacity": spec.Capacity,
		},
		"decode_errors": 0,
	}, nil
}

func canonicalJSONLine(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	// Python's json.dumps(..., sort_keys=True) uses separators ", " and ": ".
	// encoding/json already sorts map keys, so add exactly those structural
	// spaces without touching punctuation inside JSON strings.
	out := make([]byte, 0, len(raw)+len(raw)/16)
	inString, escaped := false, false
	for _, c := range raw {
		out = append(out, c)
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			continue
		}
		if c == ',' || c == ':' {
			out = append(out, ' ')
		}
	}
	return out, nil
}

func decodedENCSpecsByEntry(specs []decodedENCSpec) map[[3]string][]decodedENCSpec {
	out := map[[3]string][]decodedENCSpec{}
	for _, s := range specs {
		key := [3]string{s.Locator.Dataset, strconv.Itoa(s.Locator.Archive), strconv.Itoa(s.Locator.GDAT)}
		out[key] = append(out[key], s)
	}
	return out
}

func decodedENCEntryKey(dataset string, archive, gdat int) [3]string {
	return [3]string{dataset, strconv.Itoa(archive), strconv.Itoa(gdat)}
}

func ensureNoUnexpectedDecodedENCSpecs(specs []decodedENCSpec) error {
	for _, s := range specs {
		if strings.TrimSpace(s.Target.RecordKey) == "" || strings.TrimSpace(s.Target.Localized) == "" && s.Capacity < 0 {
			return fmt.Errorf("invalid decoded-ENC spec %+v", s.Locator)
		}
	}
	return nil
}

type existingDecodedENCRecord struct {
	SourceDataset    string `json:"source_dataset"`
	ArchiveNumber    int    `json:"archive_number"`
	SourceFileSHA256 string `json:"source_file_sha256"`
	FieldRole        string `json:"field_role"`
	RepackMetadata   struct {
		Row       *int `json:"row"`
		StrOffset *int `json:"str_offset"`
	} `json:"repack_metadata"`
}

func existingDecodedENCLocator(rec existingDecodedENCRecord) (decodedENCLocator, bool) {
	if rec.FieldRole != "enc_string" || rec.RepackMetadata.Row == nil || rec.RepackMetadata.StrOffset == nil {
		return decodedENCLocator{}, false
	}
	// The record key/source metadata carries the GDAT index, which the compact
	// removal parser deliberately does not decode. The caller fills it from the
	// record_key using the stable /gNNNNN/ component.
	return decodedENCLocator{Dataset: rec.SourceDataset, Archive: rec.ArchiveNumber, Row: *rec.RepackMetadata.Row, Offset: *rec.RepackMetadata.StrOffset}, true
}

var decodedENCGDATKeyRE = regexp.MustCompile(`/g(\d{5})/`)

func materializeDecodedENCDataset(root, dataset, recordsPath, master string, specs []decodedENCSpec, stdout io.Writer) error {
	wanted := map[decodedENCLocator]decodedENCSpec{}
	for _, s := range specs {
		if s.Locator.Dataset == dataset {
			wanted[s.Locator] = s
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	in, err := os.Open(recordsPath)
	if err != nil {
		return err
	}
	tmp := recordsPath + ".exact-enc.tmp"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		in.Close()
		return err
	}
	cleanup := func(e error) error {
		_ = in.Close()
		_ = out.Close()
		_ = os.Remove(tmp)
		return e
	}
	sourceSHA := map[int]string{}
	removed, kept := 0, 0
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec existingDecodedENCRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return cleanup(err)
		}
		if rec.SourceFileSHA256 != "" {
			if _, ok := sourceSHA[rec.ArchiveNumber]; !ok {
				sourceSHA[rec.ArchiveNumber] = rec.SourceFileSHA256
			}
		}
		loc, ok := existingDecodedENCLocator(rec)
		if ok {
			var key struct {
				RecordKey string `json:"record_key"`
			}
			if err := json.Unmarshal(line, &key); err != nil {
				return cleanup(err)
			}
			m := decodedENCGDATKeyRE.FindStringSubmatch(key.RecordKey)
			if len(m) == 2 {
				g, _ := strconv.Atoi(m[1])
				loc.GDAT = g
				if _, replace := wanted[loc]; replace {
					removed++
					continue
				}
			}
		}
		if _, err := out.Write(line); err != nil {
			return cleanup(err)
		}
		if _, err := out.Write([]byte{'\n'}); err != nil {
			return cleanup(err)
		}
		kept++
	}
	if err := sc.Err(); err != nil {
		return cleanup(err)
	}
	if err := in.Close(); err != nil {
		return cleanup(err)
	}

	// Group exact targets by archive/GDAT so every archive and compressed row is
	// opened/decoded once, even though tens of thousands of fields may point into it.
	type entryKey struct{ Archive, GDAT int }
	groups := map[entryKey][]decodedENCSpec{}
	var keys []entryKey
	for _, s := range specs {
		if s.Locator.Dataset != dataset {
			continue
		}
		k := entryKey{s.Locator.Archive, s.Locator.GDAT}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], s)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Archive != keys[j].Archive {
			return keys[i].Archive < keys[j].Archive
		}
		return keys[i].GDAT < keys[j].GDAT
	})
	for _, k := range keys {
		sort.Slice(groups[k], func(i, j int) bool {
			a, b := groups[k][i].Locator, groups[k][j].Locator
			if a.Row != b.Row {
				return a.Row < b.Row
			}
			return a.Offset < b.Offset
		})
	}

	currentArchive := -1
	var gf *mskit.GDATFile
	defer func() {
		if gf != nil {
			_ = gf.Close()
		}
	}()
	generated := 0
	for _, k := range keys {
		if currentArchive != k.Archive {
			if gf != nil {
				_ = gf.Close()
				gf = nil
			}
			archivePath := filepath.Join(master, fmt.Sprintf("GxArchivedFile%03d.dat", k.Archive))
			gf, err = mskit.OpenGDATFile(archivePath)
			if err != nil {
				return cleanup(err)
			}
			currentArchive = k.Archive
			if sourceSHA[k.Archive] == "" {
				h, err := sha256Path(archivePath)
				if err != nil {
					return cleanup(err)
				}
				sourceSHA[k.Archive] = h
			}
		}
		entry, err := gf.ReadEntry(k.GDAT)
		if err != nil {
			return cleanup(err)
		}
		ef, err := mskit.ParseENCEntry(entry)
		if err != nil {
			return cleanup(fmt.Errorf("%s a%03d/g%05d: %w", dataset, k.Archive, k.GDAT, err))
		}
		rowCache := map[int][]byte{}
		for _, s := range groups[k] {
			dec, ok := rowCache[s.Locator.Row]
			if !ok {
				dec, err = ef.Rows[s.Locator.Row].DecodedPayload()
				if err != nil {
					return cleanup(err)
				}
				rowCache[s.Locator.Row] = dec
			}
			rec, err := decodedENCRecordWithRow(s, fmt.Sprintf("GxArchivedFile%03d.dat", k.Archive), sourceSHA[k.Archive], ef, dec)
			if err != nil {
				return cleanup(err)
			}
			line, err := canonicalJSONLine(rec)
			if err != nil {
				return cleanup(err)
			}
			if _, err := out.Write(line); err != nil {
				return cleanup(err)
			}
			if _, err := out.Write([]byte{'\n'}); err != nil {
				return cleanup(err)
			}
			generated++
		}
	}
	if err := out.Sync(); err != nil {
		return cleanup(err)
	}
	if err := out.Close(); err != nil {
		return cleanup(err)
	}
	if generated != len(wanted) {
		_ = os.Remove(tmp)
		return fmt.Errorf("%s: generated decoded-ENC records=%d wanted=%d", dataset, generated, len(wanted))
	}
	if err := os.Rename(tmp, recordsPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "%s: exact decoded-ENC targets=%d replaced_scanner_records=%d generated=%d retained=%d\n", dataset, len(wanted), removed, generated, kept)
	}
	return nil
}

// MaterializeDecodedENCTargets replaces scanner-derived ENC records with exact
// records from the pinned decoded-row locators and slot-capacity manifest.
func MaterializeDecodedENCTargets(root, recordsDir, ms2Master, msfMaster string, stdout io.Writer) error {
	specs, err := loadDecodedENCSpecs(root)
	if err != nil {
		return err
	}
	if err := ensureNoUnexpectedDecodedENCSpecs(specs); err != nil {
		return err
	}
	masters := map[string]string{"MS2_PC_LOCALIZED": ms2Master, "MSF_PC_LOCALIZED": msfMaster, "MSF_PC_SYSTEM": msfMaster}
	for _, dataset := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"} {
		path := filepath.Join(recordsDir, dataset+".jsonl")
		if !file(path) {
			return fmt.Errorf("missing freshly extracted source records: %s", path)
		}
		if err := materializeDecodedENCDataset(root, dataset, path, masters[dataset], specs, stdout); err != nil {
			return err
		}
	}
	return nil
}
