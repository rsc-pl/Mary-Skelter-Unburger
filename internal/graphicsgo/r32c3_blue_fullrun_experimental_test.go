package graphicsgo

import (
	"testing"
)

// Unsupported inputs fail closed without changing the original R32C3 research
// branch. The five real fonts are tested only by private SHA-pinned oracles.
func TestR32C3BlueWholeRunRejectsBadInputs(t *testing.T) {
	var absent *SFNT
	if _, err := absent.RasterR32C3LatinMasksBlueExperimental("Tsuu", R32C3FitChoice{}); err == nil {
		t.Fatal("nil font accepted")
	}
	ttf := &SFNT{Outline: TrueTypeOutline}
	if _, err := ttf.RasterR32C3LatinMasksBlueExperimental("Tsuu", R32C3FitChoice{}); err == nil {
		t.Fatal("TTF incorrectly accepts CFF blue hinting")
	}
	_, _, err := ttf.RenderR32C3LatinLabelBlueExperimental("Tsuu", R32C3FitSpec{}, R32C3Warp{}, R32C3CompositeSpec{})
	if err == nil {
		t.Fatal("non-CFF full run accepted")
	}
	var no *SFNT
	_, _, err = no.RenderR32C3LatinLabelBlueExperimental("Tsuu", R32C3FitSpec{}, R32C3Warp{}, R32C3CompositeSpec{})
	if err == nil {
		t.Fatal("nil font full run accepted")
	}
}
