package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression for all proven Go renderers: the old PNG round-trip could
// premultiply, normalize or lose RGB channels under fully transparent alpha.
func TestFamilyBatchStagesStraightRGBAWithoutColorLoss(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bank.rgba")
	source := []byte{
		211, 90, 174, 0, // alpha=0 but RGB intentionally nonzero
		255, 1, 2, 1,
		52, 150, 214, 127,
		1, 2, 3, 255,
	}
	sha, err := stageFamilyRGBA(p, source, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if sha != digest(source) {
		t.Fatal("RGBA SHA does not cover exact source bytes")
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatal("RGBA bytes changed, including hidden color")
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("intermediate should be private, got %o", st.Mode().Perm())
	}
	if _, err = stageFamilyRGBA(p, source, 2, 2); err == nil {
		t.Fatal("RGBA staging overwrote existing bytes")
	}
	if _, err = stageFamilyRGBA(filepath.Join(t.TempDir(), "bad.rgba"), source[:15], 2, 2); err == nil {
		t.Fatal("invalid RGBA dimensions accepted")
	}
}

func TestFamilyBatchNativeRGBAEncoderCarriesStrictManifestGuards(t *testing.T) {
	f, err := os.ReadFile("family_batch.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(f)
	for _, token := range []string{"RunGDATRGBABatchWithHook", "GDATRGBAEdit", "TargetRGBASHA256", "stageFamilyRGBA"} {
		if !strings.Contains(s, token) {
			t.Errorf("missing native RGBA route: %s", token)
		}
	}
	if strings.Contains(s, "png.Encode") || strings.Contains(s, "RunGDATPNGBatchWithHook") {
		t.Fatal("family batch still routes through PNG")
	}
}
