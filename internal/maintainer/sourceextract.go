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
	"unicode/utf8"

	"maryskelter-unburger/internal/mskit"
)

type sourceExtractStats struct {
	Entries       int            `json:"entries"`
	Records       int            `json:"records"`
	ByRole        map[string]int `json:"by_role"`
	DecodeErrors  int            `json:"decode_errors"`
	Unparsed      map[string]int `json:"unparsed"`
	TextResources int            `json:"text_resources"`
}

type sourceParityDataset struct {
	Dataset            string `json:"dataset"`
	AcceptedRecords    int    `json:"accepted_records"`
	GeneratedRecords   int    `json:"generated_records"`
	AcceptedBytes      int64  `json:"accepted_bytes"`
	GeneratedBytes     int64  `json:"generated_bytes"`
	AcceptedSHA256     string `json:"accepted_sha256"`
	GeneratedSHA256    string `json:"generated_sha256"`
	ByteIdentical      bool   `json:"byte_identical"`
	FirstDiffLine      int    `json:"first_diff_line,omitempty"`
	AcceptedRecordKey  string `json:"accepted_record_key,omitempty"`
	GeneratedRecordKey string `json:"generated_record_key,omitempty"`
	FirstDiffField     string `json:"first_diff_field,omitempty"`
}

type sourceParityReport struct {
	Status      string                `json:"status"`
	CacheRoot   string                `json:"cache_root"`
	GeneratedAt string                `json:"generated_records_dir"`
	Datasets    []sourceParityDataset `json:"datasets"`
}

type sourceDatasetInfo struct {
	Game, Platform string
	Language       any
}

func sourceDatasetInfoFor(name string) (sourceDatasetInfo, error) {
	switch name {
	case "MS2_PC_LOCALIZED":
		return sourceDatasetInfo{"MS2", "PC", "en"}, nil
	case "MSF_PC_LOCALIZED":
		return sourceDatasetInfo{"MSF", "PC", "en"}, nil
	case "MSF_PC_SYSTEM":
		return sourceDatasetInfo{"MSF", "PC", nil}, nil
	default:
		return sourceDatasetInfo{}, fmt.Errorf("unsupported source-record dataset %q", name)
	}
}

func sourceArchiveNumber(path string) (int, error) {
	base := filepath.Base(path)
	if !strings.HasPrefix(base, "GxArchivedFile") || !strings.HasSuffix(base, ".dat") {
		return 0, fmt.Errorf("cannot parse archive number from %q", base)
	}
	return strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(base, "GxArchivedFile"), ".dat"))
}

func sourcePayload(entry []byte) ([]byte, error) {
	h, err := mskit.FindBILZHeader(entry, 0, 0x10000)
	if err != nil || h == nil {
		return append([]byte(nil), entry...), err
	}
	end := h.PayloadOffset() + h.CSize
	if end > len(entry) {
		return nil, fmt.Errorf("BILZ body exceeds entry")
	}
	return mskit.InflateBILZ(entry[h.PayloadOffset():end], h.Algo)
}

func sourceSniff(payload []byte) string {
	if len(payload) >= 4 && string(payload[:3]) == "CL3" && (payload[3] == 'L' || payload[3] == 'B') {
		return "cl3"
	}
	if len(payload) >= 4 && string(payload[:4]) == "GDAT" {
		return "gdat_nested"
	}
	if len(payload) >= 3 && string(payload[:3]) == "GST" {
		return "gstr"
	}
	if len(payload) > 0x40 && string(payload[len(payload)-0x40:len(payload)-0x3d]) == "GBN" {
		return "gbin"
	}
	if len(payload) >= 5 && string(payload[:5]) == "STCM2" {
		return "stcm"
	}
	if len(payload) >= 4 && string(payload[:4]) == "BILZ" {
		return "bilz"
	}
	return "other"
}

func sourceInnerKind(blob []byte) string {
	if len(blob) >= 5 && string(blob[:5]) == "STCM2" {
		return "stcm"
	}
	if len(blob) >= 3 && string(blob[:3]) == "GST" {
		return "gstr"
	}
	if len(blob) > 0x40 && string(blob[len(blob)-0x40:len(blob)-0x3d]) == "GBN" {
		return "gbin"
	}
	return "other"
}

func sourceDecodeUTF8(raw []byte) (string, int, error) {
	if !utf8.Valid(raw) {
		return "", 1, fmt.Errorf("native source extractor encountered non-UTF-8 text; production corpora are expected to be UTF-8 and parity must fail closed")
	}
	return string(raw), 0, nil
}

func sourceDetectLanguage(text string, fallback any) any {
	kana, hangul, han, latin := false, false, false, false
	for _, r := range text {
		switch {
		case r >= 0x3040 && r <= 0x30ff:
			kana = true
		case (r >= 0xac00 && r <= 0xd7a3) || (r >= 0x1100 && r <= 0x11ff):
			hangul = true
		case (r >= 0x4e00 && r <= 0x9fff) || (r >= 0x3400 && r <= 0x4dbf):
			han = true
		case r < 0x80 && ((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')):
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
	return fallback
}

func sourceRecord(dataset string, info sourceDatasetInfo, sourceFile, sourceSHA string, archive, gdat int, chain []string, cl3Index any, cl3Name any, container, table string, recordIndex, fieldIndex int, role, encoding, text string, raw []byte, control, repack map[string]any) map[string]any {
	return map[string]any{
		"record_key":     fmt.Sprintf("%s/a%03d/g%05d/%s/%s/r%05d/f%03d", dataset, archive, gdat, container, table, recordIndex, fieldIndex),
		"schema_version": 1, "source_dataset": dataset, "game": info.Game, "platform": info.Platform,
		"language": info.Language, "source_file": sourceFile, "source_file_sha256": sourceSHA,
		"archive_number": archive, "gdat_index": gdat, "format_chain": chain,
		"cl3_inner_index": cl3Index, "cl3_inner_name": cl3Name, "container_key": container,
		"table_key": table, "record_index": recordIndex, "field_index": fieldIndex, "field_role": role,
		"encoding": encoding, "text": text, "raw_hex": hex.EncodeToString(raw),
		"control_metadata": control, "repack_metadata": repack, "decode_errors": 0,
	}
}

func sourceGBNLRecords(blob []byte, dataset string, info sourceDatasetInfo, sourceFile, sourceSHA string, archive, gdat int, chain []string, cl3Index any, cl3Name any, container, table string, blobOffset any) ([]map[string]any, error) {
	g, err := mskit.ParseGBNL(blob)
	if err != nil {
		return nil, err
	}
	if g.Field30 != 8 {
		return nil, fmt.Errorf("GBNL field_30=%d requires CP932; current production source corpora are UTF-8", g.Field30)
	}
	var out []map[string]any
	for mi := 0; mi < g.CountMsgs; mi++ {
		for fi, it := range g.Items {
			if it.Kind != "str" {
				continue
			}
			f, err := g.Field(mi, fi)
			if err != nil {
				return nil, err
			}
			if f.IsNull {
				continue
			}
			text, _, err := sourceDecodeUTF8(f.StrRaw)
			if err != nil {
				return nil, err
			}
			var tableOff any
			if f.StrOffset != nil {
				tableOff = *f.StrOffset
			}
			out = append(out, sourceRecord(dataset, info, sourceFile, sourceSHA, archive, gdat, chain, cl3Index, cl3Name, container, table, mi, fi, "gnl_str", "utf-8", text, f.StrRaw,
				map[string]any{"endian": g.EndianMarker, "msg_descr_size": g.MsgDescrSize, "count_types": g.CountTypes, "flags": g.Flags, "field_28": g.Field28, "field_30": g.Field30, "field_absent": false, "string_is_null": false},
				map[string]any{"blob_type": "gnl", "blob_offset": blobOffset, "struct_offset": f.Offset, "table_offset": tableOff, "field_kind": f.Kind, "field_size": f.Size}))
		}
	}
	return out, nil
}

func sourceSystemRecords(entry []byte, tag, dataset string, info sourceDatasetInfo, sourceFile, sourceSHA string, archive, gdat int) ([]map[string]any, error) {
	var out []map[string]any
	switch tag {
	case "enc":
		e, err := mskit.ParseENCEntry(entry)
		if err != nil {
			return nil, err
		}
		ss, err := e.Strings()
		if err != nil {
			return nil, err
		}
		ord := map[int]int{}
		for _, s := range ss {
			text, _, err := sourceDecodeUTF8(s.Raw)
			if err != nil {
				return nil, err
			}
			fi := ord[s.RowIndex]
			ord[s.RowIndex] = fi + 1
			r := sourceRecord(dataset, info, sourceFile, sourceSHA, archive, gdat, []string{"GDAT", "BILZ", "ENC(row table)"}, nil, nil, "top", fmt.Sprintf("row%04d", s.RowIndex), s.RowIndex, fi, "enc_string", "utf-8", text, s.Raw,
				map[string]any{"terminated": s.Terminated, "row_count": e.Count, "string_ordinal_in_row": fi},
				map[string]any{"blob_type": "enc_row", "row": s.RowIndex, "str_offset": s.Offset, "str_len": len(s.Raw)})
			r["language"] = sourceDetectLanguage(text, info.Language)
			out = append(out, r)
		}
	case "bin":
		b, err := mskit.ParseBINEntry(entry)
		if err != nil {
			return nil, err
		}
		ss, err := b.Strings()
		if err != nil {
			return nil, err
		}
		for i, raw := range ss {
			text, _, err := sourceDecodeUTF8(raw)
			if err != nil {
				return nil, err
			}
			r := sourceRecord(dataset, info, sourceFile, sourceSHA, archive, gdat, []string{"GDAT", "BILZ", "BIN(string table)"}, nil, nil, "top", fmt.Sprintf("str%05d", i), i, 0, "bin_string", "utf-8", text, raw,
				map[string]any{"string_count": len(b.Offsets)}, map[string]any{"blob_type": "bin_string", "string_index": i, "str_len": len(raw)})
			r["language"] = sourceDetectLanguage(text, info.Language)
			out = append(out, r)
		}
	}
	return out, nil
}

func sourceEntryRecords(entry []byte, dataset string, info sourceDatasetInfo, sourceFile, sourceSHA string, archive, gdat int) ([]map[string]any, error) {
	tag := mskit.OrochiWrapperTag(entry)
	if tag == "enc" || tag == "bin" {
		return sourceSystemRecords(entry, tag, dataset, info, sourceFile, sourceSHA, archive, gdat)
	}
	payload, err := sourcePayload(entry)
	if err != nil {
		return nil, err
	}
	kind := sourceSniff(payload)
	var out []map[string]any
	switch kind {
	case "gstr", "gbin":
		return sourceGBNLRecords(payload, dataset, info, sourceFile, sourceSHA, archive, gdat, []string{"GDAT", "BILZ", strings.ToUpper(kind)}, nil, nil, "top", "blob0", nil)
	case "stcm":
		s, err := mskit.ParseSTCM(payload)
		if err != nil {
			return nil, nil
		}
		for _, item := range s.GBNLItems() {
			r, err := sourceGBNLRecords(item.Payload, dataset, info, sourceFile, sourceSHA, archive, gdat, []string{"GDAT", "BILZ", "STCM2", "GBNL"}, nil, nil, "top", fmt.Sprintf("d%06x", item.Offset), item.Offset)
			if err == nil {
				out = append(out, r...)
			}
		}
		return out, nil
	case "cl3":
		c, err := mskit.ParseCL3(payload)
		if err != nil {
			return nil, nil
		}
		for _, f := range c.Files {
			blob, err := c.FileData(f.Index)
			if err != nil {
				continue
			}
			switch sourceInnerKind(blob) {
			case "gstr", "gbin":
				ik := sourceInnerKind(blob)
				r, err := sourceGBNLRecords(blob, dataset, info, sourceFile, sourceSHA, archive, gdat, []string{"GDAT", "BILZ", "CL3", strings.ToUpper(ik)}, f.Index, f.Name, fmt.Sprintf("c%d", f.Index), "blob0", nil)
				if err == nil {
					out = append(out, r...)
				}
			case "stcm":
				s, err := mskit.ParseSTCM(blob)
				if err != nil {
					continue
				}
				for _, item := range s.GBNLItems() {
					r, err := sourceGBNLRecords(item.Payload, dataset, info, sourceFile, sourceSHA, archive, gdat, []string{"GDAT", "BILZ", "CL3", "STCM2", "GBNL"}, f.Index, f.Name, fmt.Sprintf("c%d", f.Index), fmt.Sprintf("d%06x", item.Offset), item.Offset)
					if err == nil {
						out = append(out, r...)
					}
				}
				for _, item := range s.StringItems() {
					raw := item.StringBytes()
					text, _, err := sourceDecodeUTF8(raw)
					if err != nil {
						return nil, err
					}
					out = append(out, sourceRecord(dataset, info, sourceFile, sourceSHA, archive, gdat, []string{"GDAT", "BILZ", "CL3", "STCM2", "string_data"}, f.Index, f.Name, fmt.Sprintf("c%d", f.Index), fmt.Sprintf("d%06x", item.Offset), 0, 0, "stcm_string", "utf-8", text, raw,
						map[string]any{"offset_unit": item.OffsetUnit, "field_8": item.Field8}, map[string]any{"blob_type": "stcm_string", "blob_offset": item.Offset, "struct_offset": 0, "table_offset": nil, "field_kind": "stcm_string", "field_size": len(item.Payload)}))
				}
			}
		}
	}
	return out, nil
}

// ExtractSourceDatasetGo is the native-Go dataset-level equivalent of legacy
// `ms_text_tool extract` for the three active PC source-cache corpora. It is
// active production source-cache extractor after full preserved-cache parity
// proved all three final corpora byte-identical to the accepted legacy cache.
func ExtractSourceDatasetGo(dataset, master string, archives []int, outPath string, stdout io.Writer) (sourceExtractStats, error) {
	info, err := sourceDatasetInfoFor(dataset)
	if err != nil {
		return sourceExtractStats{}, err
	}
	wanted := map[int]bool{}
	for _, a := range archives {
		wanted[a] = true
	}
	paths, err := filepath.Glob(filepath.Join(master, "GxArchivedFile*.dat"))
	if err != nil {
		return sourceExtractStats{}, err
	}
	sort.Strings(paths)
	stats := sourceExtractStats{ByRole: map[string]int{}, Unparsed: map[string]int{}}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return stats, err
	}
	fout, err := os.Create(outPath)
	if err != nil {
		return stats, err
	}
	fail := func(e error) (sourceExtractStats, error) { _ = fout.Close(); _ = os.Remove(outPath); return stats, e }
	for _, path := range paths {
		a, err := sourceArchiveNumber(path)
		if err != nil {
			return fail(err)
		}
		if len(wanted) > 0 && !wanted[a] {
			continue
		}
		sha, err := sha256Path(path)
		if err != nil {
			return fail(err)
		}
		gf, err := mskit.OpenGDATFile(path)
		if err != nil {
			return fail(err)
		}
		for i := 0; i < gf.Count(); i++ {
			stats.Entries++
			entry, err := gf.ReadEntry(i)
			if err != nil {
				_ = gf.Close()
				return fail(err)
			}
			recs, err := sourceEntryRecords(entry, dataset, info, filepath.Base(path), sha, a, i)
			if err != nil {
				_ = gf.Close()
				return fail(fmt.Errorf("%s a%03d/g%05d: %w", dataset, a, i, err))
			}
			if len(recs) > 0 {
				stats.TextResources++
			}
			for _, rec := range recs {
				line, err := canonicalJSONLine(rec)
				if err != nil {
					_ = gf.Close()
					return fail(err)
				}
				if _, err := fout.Write(append(line, '\n')); err != nil {
					_ = gf.Close()
					return fail(err)
				}
				stats.Records++
				stats.ByRole[rec["field_role"].(string)]++
			}
		}
		_ = gf.Close()
	}
	if err := fout.Sync(); err != nil {
		return fail(err)
	}
	if err := fout.Close(); err != nil {
		_ = os.Remove(outPath)
		return stats, err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "GO EXTRACT: %s records=%d entries=%d\n", dataset, stats.Records, stats.Entries)
	}
	return stats, nil
}

func countJSONLLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 32*1024*1024)
	n := 0
	for s.Scan() {
		if len(bytes.TrimSpace(s.Bytes())) > 0 {
			n++
		}
	}
	return n, s.Err()
}

func firstSourceRecordDiff(aPath, bPath string) (line int, aKey, bKey, field string, err error) {
	a, err := os.Open(aPath)
	if err != nil {
		return 0, "", "", "", err
	}
	defer a.Close()
	b, err := os.Open(bPath)
	if err != nil {
		return 0, "", "", "", err
	}
	defer b.Close()
	sa, sb := bufio.NewScanner(a), bufio.NewScanner(b)
	sa.Buffer(make([]byte, 64*1024), 32*1024*1024)
	sb.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for n := 1; ; n++ {
		oa, ob := sa.Scan(), sb.Scan()
		if !oa || !ob {
			if oa != ob {
				return n, "", "", "record_count", nil
			}
			break
		}
		if bytes.Equal(sa.Bytes(), sb.Bytes()) {
			continue
		}
		var ma, mb map[string]any
		_ = json.Unmarshal(sa.Bytes(), &ma)
		_ = json.Unmarshal(sb.Bytes(), &mb)
		aKey, _ = ma["record_key"].(string)
		bKey, _ = mb["record_key"].(string)
		keys := map[string]bool{}
		for k := range ma {
			keys[k] = true
		}
		for k := range mb {
			keys[k] = true
		}
		var ordered []string
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			xa, _ := json.Marshal(ma[k])
			xb, _ := json.Marshal(mb[k])
			if !bytes.Equal(xa, xb) {
				field = k
				break
			}
		}
		if field == "" {
			field = "serialization"
		}
		return n, aKey, bKey, field, nil
	}
	if err := sa.Err(); err != nil {
		return 0, "", "", "", err
	}
	if err := sb.Err(); err != nil {
		return 0, "", "", "", err
	}
	return 0, "", "", "", nil
}

func verifyParityArchiveSources(spec sourceCacheSpec, c CachedSources) error {
	needed := map[string]bool{}
	for ds, archives := range spec.TextArchives {
		game := "MSF"
		if strings.HasPrefix(ds, "MS2_") {
			game = "MS2"
		}
		for _, a := range archives {
			needed[game+"\x00"+fmt.Sprintf("GxArchivedFile%03d.dat", a)] = true
		}
	}
	for _, item := range spec.Files {
		if item.Kind != "archive" || !needed[item.Game+"\x00"+item.Name] {
			continue
		}
		master := c.FinaleMaster
		if item.Game == "MS2" {
			master = c.MS2Master
		}
		path := filepath.Join(master, item.Name)
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if item.Bytes != 0 && st.Size() != item.Bytes {
			return fmt.Errorf("%s size drift: got %d want %d", path, st.Size(), item.Bytes)
		}
		h, err := sha256Path(path)
		if err != nil {
			return err
		}
		if h != item.SHA256 {
			return fmt.Errorf("%s SHA drift: got %s want %s", path, h, item.SHA256)
		}
	}
	return nil
}

// ParitySourceRecords runs the native extractor against the preserved pristine
// cache, applies the already-native decoded-ENC exact materializer, and compares
// all three final JSONL corpora byte-for-byte with the accepted cache records.
// It never replaces accepted records and fails closed on any mismatch.
func ParitySourceRecords(root, cacheRoot, reportPath string, stdout io.Writer) error {
	if cacheRoot == "" {
		cacheRoot = filepath.Join(root, ".local", "source_cache")
	}
	if reportPath == "" {
		reportPath = filepath.Join(cacheRoot, "GO_SOURCE_RECORD_PARITY.json")
	}
	spec, err := sourceCacheSpecForRoot(root)
	if err != nil {
		return err
	}
	c := cachedSourcesAt(cacheRoot)
	if !dir(c.Records) {
		return fmt.Errorf("accepted source-cache records missing: %s", c.Records)
	}
	if err := verifyParityArchiveSources(spec, c); err != nil {
		return fmt.Errorf("pristine parity source verification failed: %w", err)
	}
	generated := filepath.Join(cacheRoot, "go_parity_records")
	_ = os.RemoveAll(generated)
	if err := os.MkdirAll(generated, 0o755); err != nil {
		return err
	}
	masters := map[string]string{"MS2_PC_LOCALIZED": c.MS2Master, "MSF_PC_LOCALIZED": c.FinaleMaster, "MSF_PC_SYSTEM": c.FinaleMaster}
	for _, ds := range sourceCacheRecordDatasets {
		if _, err := ExtractSourceDatasetGo(ds, masters[ds], spec.TextArchives[ds], filepath.Join(generated, ds+".jsonl"), stdout); err != nil {
			return err
		}
	}
	if err := MaterializeDecodedENCTargets(root, generated, c.MS2Master, c.FinaleMaster, stdout); err != nil {
		return err
	}
	rep := sourceParityReport{Status: "PASS", CacheRoot: cacheRoot, GeneratedAt: generated}
	allOK := true
	for _, ds := range sourceCacheRecordDatasets {
		a, b := filepath.Join(c.Records, ds+".jsonl"), filepath.Join(generated, ds+".jsonl")
		ar, err := countJSONLLines(a)
		if err != nil {
			return err
		}
		br, err := countJSONLLines(b)
		if err != nil {
			return err
		}
		as, err := os.Stat(a)
		if err != nil {
			return err
		}
		bs, err := os.Stat(b)
		if err != nil {
			return err
		}
		ah, err := sha256Path(a)
		if err != nil {
			return err
		}
		bh, err := sha256Path(b)
		if err != nil {
			return err
		}
		d := sourceParityDataset{Dataset: ds, AcceptedRecords: ar, GeneratedRecords: br, AcceptedBytes: as.Size(), GeneratedBytes: bs.Size(), AcceptedSHA256: ah, GeneratedSHA256: bh, ByteIdentical: ah == bh && as.Size() == bs.Size()}
		if !d.ByteIdentical {
			allOK = false
			d.FirstDiffLine, d.AcceptedRecordKey, d.GeneratedRecordKey, d.FirstDiffField, _ = firstSourceRecordDiff(a, b)
		}
		rep.Datasets = append(rep.Datasets, d)
		if stdout != nil {
			fmt.Fprintf(stdout, "PARITY %-17s records=%d/%d bytes=%d/%d sha=%s/%s identical=%t\n", ds, ar, br, as.Size(), bs.Size(), ah[:12], bh[:12], d.ByteIdentical)
		}
	}
	if !allOK {
		rep.Status = "FAIL"
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(rep, "", "  ")
	raw = append(raw, '\n')
	if err := os.WriteFile(reportPath, raw, 0o644); err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Parity report: %s\n", reportPath)
	}
	if !allOK {
		return fmt.Errorf("native-Go source-record full-corpus parity FAILED; accepted cache was not modified")
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "NATIVE-GO SOURCE-RECORD FULL CORPUS PARITY: PASS (3/3 byte-identical)")
	}
	return nil
}
