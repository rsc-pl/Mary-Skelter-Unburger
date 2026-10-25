package graphicsgo

import (
	"image"
	"testing"
)

func TestR32C3RadialMaskPaddingAndHole(t *testing.T) {
	a := image.NewAlpha(image.Rect(0, 0, 11, 11))
	a.Pix[5*a.Stride+5] = 255
	before := append([]byte(nil), a.Pix...)
	out, e := radialDilateAlphaR32C3(a, 2)
	if e != nil {
		t.Fatal(e)
	}
	if out.Pix[5*out.Stride+7] != 255 || out.Pix[3*out.Stride+5] != 255 || out.Pix[3*out.Stride+3] != 0 {
		t.Fatalf("radial stroke boundary incorrect")
	}
	for i, v := range before {
		if a.Pix[i] != v {
			t.Fatal("source mutated")
		}
	}
	if _, e = radialDilateAlphaR32C3(a, 129); e == nil {
		t.Fatal("unbounded stroke accepted")
	}
}
func TestR32C3NativeMasksRejectInvalid(t *testing.T) {
	f := &SFNT{}
	if _, e := f.RasterR32C3LatinMasksExperimental("Bad", R32C3FitChoice{}, 1, 1, 4); e == nil {
		t.Fatal("invalid fit accepted")
	}
}

// Rejected whole-run hypothesis is deliberately opt-in and must retain
// fail-closed validation. It must never silently replace the default.
func TestR32C3WholeOutlineTrialRejectsInvalid(t *testing.T) {
	f := &SFNT{}
	if _, e := f.RasterR32C3LatinMasksWholeOutlineExperimental("Bad", R32C3FitChoice{}, 1, 1); e == nil {
		t.Fatal("whole-run trial accepted an invalid font/fit")
	}
}
