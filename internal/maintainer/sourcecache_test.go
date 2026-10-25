package maintainer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func TestQuickSourceCacheValid(t *testing.T) {
	root := t.TempDir()
	spec := sourceCacheSpec{Fingerprint: "source-fp", Files: []sourceSpecFile{
		{Game: "MS2", Kind: "archive", Name: "GxArchivedFile055.dat", Bytes: 4, SHA256: strings.Repeat("a", 64)},
		{Game: "MS2", Kind: "exe", Name: "MarySkelter2.exe", SHA256: strings.Repeat("b", 64)},
		{Game: "MSF", Kind: "exe_optional", Name: "MarySkelterFinale.exe", SHA256: strings.Repeat("c", 64)},
	}}
	files := []sourceCachePristineFile{
		{"pristine/ms2/masterWindows/GxArchivedFile055.dat", strings.Repeat("a", 64), 4, "archive", "MS2"},
		{"pristine/ms2/MarySkelter2.exe", strings.Repeat("b", 64), 3, "exe", "MS2"},
	}
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f.RelativePath))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.Repeat([]byte{'x'}, int(f.Bytes)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta := sourceCacheMeta{CacheVersion: sourceCacheVersion, SourceFingerprint: spec.Fingerprint, PristineFiles: files}
	if err := writeSourceCacheMeta(cacheMetaPath(root), meta); err != nil {
		t.Fatal(err)
	}
	ok, _, reason := quickSourceCacheValid(root, spec)
	if !ok {
		t.Fatalf("valid cache rejected: %s", reason)
	}
	if err := os.Truncate(filepath.Join(root, "pristine/ms2/masterWindows/GxArchivedFile055.dat"), 3); err != nil {
		t.Fatal(err)
	}
	ok, _, reason = quickSourceCacheValid(root, spec)
	if ok || !strings.Contains(reason, "size drift") {
		t.Fatalf("size drift not rejected: ok=%v reason=%q", ok, reason)
	}
}

func TestSourceCacheMetaJSONFieldOrder(t *testing.T) {
	root := t.TempDir()
	meta := sourceCacheMeta{
		CacheVersion: 1, SourceFingerprint: "source", DerivedFingerprint: "derived",
		PristineFiles: []sourceCachePristineFile{{"pristine/ms2/a.dat", "abc", 3, "archive", "MS2"}},
		RecordFiles:   map[string]sourceCacheRecordMeta{"MS2_PC_LOCALIZED": {1, 1, 9, "def"}},
	}
	p := filepath.Join(root, "cache.json")
	if err := writeSourceCacheMeta(p, meta); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"cache_version\": 1,\n  \"source_fingerprint\": \"source\",\n  \"derived_fingerprint\": \"derived\",\n  \"pristine_files\": [\n    {\n      \"relative_path\": \"pristine/ms2/a.dat\",\n      \"sha256\": \"abc\",\n      \"bytes\": 3,\n      \"kind\": \"archive\",\n      \"game\": \"MS2\"\n    }\n  ],\n  \"record_files\": {\n    \"MS2_PC_LOCALIZED\": {\n      \"records\": 1,\n      \"mapped_working_fields\": 1,\n      \"bytes\": 9,\n      \"sha256\": \"def\"\n    }\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("cache metadata layout drift:\n%s", got)
	}
}

func TestDerivedRecordReuseInvalidatesPreNativeExtractorFingerprint(t *testing.T) {
	oldFingerprint := "4c8dc6e200feeb62beca92ff8e07a99f71e9b151d2000c7c480b3bfab9abfab7"
	currentFingerprint, err := derivedSourceFingerprint("../..")
	if err != nil {
		t.Fatal(err)
	}
	if currentFingerprint == oldFingerprint {
		t.Fatal("active native-Go extractor did not change the derived source-cache fingerprint")
	}

	existing := t.TempDir()
	records := cachedSourcesAt(existing).Records
	if err := os.MkdirAll(records, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := sourceCacheMeta{DerivedFingerprint: oldFingerprint, RecordFiles: map[string]sourceCacheRecordMeta{}}
	for _, ds := range sourceCacheRecordDatasets {
		p := filepath.Join(records, ds+".jsonl")
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		meta.RecordFiles[ds] = sourceCacheRecordMeta{Records: 1, Bytes: 3}
	}
	_, reused, err := reuseDerivedRecords(existing, meta, t.TempDir(), currentFingerprint, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reused {
		t.Fatal("pre-native-extractor derived records must be rebuilt once after the implementation switch")
	}
}

func TestScopedDerivedFingerprintCompatibility(t *testing.T) {
	current, err := derivedSourceFingerprint("../..")
	if err != nil {
		t.Fatal(err)
	}
	if current != scopedExtractionDerivedFingerprint {
		t.Fatalf("scoped derived fingerprint drift: got %s want %s", current, scopedExtractionDerivedFingerprint)
	}
	for _, fp := range []string{legacyEquivalentDerivedFingerprintV1, nativeGoExtractorDerivedFingerprint, graphicsIncidentalDerivedFingerprint, previousScopedDerivedFingerprint, current} {
		if !compatibleDerivedFingerprint(fp, current) {
			t.Fatalf("expected byte-compatible fingerprint %s", fp)
		}
	}
	if compatibleDerivedFingerprint("4c8dc6e200feeb62beca92ff8e07a99f71e9b151d2000c7c480b3bfab9abfab7", current) {
		t.Fatal("pre-native extractor fingerprint must remain incompatible")
	}
	if compatibleDerivedFingerprint(nativeGoExtractorDerivedFingerprint, strings.Repeat("f", 64)) {
		t.Fatal("legacy compatibility must not leak into future extraction fingerprints")
	}
}

func TestBuildSourceRecordsCacheUsesNativeGoExtractor(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, ".local", "source_cache")
	if err := os.MkdirAll(filepath.Join(root, "translations"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifestDir := filepath.Join(root, "toolchain", "manifests")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "decoded_enc_slots.tsv"), []byte("dataset\tarchive\tgdat_index\trow\tdecoded_offset\tslot_capacity\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	counts := `{"expected_mapped_fields_by_dataset":{"MS2_PC_LOCALIZED":0,"MSF_PC_LOCALIZED":0,"MSF_PC_SYSTEM":0}}`
	if err := os.WriteFile(filepath.Join(manifestDir, "canonical_counts.json"), []byte(counts), 0o644); err != nil {
		t.Fatal(err)
	}
	gdat, err := mskit.BuildGDAT(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, game := range []string{"ms2", "finale"} {
		master := filepath.Join(cacheRoot, "pristine", game, "masterWindows")
		if err := os.MkdirAll(master, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(master, "GxArchivedFile052.dat"), gdat, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spec := sourceCacheSpec{TextArchives: map[string][]int{
		"MS2_PC_LOCALIZED": {52},
		"MSF_PC_LOCALIZED": {52},
		"MSF_PC_SYSTEM":    {52},
	}}
	meta, err := buildSourceRecordsCache(root, cacheRoot, spec, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ds := range sourceCacheRecordDatasets {
		if meta[ds].Records != 0 || meta[ds].MappedWorkingFields != 0 {
			t.Fatalf("%s: unexpected synthetic native-Go record metadata: %+v", ds, meta[ds])
		}
		if _, err := os.Stat(filepath.Join(cacheRoot, "records", ds+".jsonl")); err != nil {
			t.Fatalf("%s: missing native-Go cache output: %v", ds, err)
		}
	}
}
