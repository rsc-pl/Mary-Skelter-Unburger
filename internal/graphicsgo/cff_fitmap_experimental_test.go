package graphicsgo

import (
	"reflect"
	"testing"
)

func Plan(stems ...CFFHintStem) CFFHintPlan {
	return CFFHintPlan{Private: CFFHintPrivate{BlueScale: .039625, BlueShift: 7, BlueFuzz: 1}, Stems: stems}
}
func TestFittedStemFirstUseAndHistory(t *testing.T) {
	p := Plan(CFFHintStem{Axis: "y", Start: 100, End: 110})
	op := CFFHintSourceOperation{Kind: "line", Active: []int{0}}
	init, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, true, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := BuildCFFFittedMapExperimental(init, 2048, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.History) != 0 {
		t.Fatalf("initial committed history: %+v", first.History)
	}
	priority, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := BuildCFFFittedMapExperimental(priority, 2048, first.Map, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Accepted, []int{0}) || len(result.Edges) != 2 {
		t.Fatalf("missing accepted edges: %+v", result)
	}
	if result.Edges[0].DeviceY != 3*65536 {
		t.Fatalf("bottom not aligned: %+v", result.Edges)
	}
	hist := result.History[0]
	if !hist.Used || hist.BottomDS != result.Edges[0].DeviceY || hist.TopDS != result.Edges[1].DeviceY {
		t.Fatalf("first-use history missing %+v", hist)
	}
	locked, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, false, 0, result.History)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildCFFFittedMapExperimental(locked, 2048, first.Map, result.History)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.Accepted, []int{0}) || second.Edges[0].DeviceY != hist.BottomDS || second.Edges[1].DeviceY != hist.TopDS {
		t.Fatal("previous hint position not reused")
	}
}
func TestRejectTouchingAndNoPhantomHistory(t *testing.T) {
	p := Plan(CFFHintStem{Axis: "y", Start: 100, End: 110}, CFFHintStem{Axis: "y", Start: 110, End: 120})
	op := CFFHintSourceOperation{Active: []int{0, 1}}
	initial, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, true, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	im, err := BuildCFFFittedMapExperimental(initial, 2048, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	priority, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := BuildCFFFittedMapExperimental(priority, 2048, im.Map, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Accepted) != 1 || len(got.Rejected) != 1 || len(got.History) != 1 {
		t.Fatalf("phantom history on rejected stem: %+v", got)
	}
}
func TestNegativeFractionAndDeferredTopDown(t *testing.T) {
	edges := []CFFFittedEdgeExperimental{
		{CFFHintEdgeFixedExperimental: CFFHintEdgeFixedExperimental{CharacterY: -9 * 65536, DeviceY: -2*65536 + 55000, Scale: 16384}, StemIndex: 0, PairBottom: true},
		{CFFHintEdgeFixedExperimental: CFFHintEdgeFixedExperimental{CharacterY: -8 * 65536, DeviceY: -2*65536 + 57000, Scale: 16384}, StemIndex: 0, PairTop: true},
		{CFFHintEdgeFixedExperimental: CFFHintEdgeFixedExperimental{CharacterY: 0, DeviceY: 0, Scale: 16384}, StemIndex: -1, Locked: true, Synthetic: true},
	}
	if e := cffAdjustFittedEdgesExperimental(edges); e != nil {
		t.Fatal(e)
	}
	for i := 1; i < len(edges); i++ {
		if edges[i-1].DeviceY > edges[i].DeviceY {
			t.Fatal("negative edge ordering")
		}
	}
	if edges[0].DeviceY-edges[1].DeviceY != -2000 {
		t.Fatal("stroke thickness changed")
	}
}
func TestNoSourceMutationAndFailClosed(t *testing.T) {
	p := Plan(CFFHintStem{Axis: "y", Start: 100, End: 120})
	op := CFFHintSourceOperation{Active: []int{0}}
	prior, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, false, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	history := map[int]CFFSourceStemHistoryExperimental{999: {Used: true, BottomDS: 7}}
	before := *prior.Candidates[0].Hint.Bottom
	if _, e := BuildCFFFittedMapExperimental(prior, 2048, nil, history); e == nil {
		t.Fatal("noninitial without initial map accepted")
	}
	init, err := BuildCFFPriorityMapExperimental(p, op, 2048, 0, false, true, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	im, err := BuildCFFFittedMapExperimental(init, 2048, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = BuildCFFFittedMapExperimental(prior, 2048, im.Map, history)
	if err != nil {
		t.Fatal(err)
	}
	if *prior.Candidates[0].Hint.Bottom != before || !history[999].Used || len(history) != 1 {
		t.Fatal("source mutation")
	}
}
