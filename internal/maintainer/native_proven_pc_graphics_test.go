package maintainer

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeProvenPCBridgeFailsClosedWithoutMasters(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r, err := NativeProvenPCGraphics(root, NativeProvenPCGraphicsOptions{MS2Master: filepath.Join(dir, "ms2", "masterWindows"), FinaleMaster: filepath.Join(dir, "finale", "masterWindows"), ValidateOnly: true})
	if err == nil || !strings.Contains(err.Error(), "pristine source missing") || r.Status != "BLOCKED" {
		t.Fatalf("missing originals accepted: %+v %v", r, err)
	}
	if len(r.Archives) != 0 {
		t.Fatalf("partial check reported success: %+v", r)
	}
	r, err = NativeProvenPCGraphics(root, NativeProvenPCGraphicsOptions{MS2Master: filepath.Join(dir, "ms2"), FinaleMaster: filepath.Join(dir, "finale"), OutputDir: filepath.Join(dir, "output")})
	if err == nil || !strings.Contains(err.Error(), "renderer binary") {
		t.Fatalf("missing renderer accepted: %+v %v", r, err)
	}
}

func TestNativeProvenPCBridgeCanonicalAllowlist(t *testing.T) {
	if len(nativeProvenPCFamilyIDs) != 17 {
		t.Fatal("unexpected allowlist expansion")
	}
	visited := make(map[string]bool)
	for _, id := range nativeProvenPCFamilyIDs {
		if visited[id] {
			t.Fatal("duplicate family", id)
		}
		visited[id] = true
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m nativeProvenFamilyManifest
	if err = json.Unmarshal(blob, &m); err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Assets {
		if visited[a.ID] {
			validMember := a.GDATIndex >= 0 && a.Inner != "" && len(a.SourceVariants) == 1 && len(a.SourceVariants[0].SHA) == 64
			if a.ID == "MS2_DLC_JOB_LABELS" {
				validMember = a.Archive == 55 && len(a.Targets) == 6
				for k, m := range a.Targets {
					validMember = validMember && m.Index == 1541+k && len(m.SHA) == 64 && m.Inner != ""
				}
			}
			if !a.Enabled || a.Dataset == "" || a.Archive < 0 || !validMember {
				t.Fatalf("invalid current source family metadata: %s", a.ID)
			}
			delete(visited, a.ID)
		}
	}
	if len(visited) > 0 {
		t.Fatalf("allowlist is stale: %v", visited)
	}
	var b bytes.Buffer
	if err = WriteNativeProvenPCGraphicsReport(&b, NativeProvenPCGraphicsReport{Status: "PASS_READ_ONLY_ORIGINAL_SOURCE", Families: 17}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b.Bytes(), []byte(`"families": 17`)) {
		t.Fatalf("invalid JSON %s", b.String())
	}
}
