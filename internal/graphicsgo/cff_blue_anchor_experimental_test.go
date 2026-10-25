package graphicsgo

import (
	"math"
	"testing"
)

// These synthetic geometric checks deliberately use no licensed/private font.
func TestCFFBlueAnchorGeometry(t *testing.T) {
	// Blue baseline is kept fixed; the hinted top is snapped by less than 1 px.
	anchors := []cffSnapAnchor{{original: 0, snapped: 0}, {original: 25.68, snapped: 26}}
	if got := cffGridCoordinate(0, anchors); got != 0 {
		t.Fatalf("baseline drift %v", got)
	}
	if got := cffGridCoordinate(25.68, anchors); math.Abs(got-26) > 1e-8 {
		t.Fatalf("cap y %v", got)
	}
	for i := 1; i <= 25; i++ {
		before := cffGridCoordinate(float64(i-1), anchors)
		after := cffGridCoordinate(float64(i), anchors)
		if after < before {
			t.Fatal("non-monotone blue")
		}
	}
}
func TestCFFBlueAnchorNoOpEmpty(t *testing.T) {
	for _, y := range []float64{-18.7, -0.001, 0, 0.001, 65.4} {
		if cffGridCoordinate(y, nil) != y {
			t.Fatal("blue no-op changed geometry")
		}
	}
}
