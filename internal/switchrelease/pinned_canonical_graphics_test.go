package switchrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedCanonicalGraphicsInputs(t *testing.T) {
	root := testRoot(t)
	canonical := canonicalFixture(t, root, false)
	sum, err := sumFile(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err = validatePinnedCanonicalGraphics(canonical, sum); err != nil {
		t.Fatal(err)
	}
	if err = validatePinnedCanonicalGraphics(canonical, strings.ToUpper(sum)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, path, pin string }{
		{"missing-pin", canonical, ""}, {"missing-path", "", sum},
		{"wrong-pin", canonical, strings.Repeat("0", 64)}, {"nonhex", canonical, strings.Repeat("g", 64)},
		{"wrong-length", canonical, "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if e := validatePinnedCanonicalGraphics(tc.path, tc.pin); e == nil {
				t.Fatal("accepted invalid canonical candidate")
			}
		})
	}
	link := filepath.Join(t.TempDir(), "candidate.zip")
	if err := os.Symlink(canonical, link); err != nil {
		t.Fatal(err)
	}
	if err := validatePinnedCanonicalGraphics(link, sum); err == nil {
		t.Fatal("accepted symlink to canonical source")
	}
	if err := validatePinnedCanonicalGraphics(filepath.Dir(canonical), sum); err == nil {
		t.Fatal("accepted canonical directory")
	}
	// A supported ZIP must still pass complete existing canonical layout checks.
	for _, target := range []string{"eden", "atmosphere"} {
		out := filepath.Join(t.TempDir(), target+".zip")
		if _, err := PackCanonical(root, canonical, out, "0.1.0", target); err != nil {
			t.Fatal(err)
		}
	}
	corrupt := canonicalFixture(t, root, true)
	corruptHash, err := sumFile(corrupt)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePinnedCanonicalGraphics(corrupt, corruptHash); err != nil {
		t.Fatal(err)
	}
	if _, err := PackCanonical(root, corrupt, filepath.Join(t.TempDir(), "out.zip"), "0.1.0", "eden"); err == nil {
		t.Fatal("transport pin mistaken for canonical content approval")
	}
}

func TestPrebuiltExplicitPreflightBeforeSourceOrPython(t *testing.T) {
	_, err := BuildIntegratedCandidate(IntegratedOptions{Options: Options{Candidate: true, MS2Master: "not-a-real-ms2", FinaleMaster: "not-a-real-finale"}, CanonicalGraphicsZIP: "missing.zip", CanonicalGraphicsSHA256: strings.Repeat("0", 64)})
	if err == nil || !strings.Contains(err.Error(), "prebuilt graphics candidate rejected") {
		t.Fatalf("no early preflight: %v", err)
	}
}
