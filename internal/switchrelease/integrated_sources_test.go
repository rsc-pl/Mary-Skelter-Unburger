package switchrelease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGDAT(game, name string) []byte { return []byte("GDAT\x01\x00\x00\x00" + game + "/" + name) }
func fakeSHA(value []byte) string       { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func fakeInputs(t *testing.T) (string, map[string]map[string]string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	pins := make(map[string]map[string]string)
	source := make(map[string]string)
	for _, game := range games {
		source[game] = filepath.Join(t.TempDir(), "masterNX64")
		if e := os.MkdirAll(source[game], 0700); e != nil {
			t.Fatal(e)
		}
		pins[game] = make(map[string]string)
		for _, name := range append([]string{"GxArchivedFile074.dat"}, expectedTextArchiveNames(game)...) {
			// The graphics and text input sets must not overlap.
			if name == "GxArchivedFile074.dat" {
				if game == "finale" {
					continue
				}
			}
			b := fakeGDAT(game, name)
			pins[game][name] = fakeSHA(b)
			if e := os.WriteFile(filepath.Join(source[game], name), b, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	return root, pins, source
}

func TestIntegratedCacheReuseAndPartialImport(t *testing.T) {
	root, pins, source := fakeInputs(t)
	// Already imported original graphic sources must be reused intact.
	graphic := filepath.Join(cacheMaster(root, "ms2"), "GxArchivedFile074.dat")
	if e := os.MkdirAll(filepath.Dir(graphic), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(graphic, fakeGDAT("ms2", "GxArchivedFile074.dat"), 0600); e != nil {
		t.Fatal(e)
	}
	st, e := inspectIntegratedCacheWithPins(root, pins)
	if e != nil || len(st[0].Verified) != 1 {
		t.Fatalf("inspect %+v %v", st, e)
	}
	st, e = ensureIntegratedCacheWithPins(root, pins, source)
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range st {
		if !s.Ready() || len(s.Verified) != len(pins[s.Game]) {
			t.Fatalf("incomplete %+v", s)
		}
	}
	first, _ := os.Stat(graphic)
	st, e = ensureIntegratedCacheWithPins(root, pins, map[string]string{})
	if e != nil || !st[0].Ready() || !st[1].Ready() {
		t.Fatalf("second pass %+v %v", st, e)
	}
	after, _ := os.Stat(graphic)
	if !os.SameFile(first, after) {
		t.Fatal("cached original was replaced")
	}
}

func TestIntegratedCacheMissingFailsBeforeAnyImport(t *testing.T) {
	root, pins, source := fakeInputs(t)
	if e := os.Remove(filepath.Join(source["finale"], "GxArchivedFile001.dat")); e != nil {
		t.Fatal(e)
	}
	if _, e := ensureIntegratedCacheWithPins(root, pins, source); e == nil || !strings.Contains(e.Error(), "finale/GxArchivedFile001.dat") {
		t.Fatalf("missing preflight %v", e)
	}
	if _, e := os.Stat(cacheMaster(root, "ms2")); !os.IsNotExist(e) {
		t.Fatalf("partial copy despite preflight: %v", e)
	}
}

func TestIntegratedCacheCorruptAndLinkedInputsFailClosed(t *testing.T) {
	root, pins, source := fakeInputs(t)
	if e := os.WriteFile(filepath.Join(source["ms2"], "GxArchivedFile074.dat"), []byte("wrong"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ensureIntegratedCacheWithPins(root, pins, source); e == nil || !strings.Contains(e.Error(), "wrong original/update SHA") {
		t.Fatalf("wrong source not refused: %v", e)
	}
	if e := os.Remove(filepath.Join(source["ms2"], "GxArchivedFile074.dat")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink("missing", filepath.Join(source["ms2"], "GxArchivedFile074.dat")); e != nil {
		t.Fatal(e)
	}
	if _, e := ensureIntegratedCacheWithPins(root, pins, source); e == nil || !strings.Contains(e.Error(), "unsafe source file") {
		t.Fatalf("symlink input not refused %v", e)
	}
	if e := os.Remove(filepath.Join(source["ms2"], "GxArchivedFile074.dat")); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(source["ms2"], "GxArchivedFile074.dat"), fakeGDAT("ms2", "GxArchivedFile074.dat"), 0600); e != nil {
		t.Fatal(e)
	}
	dir := cacheMaster(root, "ms2")
	if e := os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "GxArchivedFile074.dat"), []byte("modified"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ensureIntegratedCacheWithPins(root, pins, source); e == nil || !strings.Contains(e.Error(), "wrong SHA") {
		t.Fatalf("modified cached original not refused %v", e)
	}
}

func TestIntegratedSourceNativePinVerification(t *testing.T) {
	root := t.TempDir()
	mappingDir := filepath.Join(root, builderRel)
	if e := os.MkdirAll(mappingDir, 0700); e != nil {
		t.Fatal(e)
	}
	data := map[string]any{"phase": "switch_graphics_mapping_v1", "applicable_source_target_count": 40, "games": map[string]any{}}
	gamesMap := data["games"].(map[string]any)
	indexMap := map[string]any{"games": map[string]any{}}
	for _, game := range games {
		gamesMap[game] = map[string]any{"archives": map[string]any{"GxArchivedFile074.dat": map[string]string{"sha256": strings.Repeat("a", 64)}}}
		// Graphical archive 074 is separate from all native text archives.
		rows := []byte{}
		for _, num := range textArchiveNumbers[game] {
			row, _ := json.Marshal(nativeInputRecord{Archive: num, SHA: strings.Repeat("b", 64)})
			rows = append(rows, append(row, '\n')...)
		}
		idx := filepath.Join(root, ".local", "switch_text_port", game, "western-native-records.jsonl")
		if e := os.MkdirAll(filepath.Dir(idx), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(idx, rows, 0600); e != nil {
			t.Fatal(e)
		}
		indexMap["games"].(map[string]any)[game] = map[string]string{"native_records_sha256": fakeSHA(rows)}
	}
	mapB, _ := json.Marshal(data)
	if e := os.WriteFile(filepath.Join(mappingDir, mappingFile), mapB, 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "toolchain", "switch_port", "maps", "manifest.json")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	indexB, _ := json.Marshal(indexMap)
	if e := os.WriteFile(path, indexB, 0600); e != nil {
		t.Fatal(e)
	}
	got, e := integratedInputPins(root)
	if e != nil || len(got["ms2"]) != 4 || len(got["finale"]) != 6 {
		t.Fatalf("pins %+v %v", got, e)
	}
	idx := filepath.Join(root, ".local", "switch_text_port", "ms2", "western-native-records.jsonl")
	if e := os.WriteFile(idx, append([]byte(" "), []byte("\n")...), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := integratedInputPins(root); e == nil {
		t.Fatal("accepted mutated native input index")
	}
}
