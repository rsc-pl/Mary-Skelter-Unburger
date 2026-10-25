package maintainer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeJSONTest(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildPatchBundlesNativeAndNoLegacySaveTool(t *testing.T) {
	root := t.TempDir()
	build := filepath.Join(root, ".local", "build")
	writeJSONTest(t, filepath.Join(build, "change_manifest.json"), map[string]any{
		"MS2_PC_LOCALIZED": map[string]any{"changed_fields": 2},
		"MSF_PC_LOCALIZED": map[string]any{"changed_fields": 1},
		"MSF_PC_SYSTEM":    map[string]any{"changed_fields": 0},
	})
	writeJSONTest(t, filepath.Join(build, "reports", "graphics.json"), map[string]any{"status": "PASS", "results": []map[string]any{
		{"id": "MS2_GRAPHIC", "dataset": "MS2_PC_LOCALIZED", "archive": 55},
		{"id": "MSF_GRAPHIC", "dataset": "MSF_PC_SYSTEM", "archive": 73},
	}})
	writeJSONTest(t, filepath.Join(build, "reports", "MS2_PC_LOCALIZED.rebuild.json"), map[string]any{"archives": []map[string]any{{"archive_number": 52, "byte_identical": false}}})
	writeJSONTest(t, filepath.Join(build, "reports", "MSF_PC_LOCALIZED.rebuild.json"), map[string]any{"archives": []map[string]any{{"archive_number": 70, "byte_identical": false}}})
	for ds, arcs := range map[string][]int{"MS2_PC_LOCALIZED": {52, 55}, "MSF_PC_LOCALIZED": {70}, "MSF_PC_SYSTEM": {73}} {
		d := filepath.Join(build, "rebuilt", ds, "masterWindows")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, a := range arcs {
			if err := os.WriteFile(filepath.Join(d, archiveName(a)), []byte(ds+archiveName(a)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	dist := filepath.Join(build, "dist.staging")
	if err := BuildPatchBundles(root, dist, nil); err != nil {
		t.Fatal(err)
	}
	ms2 := filepath.Join(dist, "MS2_EN_FIDELITY_PATCH")
	msf := filepath.Join(dist, "MSF_EN_FIDELITY_PATCH")
	for _, p := range []string{filepath.Join(ms2, "masterWindows", archiveName(52)), filepath.Join(ms2, "masterWindows", archiveName(55)), filepath.Join(msf, "masterWindows", archiveName(70)), filepath.Join(msf, "masterWindows", archiveName(73))} {
		if !file(p) {
			t.Fatalf("missing bundle file %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(ms2, "tools")); !os.IsNotExist(err) {
		t.Fatalf("legacy save-converter tools directory unexpectedly bundled")
	}
	var m bundleManifest
	raw, err := os.ReadFile(filepath.Join(ms2, "PATCH_MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.ChangedFields != 2 || len(m.Files) != 2 || len(m.GraphicalTextAssets) != 1 || m.GraphicalTextAssets[0] != "MS2_GRAPHIC" {
		t.Fatalf("unexpected MS2 manifest: %+v", m)
	}
}

func archiveName(n int) string { return "GxArchivedFile" + threeDigits(n) + ".dat" }
func threeDigits(n int) string {
	return string([]byte{byte('0' + (n/100)%10), byte('0' + (n/10)%10), byte('0' + n%10)})
}
