package graphicsgo

import (
	"image"
	"strings"
	"testing"
)

// Synthetic outlines and pair metrics are independent of private owner fonts.
// This tests native high-res geometry, not FreeType hinting parity.
func TestR32C3UnhintedTextBBoxSyntheticTTFAndCFF(t *testing.T) {
	for _, cff := range []bool{false, true} {
		f, err := ParseSFNT(syntheticFont(cff))
		if err != nil {
			t.Fatal(err)
		}
		bbox, err := f.MeasureLatinTextBBoxUnhinted("A", 100)
		if err != nil || bbox != image.Rect(0, 15, 60, 80) {
			t.Fatalf("cff=%v glyph A: %v %v", cff, bbox, err)
		}
		// AV uses the synthetic horizontal kern value (-25 font units).
		pair, err := f.MeasureLatinTextBBoxUnhinted("AV", 100)
		if err != nil || pair != image.Rect(0, 15, 118, 80) {
			t.Fatalf("cff=%v glyph pair: %v %v", cff, pair, err)
		}
	}
}

func TestR32C3UnhintedTextBBoxFailClosed(t *testing.T) {
	f, err := ParseSFNT(syntheticFont(false))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"", "Ningyohime", "\x00", "\xff", "あ", strings.Repeat("A", 65)} {
		if _, err = f.MeasureLatinTextBBoxUnhinted(s, 136); err == nil {
			t.Fatalf("accepted unsupported run %q", s)
		}
	}
	for _, px := range []int{-1, 0, 1025} {
		if _, err = f.MeasureLatinTextBBoxUnhinted("A", px); err == nil {
			t.Fatalf("accepted pixel size %d", px)
		}
	}
	if _, err = (*SFNT)(nil).MeasureLatinTextBBoxUnhinted("A", 100); err == nil {
		t.Fatal("accepted nil font")
	}
}
