package graphicsgo

import (
	"reflect"
	"testing"
)

func cffTestPlan() CFFHintPlan {
	return CFFHintPlan{
		Private: CFFHintPrivate{BlueValues: []float64{-12, 0, 490, 505}, BlueScale: .039625, BlueShift: 7, BlueFuzz: 1},
		Stems: []CFFHintStem{
			{Axis: "y", Start: -8, End: 0},    // caught by bottom Blue zone
			{Axis: "x", Start: 30, End: 40},   // never enters Y map
			{Axis: "y", Start: 200, End: 220}, // free/non-captured
			{Axis: "y", Start: 492, End: 504}, // caught by top Blue zone
		},
	}
}
func TestInitialMapAllDeclaredAndZeroAnchor(t *testing.T) {
	p := cffTestPlan()
	// An operation with only one active horizontal stem: initial map must
	// still consider every declared horizontal stem, NOT its active mask.
	op := CFFHintSourceOperation{Kind: "line", Active: []int{2}}
	orig := append([]int(nil), op.Active...)
	r, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, true, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.CapturedIndices, []int{0, 3}) || !reflect.DeepEqual(r.PendingIndices, []int{2}) {
		t.Fatalf("priority classification: caught=%v pending=%v", r.CapturedIndices, r.PendingIndices)
	}
	if len(r.Candidates) != 3 || len(r.Map.edges) == 0 || !reflect.DeepEqual(op.Active, orig) {
		t.Fatal("map lost candidates or mutated caller")
	}
	// Initial synthetic (0,0) depends on accepted captured edges crossing
	// zero. For a plan whose only captured stem is above zero, anchor added.
	p.Stems = []CFFHintStem{{Axis: "y", Start: 492, End: 504}}
	z, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, true, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	foundZero := false
	for _, e := range z.Map.edges {
		if e.CharacterY == 0 && e.DeviceY == 0 {
			foundZero = true
		}
	}
	if !foundZero {
		t.Fatal("initial anchor absent")
	}
}
func TestLaterMaskAndStickyPositions(t *testing.T) {
	p := cffTestPlan()
	op := CFFHintSourceOperation{Kind: "curve", Active: []int{0, 1, 2}}
	history := map[int]CFFSourceStemHistoryExperimental{2: {Used: true, BottomDS: 9 * 65536, TopDS: 10 * 65536}}
	r, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, false, 0, history)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.CapturedIndices, []int{0}) || !reflect.DeepEqual(r.ReusedIndices, []int{2}) || len(r.PendingIndices) != 0 {
		t.Fatalf("classification %+v", r)
	}
	for _, e := range r.Map.edges {
		if e.CharacterY == 200*65536 && e.DeviceY != 9*65536 {
			t.Fatalf("locked edge moved %+v", e)
		}
	}
	if !history[2].Used || history[2].BottomDS != 9*65536 {
		t.Fatal("history mutated")
	}
	// When only the free stem is active, it MUST NOT be inserted as a
	// prepositioned/locked hint until the future two-pass adjustment exists.
	noHist, err := BuildCFFPriorityMapExperimental(p, CFFHintSourceOperation{Active: []int{2}}, 2048, 0, false, false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(noHist.Map.edges) != 0 || !reflect.DeepEqual(noHist.PendingIndices, []int{2}) {
		t.Fatalf("unsafe map: %+v", noHist)
	}
}
func TestRejectUnsupportedOrMalformed(t *testing.T) {
	p := cffTestPlan()
	for _, tc := range []struct {
		lang int
		init bool
		hist map[int]CFFSourceStemHistoryExperimental
	}{
		{lang: 1}, {init: true, hist: map[int]CFFSourceStemHistoryExperimental{0: {Used: true}}},
	} {
		if _, e := BuildCFFPriorityMapExperimental(p, CFFHintSourceOperation{}, 2048, 0, false, tc.init, tc.lang, tc.hist); e == nil {
			t.Fatalf("must fail: %+v", tc)
		}
	}
	if _, e := BuildCFFPriorityMapExperimental(p, CFFHintSourceOperation{Active: []int{999}}, 2048, 0, false, false, 0, nil); e == nil {
		t.Fatal("invalid stem id accepted")
	}
}
