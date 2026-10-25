package graphicsgo

import "testing"

func TestCFFSourceHorizontalHintsMaskGhostAndLock(t *testing.T) {
	p := CFFHintPlan{Stems: []CFFHintStem{
		{Axis: "x", Start: 100, End: 120}, {Axis: "y", Start: 40, End: 55},
		{Axis: "y", Start: 90, End: 69},   // -21, bottom ghost at 69
		{Axis: "y", Start: 160, End: 140}, // -20, top ghost at 160
		{Axis: "y", Start: 230, End: 220}, // inverted ordinary width, permissive
	}}
	op := CFFHintSourceOperation{Active: []int{0, 1, 2, 3, 4}}
	x, err := ExpandCFFSourceHorizontalHintsExperimental(p, op, 65536, 0, 0, map[int]CFFSourceStemHistoryExperimental{1: {Used: true, BottomDS: 41 * 65536, TopDS: 56 * 65536}})
	if err != nil || len(x) != 4 {
		t.Fatalf("%+v %v", x, err)
	}
	if !x[0].Locked || x[0].Hint.Bottom.DeviceY != 41*65536 || x[0].Hint.Top.DeviceY != 56*65536 {
		t.Fatal("history lock")
	}
	if x[1].Ghost != "bottom" || x[1].Hint.Top != nil || x[1].Hint.Bottom.CharacterY != 69*65536 {
		t.Fatal("bottom ghost")
	}
	if x[2].Ghost != "top" || x[2].Hint.Bottom != nil || x[2].Hint.Top.CharacterY != 160*65536 {
		t.Fatal("top ghost")
	}
	if x[3].Hint.Bottom.CharacterY != 220*65536 || x[3].Hint.Top.CharacterY != 230*65536 {
		t.Fatal("inverted pair")
	}
	// Sparse mask intentionally excludes the hstem and never enables it.
	x, err = ExpandCFFSourceHorizontalHintsExperimental(p, CFFHintSourceOperation{Active: []int{0}}, 65536, 0, 0, nil)
	if err != nil || len(x) != 0 {
		t.Fatal("incorrectly enabled horizontal hints")
	}
	for _, a := range [][]int{{1, 1}, {5}, {-1}} {
		_, err = ExpandCFFSourceHorizontalHintsExperimental(p, CFFHintSourceOperation{Active: a}, 65536, 0, 0, nil)
		if err == nil {
			t.Fatalf("accepted indices %v", a)
		}
	}
}
func TestCFFSourceHorizontalHintsScaleOriginAndDarken(t *testing.T) {
	p := CFFHintPlan{Stems: []CFFHintStem{{Axis: "y", Start: 12.5, End: 14.5}}}
	out, err := ExpandCFFSourceHorizontalHintsExperimental(p, CFFHintSourceOperation{Active: []int{0}}, 32768, 65536, 16384, nil)
	if err != nil || len(out) != 1 {
		t.Fatal(out, err)
	}
	if out[0].Hint.Bottom.CharacterY != int64(13.5*65536) || out[0].Hint.Top.CharacterY != int64(16*65536) {
		t.Fatal(out)
	}
	if out[0].Hint.Top.DeviceY != 8*65536 {
		t.Fatal("nominal device scale")
	}
	p.Stems[0].Start = 1e20
	if _, err := ExpandCFFSourceHorizontalHintsExperimental(p, CFFHintSourceOperation{Active: []int{0}}, 32768, 0, 0, nil); err == nil {
		t.Fatal("accepted oversized input")
	}
}
