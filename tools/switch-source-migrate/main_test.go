package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func put(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (string, string, string, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	sources := map[string]string{"ms2": filepath.Join(t.TempDir(), "ms2"), "finale": filepath.Join(t.TempDir(), "finale")}
	h := map[string][]byte{}
	mp := map[string]any{"games": map[string]any{}}
	manifest := map[string]any{"games": map[string]any{}}
	for _, game := range []string{"ms2", "finale"} {
		graphics := map[string]any{}
		idx := bytes.NewBuffer(nil)
		for _, n := range nums[game] {
			name := fmt.Sprintf("GxArchivedFile%03d.dat", n)
			b := []byte("GDATtest fixture " + game + "/" + name)
			put(t, filepath.Join(sources[game], name), b)
			h[game+"/"+name] = b
			ds := "MS2_SWITCH_WESTERN"
			if game == "finale" {
				ds = "MSF_SWITCH_WESTERN"
			}
			row, _ := json.Marshal(map[string]any{"archive_number": n, "source_file_sha256": sha(b), "source_dataset": ds})
			idx.Write(row)
			idx.WriteByte('\n')
		}
		graphIDs := []int{74}
		if game == "finale" {
			graphIDs = []int{74, 75}
		}
		for _, n := range graphIDs {
			name := fmt.Sprintf("GxArchivedFile%03d.dat", n)
			b := []byte("GDATgraphic source " + game + "/" + name)
			put(t, filepath.Join(sources[game], name), b)
			h[game+"/"+name] = b
			graphics[name] = map[string]any{"sha256": sha(b)}
		}
		put(t, filepath.Join(root, ".local", "switch_text_port", game, "western-native-records.jsonl"), idx.Bytes())
		mp["games"].(map[string]any)[game] = map[string]any{"archives": graphics}
		manifest["games"].(map[string]any)[game] = map[string]any{"native_records_sha256": sha(idx.Bytes())}
	}
	b, _ := json.Marshal(mp)
	put(t, filepath.Join(root, ".local", "test-source-mapping", "source-map.json"), b)
	index := fmt.Sprintf(`{"schema":1,"entries":{"switch-graphics-source-map":{"kind":"file","path":".local/test-source-mapping/source-map.json","sha256":"%s"}}}`, sha(b))
	put(t, filepath.Join(root, ".local", "internal", "private_asset_locations.json"), []byte(index))
	b, _ = json.Marshal(manifest)
	put(t, filepath.Join(root, "toolchain", "switch_port", "maps", "manifest.json"), b)
	return root, sources["ms2"], sources["finale"], h
}
func TestMigratesElevenPinnedSourcesIdempotently(t *testing.T) {
	root, ms2, finale, source := fixture(t)
	out := &bytes.Buffer{}
	if e := migrate(root, ms2, finale, out); e != nil {
		t.Fatal(e)
	}
	if got := strings.Count(out.String(), "COPY "); got != 11 {
		t.Fatalf("got %d copies: %s", got, out.String())
	}
	for key, b := range source {
		parts := strings.SplitN(key, "/", 2)
		dest := filepath.Join(root, ".local", "switch_sources", parts[0], "latest_update", "masterNX64", parts[1])
		got, e := os.ReadFile(dest)
		if e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(got, b) {
			t.Fatalf("mismatch %s", key)
		}
	}
	out.Reset()
	if e := migrate(root, ms2, finale, out); e != nil {
		t.Fatal(e)
	}
	if got := strings.Count(out.String(), "KEEP "); got != 11 {
		t.Fatalf("rerun was not idempotent: %s", out.String())
	}
}
func TestRejectsAnyBadInputBeforeCopy(t *testing.T) {
	root, ms2, finale, _ := fixture(t)
	bad := filepath.Join(ms2, "GxArchivedFile073.dat")
	put(t, bad, []byte("GDATtampered"))
	if e := migrate(root, ms2, finale, &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "SHA mismatch") {
		t.Fatalf("unexpected error: %v", e)
	}
	if _, e := os.Stat(filepath.Join(root, ".local", "switch_sources")); !os.IsNotExist(e) {
		t.Fatalf("wrote any targets despite preflight failure: %v", e)
	}
}
func TestRejectsExistingWrongCacheAndSymlinks(t *testing.T) {
	root, ms2, finale, _ := fixture(t)
	bad := filepath.Join(root, ".local", "switch_sources", "ms2", "latest_update", "masterNX64", "GxArchivedFile073.dat")
	put(t, bad, []byte("GDATwrong"))
	if e := migrate(root, ms2, finale, &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "refusing overwrite") {
		t.Fatalf("existing wrong cache overwritten? %v", e)
	}
	os.RemoveAll(filepath.Join(root, ".local", "switch_sources"))
	link := filepath.Join(root, ".local", "switch_sources")
	if e := os.Symlink(t.TempDir(), link); e != nil {
		t.Fatal(e)
	}
	if e := migrate(root, ms2, finale, &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "symlink") {
		t.Fatalf("symlink was not rejected: %v", e)
	}
}
func TestRejectsNativeIndexChecksumMismatch(t *testing.T) {
	root, ms2, finale, _ := fixture(t)
	idx := filepath.Join(root, ".local", "switch_text_port", "ms2", "western-native-records.jsonl")
	f, e := os.OpenFile(idx, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteString("\n")
	f.Close()
	if e := migrate(root, ms2, finale, &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "index SHA mismatch") {
		t.Fatalf("missing index hash guard: %v", e)
	}
}

// Genuine Western indices can contain other archive numbers such as 052.
// Their presence must not change the 11-member active source contract.
func TestIgnoresInactiveArchivesInFullyPinnedNativeIndex(t *testing.T) {
	root, ms2, finale, _ := fixture(t)
	for _, game := range []string{"ms2", "finale"} {
		p := filepath.Join(root, ".local", "switch_text_port", game, "western-native-records.jsonl")
		original, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		other := 52
		if game == "finale" {
			other = 88
		}
		row, _ := json.Marshal(map[string]any{"archive_number": other, "source_file_sha256": "unused", "source_dataset": "SOURCE_NOT_USED"})
		newIndex := append(append(row, '\n'), original...)
		put(t, p, newIndex)
		manifestPath := filepath.Join(root, "toolchain", "switch_port", "maps", "manifest.json")
		var m map[string]any
		raw, e := os.ReadFile(manifestPath)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(raw, &m); e != nil {
			t.Fatal(e)
		}
		m["games"].(map[string]any)[game].(map[string]any)["native_records_sha256"] = sha(newIndex)
		raw, _ = json.Marshal(m)
		put(t, manifestPath, raw)
	}
	if e := migrate(root, ms2, finale, &bytes.Buffer{}); e != nil {
		t.Fatal(e)
	}
	// Even extra archives are guarded by the whole-index SHA, not silently
	// ignored when their content has changed after approval.
	p := filepath.Join(root, ".local", "switch_text_port", "ms2", "western-native-records.jsonl")
	f, e := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.WriteString(" ")
	_ = f.Close()
	if e := migrate(root, ms2, finale, &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "index SHA mismatch") {
		t.Fatalf("index drift accepted: %v", e)
	}
}
func TestRejectsInvalidActiveRecordWithExtraArchivesPresent(t *testing.T) {
	root, ms2, finale, _ := fixture(t)
	p := filepath.Join(root, ".local", "switch_text_port", "ms2", "western-native-records.jsonl")
	orig, _ := os.ReadFile(p)
	extra, _ := json.Marshal(map[string]any{"archive_number": 52, "source_file_sha256": "irrelevant"})
	activeBad, _ := json.Marshal(map[string]any{"archive_number": 71, "source_file_sha256": "invalid"})
	idx := append(append(append(append([]byte{}, extra...), '\n'), activeBad...), '\n')
	idx = append(idx, orig...)
	put(t, p, idx)
	mf := filepath.Join(root, "toolchain", "switch_port", "maps", "manifest.json")
	var v map[string]any
	b, _ := os.ReadFile(mf)
	_ = json.Unmarshal(b, &v)
	v["games"].(map[string]any)["ms2"].(map[string]any)["native_records_sha256"] = sha(idx)
	b, _ = json.Marshal(v)
	put(t, mf, b)
	if e := migrate(root, ms2, finale, &bytes.Buffer{}); e == nil || !strings.Contains(e.Error(), "invalid active source archive identity") {
		t.Fatalf("accepted invalid active record: %v", e)
	}
}
