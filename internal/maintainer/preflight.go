package maintainer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
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
)

var (
	workflowMarkerRE = regexp.MustCompile(`(?i)(^|_)(REVIEW|REVIEWED|REUSE|BATCH|CHECKPOINT|OPEN_HOLD|KEEP|APPLY|CHANGED)($|_)`)
	namedKinshipRE   = regexp.MustCompile(`(?i)\b[A-Za-z][A-Za-z'’.\-]*[A-Za-z]-(onee|onii|nee|nii)-(san|chan|sama)\b`)
	ordinaryFamilyRE = regexp.MustCompile(`(?i)\b(otou|okaa|imouto|otouto|obaa|ojii|oji|oba)-(san|chan|sama)\b`)
	archiveKeyRE     = regexp.MustCompile(`/a(\d{3})/`)
)

var publicRowKeys = map[string]bool{"id": true, "game": true, "scope": true, "source": true, "fields": true}
var publicFieldKeys = map[string]bool{
	"kind": true, "JP": true, "LOCALIZED": true, "EN": true, "field_ordinal": true,
	"target_dataset": true, "localized_record_key": true, "jp_record_key": true, "mapping_status": true,
}
var forbiddenWorkflowKeys = map[string]bool{
	"review_status": true, "row_review_status": true, "status": true, "notes": true, "review": true,
	"decision": true, "batch": true, "checkpoint": true, "review_batch": true, "review_notes": true,
}

type TranslationStats struct {
	Records              int `json:"records"`
	Fields               int `json:"fields"`
	ExactJPFields        int `json:"exact_jp_fields"`
	SourceAbsentJPFields int `json:"source_absent_jp_fields"`
	PendingJPFields      int `json:"pending_jp_fields"`
	ChangedENFields      int `json:"changed_EN_fields"`
}

type translationField struct {
	Kind               string  `json:"kind"`
	JP                 *string `json:"JP"`
	Localized          string  `json:"LOCALIZED"`
	EN                 string  `json:"EN"`
	TargetDataset      string  `json:"target_dataset"`
	LocalizedRecordKey string  `json:"localized_record_key"`
	MappingStatus      string  `json:"mapping_status"`
}

type workingEdit struct {
	Dataset   string
	Localized string
	EN        string
	WorkingID string
	Kind      string
}

type recordChange struct {
	RecordKey string `json:"record_key"`
	WorkingID string `json:"working_id"`
	Kind      string `json:"kind"`
	From      string `json:"from"`
	To        string `json:"to"`
}

type recordSummary struct {
	RawRecords          int            `json:"raw_records"`
	MappedWorkingFields int            `json:"mapped_working_fields"`
	ChangedFields       int            `json:"changed_fields"`
	Changes             []recordChange `json:"changes"`
}

func scanWorkflowMetadata(v any, path string, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val := x[k]
			kp := k
			if path != "" {
				kp = path + "." + k
			}
			if forbiddenWorkflowKeys[strings.ToLower(k)] {
				*out = append(*out, fmt.Sprintf("forbidden workflow key %q", kp))
			}
			if s, ok := val.(string); ok && k != "JP" && k != "LOCALIZED" && k != "EN" && workflowMarkerRE.MatchString(s) {
				*out = append(*out, fmt.Sprintf("workflow marker in metadata %s: %q", kp, s))
			}
			scanWorkflowMetadata(val, kp, out)
		}
	case []any:
		for i, val := range x {
			scanWorkflowMetadata(val, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

func ValidateTranslations(root string, stdout io.Writer) (map[string]TranslationStats, error) {
	paths, err := filepath.Glob(filepath.Join(root, "translations", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	stats := make(map[string]TranslationStats, len(paths))
	var errs []string
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		s := TranslationStats{}
		seen := map[string]bool{}
		sc := bufio.NewScanner(f)
		buf := make([]byte, 64*1024)
		sc.Buffer(buf, 16*1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := sc.Bytes()
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			s.Records++
			var generic map[string]any
			if err := json.Unmarshal(line, &generic); err != nil {
				f.Close()
				return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), lineNo, err)
			}
			for k := range generic {
				if !publicRowKeys[k] {
					errs = append(errs, fmt.Sprintf("%s:%d: unexpected public row key: %s", filepath.Base(path), lineNo, k))
				}
			}
			id, _ := generic["id"].(string)
			scope, _ := generic["scope"].(string)
			if strings.Contains(strings.ToUpper(filepath.Base(path)), "DLC") || strings.Contains(strings.ToUpper(id), "DLC") || strings.Contains(strings.ToUpper(scope), "DLC") {
				errs = append(errs, fmt.Sprintf("%s:%d: public corpus identity must describe the physical archive/surface, not workflow/DLC grouping", filepath.Base(path), lineNo))
			}
			var wf []string
			scanWorkflowMetadata(generic, "", &wf)
			for _, msg := range wf {
				errs = append(errs, fmt.Sprintf("%s:%d: %s", filepath.Base(path), lineNo, msg))
			}
			rawFields, ok := generic["fields"].([]any)
			if !ok {
				f.Close()
				return nil, fmt.Errorf("%s:%d: missing/invalid fields", filepath.Base(path), lineNo)
			}
			for _, rv := range rawFields {
				fm, ok := rv.(map[string]any)
				if !ok {
					f.Close()
					return nil, fmt.Errorf("%s:%d: invalid field object", filepath.Base(path), lineNo)
				}
				for k := range fm {
					if !publicFieldKeys[k] {
						errs = append(errs, fmt.Sprintf("%s:%d: unexpected public field key: %s", filepath.Base(path), lineNo, k))
					}
				}
				b, _ := json.Marshal(fm)
				var fld translationField
				if err := json.Unmarshal(b, &fld); err != nil {
					f.Close()
					return nil, fmt.Errorf("%s:%d: %w", filepath.Base(path), lineNo, err)
				}
				s.Fields++
				if fld.LocalizedRecordKey == "" {
					f.Close()
					return nil, fmt.Errorf("%s:%d: missing localized_record_key", filepath.Base(path), lineNo)
				}
				if seen[fld.LocalizedRecordKey] {
					errs = append(errs, fmt.Sprintf("%s:%d: duplicate target key %s", filepath.Base(path), lineNo, fld.LocalizedRecordKey))
				}
				seen[fld.LocalizedRecordKey] = true
				if strings.HasPrefix(fld.MappingStatus, "EXACT") {
					s.ExactJPFields++
				} else if fld.MappingStatus == "SOURCE_ABSENT_IN_PC_JP_RESOURCE" {
					s.SourceAbsentJPFields++
				} else {
					s.PendingJPFields++
				}
				if fld.EN != fld.Localized {
					s.ChangedENFields++
				}
				if namedKinshipRE.MatchString(fld.EN) {
					errs = append(errs, fmt.Sprintf("%s:%d: named kinship suffix must use one separator only (e.g. Name-neesama): %q", filepath.Base(path), lineNo, fld.EN))
				}
				if ordinaryFamilyRE.MatchString(fld.EN) {
					errs = append(errs, fmt.Sprintf("%s:%d: ordinary family term must be translated naturally, not romanized: %q", filepath.Base(path), lineNo, fld.EN))
				}
				if fld.JP == nil && strings.HasPrefix(fld.MappingStatus, "EXACT") {
					errs = append(errs, fmt.Sprintf("%s:%d: exact mapping with JP=null", filepath.Base(path), lineNo))
				}
				if fld.JP != nil && fld.MappingStatus == "SOURCE_ABSENT_IN_PC_JP_RESOURCE" {
					errs = append(errs, fmt.Sprintf("%s:%d: source-absent mapping with non-null JP", filepath.Base(path), lineNo))
				}
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
		stats[filepath.Base(path)] = s
	}
	if stdout != nil {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(stats)
	}
	if len(errs) > 0 {
		limit := len(errs)
		if limit > 100 {
			limit = 100
		}
		return stats, fmt.Errorf("translation validation failed (%d errors):\n%s", len(errs), strings.Join(errs[:limit], "\n"))
	}
	return stats, nil
}

func PreflightTitleFooter(root string, stdout io.Writer) error {
	type variant struct {
		TIDSHA256 string `json:"tid_sha256"`
	}
	type asset struct {
		ID             string    `json:"id"`
		Enabled        bool      `json:"enabled"`
		Dataset        string    `json:"dataset"`
		Archive        int       `json:"archive"`
		GDATIndex      int       `json:"gdat_index"`
		CL3InnerName   string    `json:"cl3_inner_name"`
		Renderer       string    `json:"renderer"`
		CopyrightRect  []int     `json:"copyright_rect"`
		SourceVariants []variant `json:"source_variants"`
	}
	var manifest struct {
		Assets []asset `json:"assets"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	by := map[string]asset{}
	for _, a := range manifest.Assets {
		by[a.ID] = a
	}
	type expectedAsset struct {
		dataset       string
		archive, gidx int
		inner         string
		rect          [4]int
		sha           string
	}
	expected := map[string]expectedAsset{
		"MS2_TITLE_MOD_FOOTER_MAIN": {"MS2_PC_LOCALIZED", 55, 1821, "titlemenu.tid", [4]int{50, 2544, 900, 2634}, "101c81d0e9953dfea166ed01852e3f4f50c20f575f59d1f990be41f40c9e2311"},
		"MS2_TITLE_MOD_FOOTER_BE":   {"MS2_PC_LOCALIZED", 55, 1822, "TitleBE.tid", [4]int{1312, 1660, 2040, 1732}, "44cd6d66ec412412b650b431120c4c04390cb8574d357905bd39b5ac54d66462"},
		"MSF_TITLE_MOD_FOOTER":      {"MSF_PC_LOCALIZED", 73, 220, "title_f.tid", [4]int{374, 1796, 1230, 1886}, "4cfbfb005ba44299615259ece0485ffa56c4a427c06b82917ed21e12b441d7fd"},
	}
	vraw, err := os.ReadFile(filepath.Join(root, "version.txt"))
	if err != nil {
		return err
	}
	version := strings.TrimSpace(string(vraw))
	if version == "" || strings.ContainsAny(version, "\r\n") {
		return fmt.Errorf("TITLE FOOTER PREFLIGHT: FAIL — malformed version.txt")
	}
	for id, e := range expected {
		a, ok := by[id]
		if !ok || !a.Enabled {
			return fmt.Errorf("TITLE FOOTER PREFLIGHT: FAIL — missing/disabled %s", id)
		}
		if a.Dataset != e.dataset || a.Archive != e.archive || a.GDATIndex != e.gidx || a.CL3InnerName != e.inner {
			return fmt.Errorf("TITLE FOOTER PREFLIGHT: FAIL — source locator drift for %s", id)
		}
		if a.Renderer != "title_copyright_footer_chiaro_v2" || len(a.CopyrightRect) != 4 || a.CopyrightRect[0] != e.rect[0] || a.CopyrightRect[1] != e.rect[1] || a.CopyrightRect[2] != e.rect[2] || a.CopyrightRect[3] != e.rect[3] {
			return fmt.Errorf("TITLE FOOTER PREFLIGHT: FAIL — renderer/rect drift for %s", id)
		}
		if len(a.SourceVariants) != 1 || a.SourceVariants[0].TIDSHA256 != e.sha {
			return fmt.Errorf("TITLE FOOTER PREFLIGHT: FAIL — source pin drift for %s", id)
		}
	}
	for _, a := range manifest.Assets {
		if (strings.HasPrefix(a.ID, "MS2_TITLE_MOD_FOOTER") || strings.HasPrefix(a.ID, "MSF_TITLE_MOD_FOOTER")) && a.CL3InnerName == "title_moji.tid" {
			return fmt.Errorf("TITLE FOOTER PREFLIGHT: FAIL — rejected title_moji target is active")
		}
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "TITLE FOOTER PREFLIGHT: PASS — source-pinned copyright sprites; label=UNBURGER MOD V%s\n", version)
	}
	return nil
}

func collectWorkingEdits(root string) (map[string]workingEdit, error) {
	paths, err := filepath.Glob(filepath.Join(root, "translations", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	edits := map[string]workingEdit{}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			var row struct {
				ID     string             `json:"id"`
				Fields []translationField `json:"fields"`
			}
			if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
				f.Close()
				return nil, err
			}
			for _, fld := range row.Fields {
				k := fld.LocalizedRecordKey
				if _, exists := edits[k]; exists {
					f.Close()
					return nil, fmt.Errorf("duplicate working target key: %s", k)
				}
				edits[k] = workingEdit{Dataset: fld.TargetDataset, Localized: fld.Localized, EN: fld.EN, WorkingID: row.ID, Kind: fld.Kind}
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return edits, nil
}

func pythonStyleJSONLine(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	out := make([]byte, 0, len(raw)+32)
	inStr := false
	esc := false
	for _, c := range raw {
		if inStr {
			out = append(out, c)
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
			out = append(out, c)
		case ',':
			out = append(out, ',', ' ')
		case ':':
			out = append(out, ':', ' ')
		default:
			out = append(out, c)
		}
	}
	return out, nil
}

func buildRecordsInto(root, outDir, manifest string) (map[string]recordSummary, error) {
	rawDir := filepath.Join(root, ".local", "build", "source_records")
	if !dir(rawDir) {
		return nil, fmt.Errorf("missing local source records; prepare source records first")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	edits, err := collectWorkingEdits(root)
	if err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(rawDir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	summary := map[string]recordSummary{}
	for _, rawPath := range paths {
		ds := strings.TrimSuffix(filepath.Base(rawPath), ".jsonl")
		in, err := os.Open(rawPath)
		if err != nil {
			return nil, err
		}
		outPath := filepath.Join(outDir, filepath.Base(rawPath))
		tmp := outPath + ".tmp"
		_ = os.Remove(tmp)
		out, err := os.Create(tmp)
		if err != nil {
			in.Close()
			return nil, err
		}
		found := map[string]bool{}
		changes := []recordChange{}
		total := 0
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			total++
			var obj map[string]any
			if err := json.Unmarshal(line, &obj); err != nil {
				out.Close()
				in.Close()
				return nil, err
			}
			k, _ := obj["record_key"].(string)
			e, ok := edits[k]
			if ok && e.Dataset == ds {
				found[k] = true
				txt, _ := obj["text"].(string)
				if txt != e.Localized {
					out.Close()
					in.Close()
					return nil, fmt.Errorf("LOCALIZED drift for %s: raw=%q working=%q", k, txt, e.Localized)
				}
				if e.EN != e.Localized {
					obj["text"] = e.EN
					encoded, err := pythonStyleJSONLine(obj)
					if err != nil {
						out.Close()
						in.Close()
						return nil, err
					}
					if _, err := out.Write(append(encoded, '\n')); err != nil {
						out.Close()
						in.Close()
						return nil, err
					}
					changes = append(changes, recordChange{RecordKey: k, WorkingID: e.WorkingID, Kind: e.Kind, From: e.Localized, To: e.EN})
					continue
				}
			}
			if _, err := out.Write(append(line, '\n')); err != nil {
				out.Close()
				in.Close()
				return nil, err
			}
		}
		if err := sc.Err(); err != nil {
			out.Close()
			in.Close()
			return nil, err
		}
		in.Close()
		if err := out.Close(); err != nil {
			return nil, err
		}
		expected := 0
		var firstMissing string
		for k, e := range edits {
			if e.Dataset == ds {
				expected++
				if !found[k] && firstMissing == "" {
					firstMissing = k
				}
			}
		}
		if firstMissing != "" {
			_ = os.Remove(tmp)
			return nil, fmt.Errorf("%s: %d working keys absent from raw target; first=%s", ds, expected-len(found), firstMissing)
		}
		if err := os.Rename(tmp, outPath); err != nil {
			return nil, err
		}
		summary[ds] = recordSummary{RawRecords: total, MappedWorkingFields: len(found), ChangedFields: len(changes), Changes: changes}
	}
	tmp := manifest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(summary); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, manifest); err != nil {
		return nil, err
	}
	return summary, nil
}

type sourceHashRow struct {
	Dataset, File string
	Bytes         int64
	SHA256        string
}

func readSourceHashManifest(path string) ([]sourceHashRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.Comma = '\t'
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 1 {
		return nil, fmt.Errorf("empty source hash manifest")
	}
	idx := map[string]int{}
	for i, h := range rows[0] {
		idx[h] = i
	}
	for _, k := range []string{"dataset", "file", "bytes", "sha256"} {
		if _, ok := idx[k]; !ok {
			return nil, fmt.Errorf("source hash manifest missing %s", k)
		}
	}
	out := make([]sourceHashRow, 0, len(rows)-1)
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		n, err := strconv.ParseInt(row[idx["bytes"]], 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, sourceHashRow{row[idx["dataset"]], row[idx["file"]], n, row[idx["sha256"]]})
	}
	return out, nil
}

func CheckChangedSources(root string, c CachedSources, changeManifest string, stdout io.Writer) error {
	raw, err := os.ReadFile(changeManifest)
	if err != nil {
		return err
	}
	var cm map[string]recordSummary
	if err := json.Unmarshal(raw, &cm); err != nil {
		return err
	}
	type key struct{ ds, fn string }
	wanted := map[key]bool{}
	for ds, v := range cm {
		for _, ch := range v.Changes {
			m := archiveKeyRE.FindStringSubmatch(ch.RecordKey)
			if len(m) != 2 {
				continue
			}
			n, _ := strconv.Atoi(m[1])
			nds := ds
			if nds == "MSF_PC_SYSTEM" {
				nds = "MSF_PC_LOCALIZED"
			}
			wanted[key{nds, fmt.Sprintf("GxArchivedFile%03d.dat", n)}] = true
		}
	}
	rows, err := readSourceHashManifest(filepath.Join(root, "toolchain", "manifests", "expected_pc_archive_sha256.tsv"))
	if err != nil {
		return err
	}
	roots := map[string]string{"MS2_PC_LOCALIZED": c.MS2Master, "MSF_PC_LOCALIZED": c.FinaleMaster, "MSF_PC_SYSTEM": c.FinaleMaster}
	checked := 0
	var bad []string
	for _, row := range rows {
		k := key{row.Dataset, row.File}
		if !wanted[k] {
			continue
		}
		base, ok := roots[row.Dataset]
		if !ok {
			continue
		}
		p := filepath.Join(base, row.File)
		st, err := os.Stat(p)
		if err != nil {
			bad = append(bad, "MISSING "+p)
			continue
		}
		if st.Size() != row.Bytes {
			bad = append(bad, fmt.Sprintf("SIZE %s: %d != %d", p, st.Size(), row.Bytes))
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		got := hex.EncodeToString(h.Sum(nil))
		if got != row.SHA256 {
			bad = append(bad, fmt.Sprintf("SHA256 %s: %s != %s", p, got, row.SHA256))
		}
		checked++
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "expected_to_check=%d checked=%d mismatches=%d\n", len(wanted), checked, len(bad))
	}
	if len(bad) > 0 || checked != len(wanted) {
		return fmt.Errorf("source hash gate failed: expected_to_check=%d checked=%d mismatches=%d%s", len(wanted), checked, len(bad), func() string {
			if len(bad) > 0 {
				return "\n" + strings.Join(bad, "\n")
			}
			return ""
		}())
	}
	return nil
}
