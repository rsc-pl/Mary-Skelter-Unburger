package switchrelease

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testRoot(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func checksum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func canonicalFixture(t *testing.T, root string, corrupt bool) string {
	t.Helper()
	m, e := LoadMapping(root)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "canonical.zip")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	z := zip.NewWriter(f)
	var archives []map[string]string
	for _, game := range games {
		l := accepted[game]
		for _, n := range required(m, game) {
			data := []byte("GDAT\x01\x00\x00\x00test-" + game + "-" + n)
			archives = append(archives, map[string]string{"game": game, "archive": n, "output_sha256": checksum(data)})
			if corrupt && game == "ms2" {
				data = append(data, 0x01)
			}
			w, e := z.Create("2_EMULATOR_EDEN/load/" + l.TitleID + "/UNBURGER_TEST/romfs/masterNX64/" + n)
			if e != nil {
				t.Fatal(e)
			}
			w.Write(data)
		}
		ips, e := acceptedIPS(root, game)
		if e != nil {
			t.Fatal(e)
		}
		w, e := z.Create("2_EMULATOR_EDEN/load/" + l.TitleID + "/UNBURGER_TEST/exefs/" + l.BuildID + ".ips")
		if e != nil {
			t.Fatal(e)
		}
		w.Write(ips)
	}
	report := map[string]any{"source_target_count": 40, "modified_target_count": 40, "preserved_target_count": 0, "preserved_targets": []map[string]any{}, "canonical_target_count": 1, "canonical_targets": []map[string]any{{"game": "ms2", "asset_id": "MS2_CAMP_TOP2_CHARACTER_NAMES", "archive": "GxArchivedFile074.dat", "gdat_index": 133, "inner_name": "camp_top_2.tid", "policy": "canonical_whole_words_v2", "changed_names": 8, "untouched_names": 6, "canonical_cap_height": 25, "original_source_tid_sha256": "6cac6e1c439db75ac43084e3452fb959c11984030b178f7b6a23c2ba445d8f39", "font_sha256": strings.Repeat("a", 64)}}, "non_target_gdat_payloads_byte_identical": true, "non_target_cl3_members_byte_identical": true, "archives": archives}
	b, _ := json.Marshal(report)
	w, e := z.Create("R32C3_BUILD_REPORT.json")
	if e != nil {
		t.Fatal(e)
	}
	w.Write(b)
	w, e = z.Create("R32C3_PREVIEWS/secret-preview.png")
	if e != nil {
		t.Fatal(e)
	}
	w.Write([]byte("should never leak"))
	w, e = z.Create("README_TEST.txt")
	if e != nil {
		t.Fatal(e)
	}
	w.Write([]byte("/home/user/private/romfs"))
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestMappingAndVersion(t *testing.T) {
	root := testRoot(t)
	m, e := LoadMapping(root)
	if e != nil {
		t.Fatal(e)
	}
	if len(required(m, "ms2")) != 1 || len(required(m, "finale")) != 2 {
		t.Fatalf("wrong archive count: %+v", m)
	}
	// version.txt is an owner-controlled release version, not a fixed test fixture.
	// Exercise the same loader with several valid versions in an isolated root.
	versionRoot := t.TempDir()
	for _, want := range []string{"0.1.0", "0.4.2", "1.2.3"} {
		if err := os.WriteFile(filepath.Join(versionRoot, "version.txt"), []byte(want+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if got, err := version(versionRoot, ""); err != nil || got != want {
			t.Fatalf("version(%q) = %q, %v", want, got, err)
		}
	}
	for _, v := range []string{"../../escape", "bad/name", "", "version\nattack"} {
		if v == "" {
			continue
		}
		if _, e = version(root, v); e == nil {
			t.Fatalf("accepted unsafe version %q", v)
		}
	}
}
func TestPackCanonical(t *testing.T) {
	root := testRoot(t)
	canonical := canonicalFixture(t, root, false)
	for _, plat := range []string{"atmosphere", "eden"} {
		t.Run(plat, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "release.zip")
			result, e := PackCanonical(root, canonical, out, "0.1.0", plat)
			if e != nil {
				t.Fatal(e)
			}
			z, e := zip.OpenReader(out)
			if e != nil {
				t.Fatal(e)
			}
			defer z.Close()
			if len(z.File) != result.Files {
				t.Fatalf("files mismatch %d != %d", len(z.File), result.Files)
			}
			expected := map[string]bool{}
			for _, game := range games {
				l := accepted[game]
				name := l.BuildID + ".ips"
				if plat == "eden" {
					base := edenFolder(game, "0.1.0")
					expected[base+"exefs/"+name] = true
					for _, archive := range requiredMapping(t, root, game) {
						expected[base+"romfs/masterNX64/"+archive] = true
					}
				} else {
					expected["atmosphere/exefs_patches/MarySkelter_UNBURGER/"+name] = true
					for _, archive := range requiredMapping(t, root, game) {
						expected["atmosphere/contents/"+l.TitleID+"/romfs/masterNX64/"+archive] = true
					}
				}
			}
			if len(z.File) != len(expected) {
				t.Fatalf("expected %d exact ZIP members, got %d", len(expected), len(z.File))
			}
			for _, f := range z.File {
				if !expected[f.Name] {
					t.Fatalf("unexpected entry in %s ZIP: %q", plat, f.Name)
				}
				delete(expected, f.Name)
				if strings.Contains(f.Name, "load/") || strings.HasPrefix(f.Name, "1_") || strings.HasPrefix(f.Name, "2_") || strings.Contains(f.Name, "README") {
					t.Fatalf("wrapper/metadata leaked: %q", f.Name)
				}
			}
			if len(expected) != 0 {
				t.Fatalf("missing entries: %v", expected)
			}
			sum, e := sumFile(out)
			if e != nil || sum != result.SHA256 {
				t.Fatalf("zip hash mismatch")
			}
		})
	}
}
func TestRejectCorruptCanonicalPayload(t *testing.T) {
	root := testRoot(t)
	fixture := canonicalFixture(t, root, true)
	_, e := PackCanonical(root, fixture, filepath.Join(t.TempDir(), "out.zip"), "0.1.0", "eden")
	if e == nil || !strings.Contains(e.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", e)
	}
}
func TestRejectIncompleteOrWrongSources(t *testing.T) {
	root := testRoot(t)
	m, e := LoadMapping(root)
	if e != nil {
		t.Fatal(e)
	}
	d := t.TempDir()
	if e = ValidateSource(m, "ms2", d); e == nil {
		t.Fatal("empty source accepted")
	}
	n := required(m, "ms2")[0]
	if e = os.WriteFile(filepath.Join(d, n), []byte("GDAT\x01\x00\x00\x00test"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = ValidateSource(m, "ms2", d); e == nil || !strings.Contains(e.Error(), "SHA-256") {
		t.Fatalf("wrong hash accepted: %v", e)
	}
}

func requiredMapping(t *testing.T, root, game string) []string {
	t.Helper()
	m, e := LoadMapping(root)
	if e != nil {
		t.Fatal(e)
	}
	return required(m, game)
}

func TestSeparateOutputNames(t *testing.T) {
	if releaseName("0.1.0", "eden") != "Mary-Skelter-2-Finale-UNBURGER-Switch-Eden-v0.1.0.zip" {
		t.Fatal("Eden name mismatch")
	}
	if releaseName("0.1.0", "atmosphere") != "Mary-Skelter-2-Finale-UNBURGER-Switch-Atmosphere-v0.1.0.zip" {
		t.Fatal("Atmosphere name mismatch")
	}
	if edenFolder("ms2", "0.1.0") == edenFolder("finale", "0.1.0") {
		t.Fatal("Eden game mods not distinguishable")
	}
	_, e := PackCanonical(testRoot(t), canonicalFixture(t, testRoot(t), false), filepath.Join(t.TempDir(), "x.zip"), "0.1.0", "both")
	if e == nil {
		t.Fatal("combined wrapper must not be a standalone ZIP")
	}
}

func TestPackagePairFromOneCanonicalBuild(t *testing.T) {
	root := testRoot(t)
	canonical := canonicalFixture(t, root, false)
	out := t.TempDir()
	stage := t.TempDir()
	results, err := packagePair(root, canonical, out, stage, "0.1.0", "both")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected two separate ZIPs, got %d", len(results))
	}
	for i, consumer := range []string{"atmosphere", "eden"} {
		got := results[i]
		if got.Platform != consumer || filepath.Base(got.ZIP) != releaseName("0.1.0", consumer) {
			t.Fatalf("wrong output: %+v", got)
		}
		z, e := zip.OpenReader(got.ZIP)
		if e != nil {
			t.Fatal(e)
		}
		if len(z.File) != 5 {
			t.Fatalf("expected three DAT + two IPS, got %d", len(z.File))
		}
		for _, f := range z.File {
			if consumer == "eden" && !strings.HasPrefix(f.Name, "Mary-Skelter-") {
				t.Fatalf("Eden ZIP must contain only mod folders, got %q", f.Name)
			}
			if consumer == "atmosphere" && !strings.HasPrefix(f.Name, "atmosphere/") {
				t.Fatalf("Atmosphere ZIP must be SD-root ready, got %q", f.Name)
			}
		}
		z.Close()
		b, e := os.ReadFile(got.ZIP + ".sha256")
		if e != nil {
			t.Fatal(e)
		}
		if string(b) != got.SHA256+"  "+filepath.Base(got.ZIP)+"\n" {
			t.Fatal("checksum sidecar mismatch")
		}
	}
	// No overwrite and no partial new outputs if only one already exists.
	_, err = packagePair(root, canonical, out, t.TempDir(), "0.1.0", "both")
	if err == nil || !strings.Contains(err.Error(), "release exists") {
		t.Fatalf("missing overwrite protection: %v", err)
	}
}

// rejects the old 40/40 graphics report even if its archive files are
// individually well-formed. The original MS2 074/g133 must remain stock.
func TestRejectOldAllFortyModifiedGraphicsContract(t *testing.T) {
	root := testRoot(t)
	baseline := canonicalFixture(t, root, false)
	old, err := zip.OpenReader(baseline)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	target := filepath.Join(t.TempDir(), "legacy-all-40.zip")
	dest, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(dest)
	for _, entry := range old.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if entry.Name == "R32C3_BUILD_REPORT.json" {
			var report map[string]any
			if err := json.Unmarshal(content, &report); err != nil {
				t.Fatal(err)
			}
			// Historic 40/40 was the *broken* full rerender before V2.
			// It must be rejected without the specific whole-word proof.
			delete(report, "canonical_targets")
			delete(report, "canonical_target_count")
			content, err = json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
		}
		f, err := w.Create(entry.Name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dest.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = PackCanonical(root, target, filepath.Join(t.TempDir(), "out.zip"), "0.1.0", "eden")
	if err == nil || !strings.Contains(err.Error(), "canonical QA report invalid") {
		t.Fatalf("unproven 40/40 report unexpectedly accepted: %v", err)
	}
}
