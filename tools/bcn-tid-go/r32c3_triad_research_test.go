package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func triadFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func TestThreeFamiliesCanonicalSources(t *testing.T) {
	root := triadFixtureRoot(t)
	for _, id := range []string{"MS2_PARTY_FORMATION_NAMES", "MS2_EQUIPMENT_HEADER_NAMES", "MSF_EQUIPMENT_HEADER_NAMES"} {
		t.Run(id, func(t *testing.T) {
			cfg, e := loadR32C3Triad(root, id, false)
			if e != nil {
				t.Fatal(e)
			}
			if len(cfg.Asset.Rows) != pendingR32C3[id].Rows {
				t.Fatal("rows lost")
			}
			if len(cfg.Font) != 0 {
				t.Fatal("preflight unexpectedly loaded owner font")
			}
			// The source renderer must NOT fall back to an unpinned substitute font.
			if _, err := renderR32C3Triad(root, id, nil); err == nil {
				t.Fatal("accepted no genuine source/private font")
			}
		})
	}
}
func TestRejectUnlistedAndMutatedGeometry(t *testing.T) {
	root := triadFixtureRoot(t)
	if _, e := loadR32C3Triad(root, "MS2_RESULT_NAMES", false); e == nil {
		t.Fatal("owner-frozen Result exposed in experimental renderer")
	}
	temp := t.TempDir()
	if e := os.MkdirAll(filepath.Join(temp, "graphics"), 0755); e != nil {
		t.Fatal(e)
	}
	manifest, e := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	typ, e := os.ReadFile(filepath.Join(root, "graphics", "ui_typography.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(temp, "graphics", "ui_typography.json"), typ, 0600); e != nil {
		t.Fatal(e)
	}
	var source map[string]any
	if e = json.Unmarshal(manifest, &source); e != nil {
		t.Fatal(e)
	}
	assets := source["assets"].([]any)
	mutated := false
	for _, v := range assets {
		a := v.(map[string]any)
		if a["id"] == "MS2_PARTY_FORMATION_NAMES" {
			rows := a["rows"].([]any)
			rows[0].(map[string]any)["text"] = "FAKE_NAME"
			mutated = true
			break
		}
	}
	if !mutated {
		t.Fatal("missing canonical test asset")
	}
	bad, e := json.Marshal(source)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(temp, "graphics", "manifest.json"), bad, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := loadR32C3Triad(temp, "MS2_PARTY_FORMATION_NAMES", false); e == nil || !strings.Contains(e.Error(), "drift") {
		t.Fatalf("tampered manifest not rejected: %v", e)
	}
}
func TestRealSourceTIDsWithoutFont(t *testing.T) {
	root := triadFixtureRoot(t)
	tests := []struct{ id, path string }{
		{"MS2_PARTY_FORMATION_NAMES", "source-party-formation"},
		{"MS2_EQUIPMENT_HEADER_NAMES", "source-equipment-header"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			sourcePath, e := privateassets.Resolve(root, tt.path)
			if e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(sourcePath)
			if e != nil {
				t.Fatal(e)
			}
			if digest(b) != pendingR32C3[tt.id].SHA {
				t.Fatal("original source SHA differs")
			}
			// Only source validation here. Full font raster / strict codec remain HOLD.
		})
	}
}

func TestAtomicPublishRollback(t *testing.T) {
	d := t.TempDir()
	staged := make([]triadStage, 3)
	for i := range staged {
		staged[i] = triadStage{temp: filepath.Join(d, fmt.Sprintf("stage%d", i)), out: filepath.Join(d, fmt.Sprintf("target%d", i))}
		if err := os.WriteFile(staged[i].temp, []byte{byte(i), 99}, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Failure on third publish must remove ONLY the two links created by us.
	if _, err := publishTriad(staged, func(i int) error {
		if i == 2 {
			return errors.New("fault")
		}
		return nil
	}); err == nil {
		t.Fatal("injected failure accepted")
	}
	for _, s := range staged {
		if _, err := os.Lstat(s.out); !os.IsNotExist(err) {
			t.Fatalf("partial output after rollback: %s (%v)", s.out, err)
		}
		if _, err := os.Stat(s.temp); err != nil {
			t.Fatalf("source staging removed: %v", err)
		}
	}
	// Late owner file collision must never be overwritten.
	if err := os.WriteFile(staged[2].out, []byte("owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := publishTriad(staged, nil); err == nil {
		t.Fatal("existing owner output overwritten")
	}
	owner, err := os.ReadFile(staged[2].out)
	if err != nil || string(owner) != "owner" {
		t.Fatal("owner output damaged")
	}
	for _, s := range staged[:2] {
		if _, err := os.Lstat(s.out); !os.IsNotExist(err) {
			t.Fatal("partial late publish survived")
		}
	}
}
