package maintainer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"maryskelter-unburger/internal/updater"
)

const sourceCacheVersion = 1

var sourceCacheRecordDatasets = []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"}

type sourceSpecFile struct {
	Game   string
	Kind   string
	Name   string
	Bytes  int64
	SHA256 string
}

type sourceCacheSpec struct {
	Fingerprint  string
	Files        []sourceSpecFile
	TextArchives map[string][]int
}

type sourceCachePristineFile struct {
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	Kind         string `json:"kind"`
	Game         string `json:"game"`
}

type sourceCacheRecordMeta struct {
	Records             int    `json:"records"`
	MappedWorkingFields int    `json:"mapped_working_fields"`
	Bytes               int64  `json:"bytes"`
	SHA256              string `json:"sha256"`
}

type sourceCacheMeta struct {
	CacheVersion       int                              `json:"cache_version"`
	SourceFingerprint  string                           `json:"source_fingerprint"`
	DerivedFingerprint string                           `json:"derived_fingerprint"`
	PristineFiles      []sourceCachePristineFile        `json:"pristine_files"`
	RecordFiles        map[string]sourceCacheRecordMeta `json:"record_files"`
}

func compactJSONNoHTML(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func sha256Path(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func digestProjectPaths(root string, paths []string) (string, error) {
	sort.Slice(paths, func(i, j int) bool {
		ri, _ := filepath.Rel(root, paths[i])
		rj, _ := filepath.Rel(root, paths[j])
		return filepath.ToSlash(ri) < filepath.ToSlash(rj)
	})
	h := sha256.New()
	for _, path := range paths {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		h.Write([]byte(filepath.ToSlash(rel)))
		h.Write([]byte{0})
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func translationSourceIdentity(root string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(root, "translations", "*.jsonl"))
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, path := range paths {
		h.Write([]byte(filepath.Base(path)))
		h.Write([]byte{0})
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			var row map[string]any
			if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
				f.Close()
				return "", err
			}
			source, _ := row["source"].(map[string]any)
			fields, _ := row["fields"].([]any)
			for _, raw := range fields {
				fld, ok := raw.(map[string]any)
				if !ok {
					f.Close()
					return "", fmt.Errorf("%s: invalid translation field", path)
				}
				item := map[string]any{
					"target_dataset":       fld["target_dataset"],
					"localized_record_key": fld["localized_record_key"],
					"LOCALIZED":            fld["LOCALIZED"],
				}
				if _, ok := source["decoded_offset"]; ok {
					item["decoded_locator"] = map[string]any{
						"localized_archive":    source["localized_archive"],
						"localized_gdat_index": source["localized_gdat_index"],
						"row":                  source["row"],
						"decoded_offset":       source["decoded_offset"],
					}
				}
				enc, err := compactJSONNoHTML(item)
				if err != nil {
					f.Close()
					return "", err
				}
				h.Write(enc)
				h.Write([]byte{'\n'})
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func derivedSourceFingerprint(root string) (string, error) {
	// Source-record cache identity is intentionally scoped to code that can
	// change extracted JSONL bytes. Graphics/QoL/container candidates outside
	// this list must not invalidate text records merely because they share the
	// internal/mskit package.
	paths := []string{
		filepath.Join(root, "internal", "maintainer", "sourceextract.go"),
		filepath.Join(root, "internal", "maintainer", "decodedenc.go"),
		filepath.Join(root, "toolchain", "manifests", "decoded_enc_slots.tsv"),
	}
	for _, name := range []string{
		"bilz.go", "bin.go", "cl3.go", "enc.go", "gbnl.go", "gdat.go",
		"gdat_file.go", "lzo1x.go", "stcm_data.go",
	} {
		paths = append(paths, filepath.Join(root, "internal", "mskit", name))
	}
	codeDigest, err := digestProjectPaths(root, paths)
	if err != nil {
		return "", err
	}
	translationDigest, err := translationSourceIdentity(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "cache-derived-v2\n%s\n%s", codeDigest, translationDigest)
	return hex.EncodeToString(h.Sum(nil)), nil
}

func requiredSourceArchives(root string) (map[string]map[int]bool, map[string]map[int]bool, error) {
	all := map[string]map[int]bool{"MS2": {}, "MSF": {}}
	text := map[string]map[int]bool{}
	for _, ds := range sourceCacheRecordDatasets {
		text[ds] = map[int]bool{}
	}
	paths, err := filepath.Glob(filepath.Join(root, "translations", "*.jsonl"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(paths)
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			var row struct {
				Fields []translationField `json:"fields"`
			}
			if err := json.Unmarshal(sc.Bytes(), &row); err != nil {
				f.Close()
				return nil, nil, err
			}
			for _, fld := range row.Fields {
				if _, ok := text[fld.TargetDataset]; !ok {
					continue
				}
				m := archiveKeyRE.FindStringSubmatch(fld.LocalizedRecordKey)
				if len(m) != 2 {
					f.Close()
					return nil, nil, fmt.Errorf("cannot recover archive from %q", fld.LocalizedRecordKey)
				}
				var archive int
				if _, err := fmt.Sscanf(m[1], "%d", &archive); err != nil {
					f.Close()
					return nil, nil, err
				}
				text[fld.TargetDataset][archive] = true
				game := "MSF"
				if strings.HasPrefix(fld.TargetDataset, "MS2_") {
					game = "MS2"
				}
				all[game][archive] = true
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, nil, err
		}
	}
	var gm struct {
		Assets []struct {
			Enabled bool   `json:"enabled"`
			Dataset string `json:"dataset"`
			Archive int    `json:"archive"`
		} `json:"assets"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(raw, &gm); err != nil {
		return nil, nil, err
	}
	for _, a := range gm.Assets {
		if !a.Enabled {
			continue
		}
		game := "MSF"
		if strings.HasPrefix(a.Dataset, "MS2_") {
			game = "MS2"
		}
		all[game][a.Archive] = true
	}
	return all, text, nil
}

func sourceCacheSpecForRoot(root string) (sourceCacheSpec, error) {
	all, text, err := requiredSourceArchives(root)
	if err != nil {
		return sourceCacheSpec{}, err
	}
	manifestRows, err := readSourceHashManifest(filepath.Join(root, "toolchain", "manifests", "expected_pc_archive_sha256.tsv"))
	if err != nil {
		return sourceCacheSpec{}, err
	}
	type manifestItem struct {
		bytes int64
		sha   string
	}
	expected := map[string]manifestItem{}
	for _, row := range manifestRows {
		game := "MSF"
		if strings.HasPrefix(row.Dataset, "MS2_") {
			game = "MS2"
		}
		key := game + "\x00" + row.File
		item := manifestItem{row.Bytes, row.SHA256}
		if prior, ok := expected[key]; ok && prior != item {
			return sourceCacheSpec{}, fmt.Errorf("conflicting source manifest entries for %s %s", game, row.File)
		}
		expected[key] = item
	}
	var files []sourceSpecFile
	for _, game := range []string{"MS2", "MSF"} {
		var archives []int
		for a := range all[game] {
			archives = append(archives, a)
		}
		sort.Ints(archives)
		for _, archive := range archives {
			name := fmt.Sprintf("GxArchivedFile%03d.dat", archive)
			item, ok := expected[game+"\x00"+name]
			if !ok {
				return sourceCacheSpec{}, fmt.Errorf("missing public pristine hash for %s %s", game, name)
			}
			files = append(files, sourceSpecFile{Game: game, Kind: "archive", Name: name, Bytes: item.bytes, SHA256: item.sha})
		}
	}
	files = append(files,
		sourceSpecFile{Game: "MS2", Kind: "exe", Name: updater.Profiles["ms2"].ExeName, SHA256: updater.Profiles["ms2"].OriginalSHA},
		sourceSpecFile{Game: "MSF", Kind: "exe_optional", Name: updater.Profiles["finale"].ExeName, SHA256: updater.Profiles["finale"].OriginalSHA},
	)
	textArchives := map[string][]int{}
	for _, ds := range sourceCacheRecordDatasets {
		for a := range text[ds] {
			textArchives[ds] = append(textArchives[ds], a)
		}
		sort.Ints(textArchives[ds])
	}
	canonicalFiles := make([]any, 0, len(files))
	for _, f := range files {
		m := map[string]any{"game": f.Game, "kind": f.Kind, "name": f.Name, "sha256": f.SHA256}
		if f.Kind == "archive" {
			m["bytes"] = f.Bytes
		}
		canonicalFiles = append(canonicalFiles, m)
	}
	canonical := map[string]any{"cache_version": sourceCacheVersion, "files": canonicalFiles, "text_archives": textArchives}
	enc, err := compactJSONNoHTML(canonical)
	if err != nil {
		return sourceCacheSpec{}, err
	}
	h := sha256.Sum256(enc)
	return sourceCacheSpec{Fingerprint: hex.EncodeToString(h[:]), Files: files, TextArchives: textArchives}, nil
}

func cacheMetaPath(cacheRoot string) string { return filepath.Join(cacheRoot, "cache.json") }

func loadSourceCacheMeta(cacheRoot string) (sourceCacheMeta, error) {
	var meta sourceCacheMeta
	raw, err := os.ReadFile(cacheMetaPath(cacheRoot))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func quickSourceCacheValid(cacheRoot string, spec sourceCacheSpec) (bool, sourceCacheMeta, string) {
	meta, err := loadSourceCacheMeta(cacheRoot)
	if err != nil {
		return false, meta, "cache metadata missing/unreadable"
	}
	if meta.CacheVersion != sourceCacheVersion {
		return false, meta, "cache format version changed"
	}
	if meta.SourceFingerprint != spec.Fingerprint {
		return false, meta, "required pristine source set changed"
	}
	listed := map[string]sourceCachePristineFile{}
	for _, item := range meta.PristineFiles {
		listed[filepath.ToSlash(item.RelativePath)] = item
	}
	required := map[string]bool{}
	for _, item := range spec.Files {
		if item.Kind == "exe_optional" {
			continue
		}
		base := "finale"
		if item.Game == "MS2" {
			base = "ms2"
		}
		var rel string
		if item.Kind == "archive" {
			rel = filepath.ToSlash(filepath.Join("pristine", base, "masterWindows", item.Name))
		} else {
			rel = filepath.ToSlash(filepath.Join("pristine", base, item.Name))
		}
		required[rel] = true
	}
	for rel := range required {
		if _, ok := listed[rel]; !ok {
			return false, meta, "cache metadata missing required pristine file: " + rel
		}
	}
	for rel, item := range listed {
		p := filepath.Join(cacheRoot, filepath.FromSlash(rel))
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() {
			return false, meta, "missing cached pristine file: " + rel
		}
		if item.Bytes != 0 && st.Size() != item.Bytes {
			return false, meta, "cached pristine size drift: " + rel
		}
	}
	return true, meta, "cache source set ready"
}

func sourceItemPath(cfg SourcePaths, item sourceSpecFile) string {
	if item.Kind == "archive" {
		master := cfg.FinaleMaster
		if item.Game == "MS2" {
			master = cfg.MS2Master
		}
		return filepath.Join(master, item.Name)
	}
	if item.Game == "MS2" {
		return cfg.MS2Exe
	}
	return cfg.FinaleExe
}

func cachedRelativePath(item sourceSpecFile) string {
	base := "finale"
	if item.Game == "MS2" {
		base = "ms2"
	}
	if item.Kind == "archive" {
		return filepath.ToSlash(filepath.Join("pristine", base, "masterWindows", item.Name))
	}
	return filepath.ToSlash(filepath.Join("pristine", base, item.Name))
}

func seedPristineCache(tempRoot string, spec sourceCacheSpec, cfg SourcePaths, existingRoot string, existingMeta sourceCacheMeta, stdout io.Writer) ([]sourceCachePristineFile, error) {
	prior := map[string]sourceCachePristineFile{}
	for _, item := range existingMeta.PristineFiles {
		prior[filepath.ToSlash(item.RelativePath)] = item
	}
	out := make([]sourceCachePristineFile, 0, len(spec.Files))
	for _, item := range spec.Files {
		rel := cachedRelativePath(item)
		if p, ok := prior[rel]; ok && existingRoot != "" {
			old := filepath.Join(existingRoot, filepath.FromSlash(rel))
			st, err := os.Stat(old)
			if err == nil && st.Mode().IsRegular() && p.SHA256 == item.SHA256 && (item.Bytes == 0 || st.Size() == item.Bytes) {
				if stdout != nil {
					fmt.Fprintf(stdout, "SOURCE CACHE: reusing verified %s %s\n", item.Game, item.Name)
				}
				dst := filepath.Join(tempRoot, filepath.FromSlash(rel))
				if err := copyFile(old, dst); err != nil {
					return nil, err
				}
				st, _ = os.Stat(dst)
				out = append(out, sourceCachePristineFile{rel, item.SHA256, st.Size(), item.Kind, item.Game})
				continue
			}
		}
		src := sourceItemPath(cfg, item)
		if item.Kind == "exe_optional" && (src == "" || !file(src)) {
			continue
		}
		if src == "" || !file(src) {
			return nil, fmt.Errorf("missing pristine source input needed to seed/extend cache: %s", item.Name)
		}
		st, err := os.Stat(src)
		if err != nil {
			return nil, err
		}
		if item.Bytes != 0 && st.Size() != item.Bytes {
			return nil, fmt.Errorf("%s: source size does not match supported pristine release; got=%d expected=%d", src, st.Size(), item.Bytes)
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "SOURCE CACHE: caching %s %s\n", item.Game, item.Name)
		}
		dst := filepath.Join(tempRoot, filepath.FromSlash(rel))
		if err := copyFile(src, dst); err != nil {
			return nil, err
		}
		got, err := sha256Path(dst)
		if err != nil {
			return nil, err
		}
		if got != item.SHA256 {
			return nil, fmt.Errorf("%s: source SHA-256 does not match supported pristine release; got=%s expected=%s", src, got, item.SHA256)
		}
		st, _ = os.Stat(dst)
		out = append(out, sourceCachePristineFile{rel, got, st.Size(), item.Kind, item.Game})
	}
	return out, nil
}

func validateCachedWorkingTargets(root string, recordsDir string) (map[string]sourceCacheRecordMeta, error) {
	edits, err := collectWorkingEdits(root)
	if err != nil {
		return nil, err
	}
	wanted := map[string]map[string]string{}
	for _, ds := range sourceCacheRecordDatasets {
		wanted[ds] = map[string]string{}
	}
	for k, e := range edits {
		if _, ok := wanted[e.Dataset]; ok {
			wanted[e.Dataset][k] = e.Localized
		}
	}
	var canon struct {
		Mapped map[string]int `json:"expected_mapped_fields_by_dataset"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "toolchain", "manifests", "canonical_counts.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &canon); err != nil {
		return nil, err
	}
	out := map[string]sourceCacheRecordMeta{}
	for _, ds := range sourceCacheRecordDatasets {
		path := filepath.Join(recordsDir, ds+".jsonl")
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		found := map[string]bool{}
		count := 0
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			if len(bytes.TrimSpace(sc.Bytes())) == 0 {
				continue
			}
			count++
			var obj struct {
				RecordKey string `json:"record_key"`
				Text      string `json:"text"`
			}
			if err := json.Unmarshal(sc.Bytes(), &obj); err != nil {
				f.Close()
				return nil, err
			}
			if expected, ok := wanted[ds][obj.RecordKey]; ok {
				if obj.Text != expected {
					f.Close()
					return nil, fmt.Errorf("%s: cached LOCALIZED drift for %s: raw=%q working=%q", ds, obj.RecordKey, obj.Text, expected)
				}
				found[obj.RecordKey] = true
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(found) != len(wanted[ds]) {
			var first string
			for k := range wanted[ds] {
				if !found[k] {
					first = k
					break
				}
			}
			return nil, fmt.Errorf("%s: source cache missing %d working targets; first=%s", ds, len(wanted[ds])-len(found), first)
		}
		if want, ok := canon.Mapped[ds]; ok && len(found) != want {
			return nil, fmt.Errorf("%s: cached mapped field count %d != canonical %d", ds, len(found), want)
		}
		st, _ := os.Stat(path)
		h, err := sha256Path(path)
		if err != nil {
			return nil, err
		}
		out[ds] = sourceCacheRecordMeta{count, len(found), st.Size(), h}
	}
	return out, nil
}

func buildSourceRecordsCache(root, cacheRoot string, spec sourceCacheSpec, stdout, stderr io.Writer) (map[string]sourceCacheRecordMeta, error) {
	c := cachedSourcesAt(cacheRoot)
	recordsDir := c.Records
	tmp := recordsDir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return nil, err
	}
	masters := map[string]string{"MS2_PC_LOCALIZED": c.MS2Master, "MSF_PC_LOCALIZED": c.FinaleMaster, "MSF_PC_SYSTEM": c.FinaleMaster}
	for _, ds := range sourceCacheRecordDatasets {
		if _, err := ExtractSourceDatasetGo(ds, masters[ds], spec.TextArchives[ds], filepath.Join(tmp, ds+".jsonl"), stdout); err != nil {
			_ = os.RemoveAll(tmp)
			return nil, err
		}
	}
	if err := MaterializeDecodedENCTargets(root, tmp, c.MS2Master, c.FinaleMaster, stdout); err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	meta, err := validateCachedWorkingTargets(root, tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return nil, err
	}
	if stdout != nil {
		for _, ds := range sourceCacheRecordDatasets {
			m := meta[ds]
			fmt.Fprintf(stdout, "SOURCE CACHE: %s records=%d mapped=%d\n", ds, m.Records, m.MappedWorkingFields)
		}
	}
	_ = os.RemoveAll(recordsDir)
	if err := os.Rename(tmp, recordsDir); err != nil {
		return nil, err
	}
	return meta, nil
}

func cachedSourcesAt(base string) CachedSources {
	return CachedSources{
		Root:         base,
		MS2Master:    filepath.Join(base, "pristine", "ms2", "masterWindows"),
		FinaleMaster: filepath.Join(base, "pristine", "finale", "masterWindows"),
		MS2Exe:       filepath.Join(base, "pristine", "ms2", updater.Profiles["ms2"].ExeName),
		FinaleExe:    filepath.Join(base, "pristine", "finale", updater.Profiles["finale"].ExeName),
		Records:      filepath.Join(base, "records"),
	}
}

const (
	legacyEquivalentDerivedFingerprintV1 = "0c055bc276139d0efc8a91d1d196da245107de081a90cb59b8680ebc26542a0e"
	nativeGoExtractorDerivedFingerprint  = "c57e6d2711ac2a36f2346abe82814ed7c56d72b04b23761c75fd1502fa5d4a9a"
	graphicsIncidentalDerivedFingerprint = "fc2d078df337db8d3f35d9d90898593814a3ea9e85d4fd8037c2ab671cee23cb"
	previousScopedDerivedFingerprint     = "9e5d1768566a20a2bd6d8750916f3a36144ba72ece60a40b4dc6d2c3f816f969"
	scopedExtractionDerivedFingerprint   = "0b708a4b3ebe9f1006d8770ee897e6e2797d2e4c35a2e7f6eb6232ffd31fd2b7"
)

func compatibleDerivedFingerprint(existing, current string) bool {
	if existing == current {
		return true
	}
	if current != scopedExtractionDerivedFingerprint {
		return false
	}
	switch existing {
	case legacyEquivalentDerivedFingerprintV1, nativeGoExtractorDerivedFingerprint, graphicsIncidentalDerivedFingerprint, previousScopedDerivedFingerprint:
		return true
	default:
		return false
	}
}

func reuseDerivedRecords(existingRoot string, existingMeta sourceCacheMeta, tempRoot, currentDerived string, stdout io.Writer) (map[string]sourceCacheRecordMeta, bool, error) {
	if !compatibleDerivedFingerprint(existingMeta.DerivedFingerprint, currentDerived) {
		return nil, false, nil
	}
	if existingMeta.DerivedFingerprint != currentDerived && stdout != nil {
		fmt.Fprintln(stdout, "SOURCE CACHE: reusing byte-compatible derived records under the scoped extraction fingerprint")
	}
	old := cachedSourcesAt(existingRoot).Records
	for _, ds := range sourceCacheRecordDatasets {
		if !file(filepath.Join(old, ds+".jsonl")) {
			return nil, false, nil
		}
		if _, ok := existingMeta.RecordFiles[ds]; !ok {
			return nil, false, nil
		}
	}
	newDir := cachedSourcesAt(tempRoot).Records
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		return nil, false, err
	}
	out := map[string]sourceCacheRecordMeta{}
	for _, ds := range sourceCacheRecordDatasets {
		src := filepath.Join(old, ds+".jsonl")
		dst := filepath.Join(newDir, ds+".jsonl")
		if err := copyFile(src, dst); err != nil {
			return nil, false, err
		}
		m := existingMeta.RecordFiles[ds]
		got, err := sha256Path(dst)
		if err != nil {
			return nil, false, err
		}
		if m.SHA256 != "" && got != m.SHA256 {
			return nil, false, fmt.Errorf("%s: existing derived source-record cache SHA drift", ds)
		}
		st, _ := os.Stat(dst)
		m.SHA256, m.Bytes = got, st.Size()
		out[ds] = m
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "SOURCE CACHE: reused derived records; only pristine graphics inputs changed")
	}
	return out, true, nil
}

func writeSourceCacheMeta(path string, meta sourceCacheMeta) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(meta); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func ensureSourceCacheNative(root string, cfg SourcePaths, stdout, stderr io.Writer) (CachedSources, error) {
	cacheRoot := filepath.Join(root, ".local", "source_cache")
	spec, err := sourceCacheSpecForRoot(root)
	if err != nil {
		return CachedSources{}, err
	}
	sourceOK, meta, reason := quickSourceCacheValid(cacheRoot, spec)
	if sourceOK {
		cp := cachedSourcesAt(cacheRoot)
		if !file(cp.FinaleExe) && cfg.FinaleExe != "" && file(cfg.FinaleExe) {
			got, err := sha256Path(cfg.FinaleExe)
			if err != nil {
				return CachedSources{}, err
			}
			want := updater.Profiles["finale"].OriginalSHA
			if got != want {
				return CachedSources{}, fmt.Errorf("%s: Finale EXE SHA-256 does not match supported pristine release; got=%s expected=%s", cfg.FinaleExe, got, want)
			}
			if err := copyFile(cfg.FinaleExe, cp.FinaleExe); err != nil {
				return CachedSources{}, err
			}
			st, _ := os.Stat(cp.FinaleExe)
			rel := filepath.ToSlash(filepath.Join("pristine", "finale", updater.Profiles["finale"].ExeName))
			meta.PristineFiles = append(meta.PristineFiles, sourceCachePristineFile{rel, got, st.Size(), "exe_optional", "MSF"})
			if err := writeSourceCacheMeta(cacheMetaPath(cacheRoot), meta); err != nil {
				return CachedSources{}, err
			}
			if stdout != nil {
				fmt.Fprintln(stdout, "SOURCE CACHE: added configured pristine Finale EXE")
			}
		}
		currentDerived, err := derivedSourceFingerprint(root)
		if err != nil {
			return CachedSources{}, err
		}
		recordsOK := compatibleDerivedFingerprint(meta.DerivedFingerprint, currentDerived)
		for _, ds := range sourceCacheRecordDatasets {
			recordsOK = recordsOK && file(filepath.Join(cp.Records, ds+".jsonl"))
		}
		if recordsOK {
			if meta.DerivedFingerprint != currentDerived {
				meta.DerivedFingerprint = currentDerived
				if err := writeSourceCacheMeta(cacheMetaPath(cacheRoot), meta); err != nil {
					return CachedSources{}, err
				}
				if stdout != nil {
					fmt.Fprintln(stdout, "SOURCE CACHE: normalized byte-compatible derived fingerprint without rebuilding records")
				}
			}
			if stdout != nil {
				fmt.Fprintf(stdout, "SOURCE CACHE: HIT (%s)\n", cacheRoot)
			}
			return cp, nil
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "SOURCE CACHE: pristine HIT; regenerating derived records (%s)\n", reason)
		}
		recordMeta, err := buildSourceRecordsCache(root, cacheRoot, spec, stdout, stderr)
		if err != nil {
			return CachedSources{}, err
		}
		meta.DerivedFingerprint = currentDerived
		meta.RecordFiles = recordMeta
		if err := writeSourceCacheMeta(cacheMetaPath(cacheRoot), meta); err != nil {
			return CachedSources{}, err
		}
		if stdout != nil {
			fmt.Fprintln(stdout, "SOURCE CACHE: derived records refreshed from cached pristine inputs")
		}
		return cp, nil
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "SOURCE CACHE: MISS (%s); seeding pristine inputs once\n", reason)
	}
	parent := filepath.Dir(cacheRoot)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return CachedSources{}, err
	}
	tmpRoot, err := os.MkdirTemp(parent, filepath.Base(cacheRoot)+".tmp.")
	if err != nil {
		return CachedSources{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmpRoot)
		}
	}()
	existingRoot := ""
	if dir(cacheRoot) {
		existingRoot = cacheRoot
	}
	pristine, err := seedPristineCache(tmpRoot, spec, cfg, existingRoot, meta, stdout)
	if err != nil {
		return CachedSources{}, err
	}
	currentDerived, err := derivedSourceFingerprint(root)
	if err != nil {
		return CachedSources{}, err
	}
	recordMeta, reused, err := reuseDerivedRecords(existingRoot, meta, tmpRoot, currentDerived, stdout)
	if err != nil {
		return CachedSources{}, err
	}
	if !reused {
		recordMeta, err = buildSourceRecordsCache(root, tmpRoot, spec, stdout, stderr)
		if err != nil {
			return CachedSources{}, err
		}
	}
	newMeta := sourceCacheMeta{sourceCacheVersion, spec.Fingerprint, currentDerived, pristine, recordMeta}
	if err := writeSourceCacheMeta(cacheMetaPath(tmpRoot), newMeta); err != nil {
		return CachedSources{}, err
	}
	old := cacheRoot + ".previous"
	_ = os.RemoveAll(old)
	hadOld := dir(cacheRoot)
	if hadOld {
		if err := os.Rename(cacheRoot, old); err != nil {
			return CachedSources{}, err
		}
	}
	if err := os.Rename(tmpRoot, cacheRoot); err != nil {
		if hadOld {
			_ = os.Rename(old, cacheRoot)
		}
		return CachedSources{}, err
	}
	committed = true
	_ = os.RemoveAll(old)
	if stdout != nil {
		fmt.Fprintf(stdout, "SOURCE CACHE: READY (%s)\n", cacheRoot)
	}
	return cachedSourcesAt(cacheRoot), nil
}
