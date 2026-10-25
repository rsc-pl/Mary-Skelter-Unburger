package graphicsgo

import (
	"math"
	"reflect"
	"testing"
)

func testCFFBlueInput() CFFHintPrivate {
	return CFFHintPrivate{BlueValues: []float64{-20, 0, 690, 710}, OtherBlues: []float64{-210, -190}, BlueScale: 0.039625, BlueShift: 7, BlueFuzz: 1}
}
func mkCFFBlueHint(bottom, top int64, ghost string) CFFSourceHorizontalHintExperimental {
	h := CFFSourceHorizontalHintExperimental{Ghost: ghost}
	if ghost != "top" {
		h.Hint.Bottom = &CFFHintEdgeFixedExperimental{CharacterY: bottom, DeviceY: bottom * 65536 / 65536, Scale: 65536}
	}
	if ghost != "bottom" {
		h.Hint.Top = &CFFHintEdgeFixedExperimental{CharacterY: top, DeviceY: top * 65536 / 65536, Scale: 65536}
	}
	return h
}
func TestCFFBlueCaptureSynthetic(t *testing.T) {
	p := testCFFBlueInput()
	// The source blueScale BCD decimal is deliberately non-exact in 16.16.
	state, err := PrepareCFFBlueZonesFixedExperimental(p, 5243, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if state.SuppressOvershoot {
		t.Fatal("unexpected overshoot suppression")
	}
	if len(state.Zones) != 3 {
		t.Fatal("zone count", len(state.Zones))
	}
	if !state.Zones[0].BottomZone || state.Zones[1].BottomZone || !state.Zones[2].BottomZone {
		t.Fatal("zone classes")
	}
	// Edge lies inside the first bottom zone, 10 font units below its flat edge:
	// require one full pixel overshoot; shift both edges equally.
	input := mkCFFBlueHint(-10*65536, 10*65536, "")
	input.Hint.Bottom.DeviceY = -52430
	input.Hint.Top.DeviceY = 52430
	saved := *input.Hint.Bottom
	got, captured, err := CaptureCFFBlueStemFixedExperimental(state, input)
	if err != nil || !captured {
		t.Fatalf("capture: %v %v", captured, err)
	}
	if got.Hint.Bottom.DeviceY != -65536 {
		t.Fatalf("bottom position: %d", got.Hint.Bottom.DeviceY)
	}
	if got.Hint.Top.DeviceY-got.Hint.Bottom.DeviceY != input.Hint.Top.DeviceY-input.Hint.Bottom.DeviceY {
		t.Fatal("pair width changed")
	}
	if !reflect.DeepEqual(*input.Hint.Bottom, saved) {
		t.Fatal("source mutated")
	}
	// Noncaptured and already locked cases should have stable output.
	off := mkCFFBlueHint(200*65536, 210*65536, "")
	_, cap2, e := CaptureCFFBlueStemFixedExperimental(state, off)
	if e != nil || cap2 {
		t.Fatal("unwanted capture", e)
	}
	off.Locked = true
	_, cap3, e := CaptureCFFBlueStemFixedExperimental(state, off)
	if e != nil || cap3 {
		t.Fatal("locked capture", e)
	}
	// Ghost top is never used to capture a bottom zone.
	ghost := mkCFFBlueHint(0, 0, "top")
	ghost.Hint.Top.CharacterY = -10 * 65536
	_, cap4, e := CaptureCFFBlueStemFixedExperimental(state, ghost)
	if e != nil || cap4 {
		t.Fatal("ghost edge side", e)
	}
}
func TestCFFBlueCaptureOvershootAndFuzz(t *testing.T) {
	p := testCFFBlueInput()
	state, err := PrepareCFFBlueZonesFixedExperimental(p, 1311, 0, false) // tiny size
	if err != nil {
		t.Fatal(err)
	}
	if !state.SuppressOvershoot || state.Boost <= 0 {
		t.Fatalf("missing small-size boost %+v", state)
	}
	h := mkCFFBlueHint(-10*65536, 1*65536, "")
	h.Hint.Bottom.DeviceY = -13110
	h.Hint.Top.DeviceY = 1311
	caph, yes, e := CaptureCFFBlueStemFixedExperimental(state, h)
	if e != nil || !yes {
		t.Fatal("suppressed capture", e)
	}
	if caph.Hint.Bottom.DeviceY != state.Zones[0].FlatDS {
		t.Fatal("overshoot must land on flat zone")
	}
	state2, err := PrepareCFFBlueZonesFixedExperimental(p, 1311, 0, true)
	if err != nil || state2.Boost != 0 {
		t.Fatal("darkening must suppress boost", err)
	}
	// BlueFuzz catches source coordinates immediately outside a zone.
	h.Hint.Bottom.CharacterY = 65536 / 2
	_, yes, e = CaptureCFFBlueStemFixedExperimental(state, h)
	if e != nil || !yes {
		t.Fatal("BlueFuzz edge case", e)
	}
}
func TestCFFBlueCaptureRejectInvalid(t *testing.T) {
	base := testCFFBlueInput()
	bad := base
	bad.BlueValues = []float64{0, 1, 2}
	if _, e := PrepareCFFBlueZonesFixedExperimental(bad, 65536, 0, false); e == nil {
		t.Fatal("odd arrays must fail")
	}
	bad = base
	bad.BlueScale = math.NaN()
	if _, e := PrepareCFFBlueZonesFixedExperimental(bad, 65536, 0, false); e == nil {
		t.Fatal("NaN must fail")
	}
	bad = base
	bad.BlueFuzz = -1
	if _, e := PrepareCFFBlueZonesFixedExperimental(bad, 65536, 0, false); e == nil {
		t.Fatal("negative fuzz must fail")
	}
	state, _ := PrepareCFFBlueZonesFixedExperimental(base, 65536, 0, false)
	if _, _, e := CaptureCFFBlueStemFixedExperimental(state, CFFSourceHorizontalHintExperimental{}); e == nil {
		t.Fatal("no edges must fail")
	}
}
