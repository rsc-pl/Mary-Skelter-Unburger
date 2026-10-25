package switchrelease

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/switchtext"
)

func fixtureTextAssets(t *testing.T, root string) map[string]TextAsset {
	t.Helper()
	assets := map[string]TextAsset{}
	for _, game := range games {
		for _, name := range expectedTextArchiveNames(game) {
			d := []byte("GDAT\x01\x00\x00\x00native-go-" + game + "-" + name)
			dir := filepath.Join(t.TempDir(), game)
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(dir, name)
			if e := os.WriteFile(path, d, 0600); e != nil {
				t.Fatal(e)
			}
			assets[game+"/"+name] = TextAsset{Path: path, SHA256: checksum(d)}
		}
	}
	return assets
}

func TestIntegratedPackExactTwoConsumerLayouts(t *testing.T) {
	root := testRoot(t)
	canonical := canonicalFixture(t, root, false)
	text := fixtureTextAssets(t, root)
	for _, consumer := range []string{"eden", "atmosphere"} {
		t.Run(consumer, func(t *testing.T) {
			zipPath := filepath.Join(t.TempDir(), "combined.zip")
			res, e := packCanonical(root, canonical, zipPath, "0.1.0", consumer, text)
			if e != nil {
				t.Fatal(e)
			}
			if res.Files != 13 {
				t.Fatalf("expected 8 native text + 3 graphics + 2 IPS files, got %d", res.Files)
			}
			z, e := zip.OpenReader(zipPath)
			if e != nil {
				t.Fatal(e)
			}
			defer z.Close()
			names := map[string]bool{}
			for _, f := range z.File {
				if names[f.Name] {
					t.Fatalf("duplicate output %q", f.Name)
				}
				names[f.Name] = true
				if strings.Contains(f.Name, "native-write-report") || strings.Contains(f.Name, ".jsonl") || strings.Contains(f.Name, "/load/") {
					t.Fatalf("private data leaked %q", f.Name)
				}
			}
			for _, game := range games {
				for _, name := range expectedTextArchiveNames(game) {
					out := edenFolder(game, "0.1.0") + "romfs/masterNX64/" + name
					if consumer == "atmosphere" {
						out = "atmosphere/contents/" + accepted[game].TitleID + "/romfs/masterNX64/" + name
					}
					if !names[out] {
						t.Fatalf("missing native archive %s", out)
					}
				}
			}
		})
	}
}

func TestIntegratedPackRejectsMismatchWithoutPublishing(t *testing.T) {
	root := testRoot(t)
	canonical := canonicalFixture(t, root, false)
	assets := fixtureTextAssets(t, root)
	delete(assets, "ms2/GxArchivedFile073.dat")
	out := filepath.Join(t.TempDir(), "bad.zip")
	if _, e := packCanonical(root, canonical, out, "0.1.0", "eden", assets); e == nil || !strings.Contains(e.Error(), "missing native-Go text archive") {
		t.Fatalf("missing expected failure: %v", e)
	}
	if _, e := os.Stat(out); !os.IsNotExist(e) {
		t.Fatalf("partial ZIP on failure %v", e)
	}
	assets = fixtureTextAssets(t, root)
	ar := assets["ms2/GxArchivedFile073.dat"]
	ar.SHA256 = strings.Repeat("0", 64)
	assets["ms2/GxArchivedFile073.dat"] = ar
	if _, e := packCanonical(root, canonical, out, "0.1.0", "eden", assets); e == nil || !strings.Contains(e.Error(), "changed during ZIP") {
		t.Fatalf("SHA failure not detected: %v", e)
	}
	if _, e := os.Stat(out); !os.IsNotExist(e) {
		t.Fatalf("partial ZIP on failure %v", e)
	}
}

func TestIntegratedCandidateExplicitGates(t *testing.T) {
	_, err := BuildIntegratedCandidate(IntegratedOptions{})
	if err == nil || !strings.Contains(err.Error(), "NOT runtime-approved") {
		t.Fatalf("missing approval gate: %v", err)
	}
	_, err = BuildIntegratedCandidate(IntegratedOptions{Options: Options{Candidate: true}})
	if err == nil || !strings.Contains(err.Error(), "switch_graphics_mapping_closed") {
		t.Fatalf("missing source gate: %v", err)
	}
}

func TestNativeAssetValidationRejectsCollisionsAndExtra(t *testing.T) {
	root := testRoot(t)
	mapping, e := LoadMapping(root)
	if e != nil {
		t.Fatal(e)
	}
	outputs := fixtureTextAssets(t, root)
	// Build a single native Go output directory, mimicking the writer report.
	dir := t.TempDir()
	nr := switchtext.NativeWriteReport{Schema: 1, Status: "RESEARCH_CANDIDATE_NOT_RELEASE_READY", Game: "ms2"}
	for _, name := range expectedTextArchiveNames("ms2") {
		asset := outputs["ms2/"+name]
		data, e := os.ReadFile(asset.Path)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			t.Fatal(e)
		}
		var number int
		if _, e = fmt.Sscanf(name, "GxArchivedFile%03d.dat", &number); e != nil {
			t.Fatal(e)
		}
		nr.Archives = append(nr.Archives, switchtext.NativeArchiveResult{Number: number, SourceSHA: strings.Repeat("a", 64), OutputSHA: checksum(data), EditedEntries: 1, EditedFields: 1})
	}
	if e = os.WriteFile(filepath.Join(dir, "native-write-report.json"), []byte("{}"), 0600); e != nil {
		t.Fatal(e)
	}
	sr := switchtext.Report{Game: "ms2", MappedFields: 6, EditedFields: 3, UnchangedFields: 3}
	if _, e = validateTextAssets(root, mapping, "ms2", dir, sr, nr); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("don't pack me"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = validateTextAssets(root, mapping, "ms2", dir, sr, nr); e == nil {
		t.Fatal("extraneous writer member was not rejected")
	}
	if e = os.Remove(filepath.Join(dir, "secret.txt")); e != nil {
		t.Fatal(e)
	}
	mapping.Games["ms2"].Archives["GxArchivedFile073.dat"] = struct {
		SHA256 string `json:"sha256"`
	}{SHA256: "fake"}
	if _, e = validateTextAssets(root, mapping, "ms2", dir, sr, nr); e == nil || !strings.Contains(e.Error(), "collision") {
		t.Fatalf("archive collision not refused: %v", e)
	}
}
