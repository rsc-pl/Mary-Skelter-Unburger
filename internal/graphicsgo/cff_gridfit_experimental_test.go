package graphicsgo

import (
	"math"
	"testing"
)

func TestCFFGridFitCoordinates(t *testing.T) {
	a, e := cffStemAnchors([]CFFHintStem{{Axis: "x", Start: 12.25, End: 14.72}}, "x", 1)
	if e != nil {
		t.Fatal(e)
	}
	if len(a) != 2 || a[0].snapped != 12 || a[1].snapped != 14 {
		t.Fatalf("anchors: %+v", a)
	}
	if cffGridCoordinate(12.25, a) != 12 || math.Abs(cffGridCoordinate(14.72, a)-14) > 1e-9 {
		t.Fatalf("stems not snapped: %+v", a)
	}
	for x := 0.0; x < 20; x += 0.125 {
		if math.IsNaN(cffGridCoordinate(x, a)) {
			t.Fatal("NaN")
		}
	}
}
func TestCFFGridFitOverlapsFailClosed(t *testing.T) {
	_, e := cffStemAnchors([]CFFHintStem{{Axis: "x", Start: 5, End: 12}, {Axis: "x", Start: 7, End: 15}}, "x", 1)
	if e == nil {
		t.Fatal("overlapping anchors accepted")
	}
}
func TestCFFGridFitWrongAxisUnaffected(t *testing.T) {
	a, e := cffStemAnchors([]CFFHintStem{{Axis: "y", Start: 1, End: 3}}, "x", 1)
	if e != nil || len(a) != 0 || cffGridCoordinate(2.125, a) != 2.125 {
		t.Fatalf("unexpected %v %v", a, e)
	}
}
func TestCFFGridFitInvalidReject(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), -1, 0, 513} {
		_, e := (&SFNT{}).CFFStemGridFitOutlineExperimental(0, v)
		if e == nil {
			t.Fatal("accepted invalid input", v)
		}
	}
}
