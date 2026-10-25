package maintainer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativePCDeferredCandidateIsExplicitAndSourcePinned(t *testing.T) {
	base := nativePCRequestedFamilies(false)
	candidate := nativePCRequestedFamilies(true)
	if len(base) != 17 || len(candidate) != 19 {
		t.Fatalf("default/deferred family counts: %d/%d", len(base), len(candidate))
	}
	if len(nativeProvenPCFamilyIDs) != 17 || candidate[17] != "MS2_RESULT_NAMES" || candidate[18] != "MS2_BATTLE_NAMES" {
		t.Fatalf("unexpected deferred family set: %v", candidate)
	}
	for _, id := range candidate {
		if id == "MS2_CAMP_CHARACTER_NAMES" {
			t.Fatal("unverified Camp included")
		}
	}
	manifest := nativeProvenRendererManifest{Version: 1, ProjectRoot: ".", EnableDeferredNameBanks: true}
	blob, err := json.Marshal(manifest)
	if err != nil || !strings.Contains(string(blob), `"enable_deferred_name_banks":true`) {
		t.Fatalf("deferred opt-in missing from renderer output: %s %v", blob, err)
	}
	manifest.EnableDeferredNameBanks = false
	blob, err = json.Marshal(manifest)
	if err != nil || strings.Contains(string(blob), "enable_deferred_name_banks") {
		t.Fatal("renderer flag unexpectedly enabled by default")
	}
}

func TestNativePCDeferredManifestDriftFailsBeforeSourceRead(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "graphics", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err = json.Unmarshal(source, &data); err != nil {
		t.Fatal(err)
	}
	assets := data["assets"].([]any)
	found := false
	for _, item := range assets {
		a := item.(map[string]any)
		if a["id"] == "MS2_RESULT_NAMES" {
			a["gdat_index"] = float64(1332) // corruption must fail before touching a master
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing canonical Result")
	}
	altered, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "graphics"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graphics", "manifest.json"), altered, 0600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "nonexistent")
	report, err := NativeProvenPCGraphics(root, NativeProvenPCGraphicsOptions{MS2Master: missing, FinaleMaster: missing, ValidateOnly: true, IncludeDeferredNameBanks: true})
	if err == nil || !strings.Contains(err.Error(), "deferred namebank MS2_RESULT_NAMES source metadata drift") || report.Status != "BLOCKED" {
		t.Fatalf("corrupted namebank metadata accepted: %+v %v", report, err)
	}
}
