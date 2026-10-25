package graphicsgo

import (
	"math"
	"reflect"
	"testing"
)

func t2HintCode(values ...int) []byte {
	out := []byte{}
	for _, n := range values {
		out = append(out, cffTestNumber(n)...)
	}
	return out
}
func TestCFFHintStemMaskRealWidths(t *testing.T) {
	// hstem y=[10,30], implicit vstem x=[50,65], mask activates both.
	code := append(t2HintCode(10, 20), 1)
	code = append(code, t2HintCode(50, 15)...)
	code = append(code, 19, 0xc0)
	code = append(code, t2HintCode(0, 0)...)
	code = append(code, 21, 14)
	rec := &cffHintRecorder{}
	c := &cffBuilder{hintRecorder: rec}
	if err := c.execute(code, &cffData{}, 0); err != nil {
		t.Fatal(err)
	}
	want := []CFFHintStem{{Axis: "y", Start: 10, End: 30}, {Axis: "x", Start: 50, End: 65}}
	if !reflect.DeepEqual(rec.stemsOut, want) {
		t.Fatalf("stems: %+v", rec.stemsOut)
	}
	if len(rec.masks) != 1 || !reflect.DeepEqual(rec.masks[0].Active, []int{0, 1}) {
		t.Fatalf("mask: %+v", rec.masks)
	}
	plain := &cffBuilder{}
	if err := plain.execute(code, &cffData{}, 0); err != nil {
		t.Fatal(err)
	}
	c.finish()
	plain.finish()
	if !reflect.DeepEqual(c.paths, plain.paths) {
		t.Fatal("hint extraction changed original unhinted paths")
	}
}
func TestCFFHintMaskRejectsUnusedBits(t *testing.T) {
	code := append(t2HintCode(10, 20), 1)
	code = append(code, 19, 0x01, 14)
	c := &cffBuilder{hintRecorder: &cffHintRecorder{}}
	if err := c.execute(code, &cffData{}, 0); err == nil {
		t.Fatal("expected reject masked unused bits")
	}
}
func TestCFFHintRecorderSubroutineAndNegativeWidth(t *testing.T) {
	sub := append(t2HintCode(100, -10), 3, 11) // negative Type2 stem widths are legal
	refs := &cffData{local: make([][]byte, 108)}
	refs.local[0] = sub
	code := append(t2HintCode(-107), 10) // biased index zero
	code = append(code, 19, 0x80, 14)
	c := &cffBuilder{hintRecorder: &cffHintRecorder{}}
	if err := c.execute(code, refs, 0); err != nil {
		t.Fatal(err)
	}
	got := c.hintRecorder.stemsOut
	if len(got) != 1 || got[0].Axis != "x" || got[0].Start != 100 || got[0].End != 90 {
		t.Fatalf("negative stem: %+v", got)
	}
	if len(c.hintRecorder.masks) != 1 || len(c.hintRecorder.masks[0].Active) != 1 {
		t.Fatal("subroutine mask missing")
	}
}
func TestCFFPrivateHintBCDBlueZonesAndStemSnap(t *testing.T) {
	// private DICT: BlueValues [0,0,522,522] as deltas; BlueScale BCD 0.039625;
	// StemSnapH delta [20,19,16] => absolute [20,39,55].
	blob := append(t2HintCode(0, 0, 522, 0), 6)
	blob = append(blob, 30, 0x0a, 0x03, 0x96, 0x25, 0xff, 12, 9)
	blob = append(blob, t2HintCode(20, 19, 16)...)
	blob = append(blob, 12, 12)
	p, err := cffPrivateHintInfo(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.BlueValues, []float64{0, 0, 522, 522}) {
		t.Fatalf("blue values: %v", p.BlueValues)
	}
	if math.Abs(p.BlueScale-0.039625) > 1e-9 {
		t.Fatalf("BlueScale: %v", p.BlueScale)
	}
	if !reflect.DeepEqual(p.StemSnapH, []float64{20, 39, 55}) {
		t.Fatalf("StemSnap: %v", p.StemSnapH)
	}
}
func TestCFFPrivateHintBCDRejectsMalformed(t *testing.T) {
	for _, blob := range [][]byte{
		{30, 0x0a}, {30, 0x0d, 0xff, 12, 9}, {139, 6},
	} {
		if _, err := cffPrivateHintInfo(blob); err == nil {
			t.Fatalf("must reject malformed dict %x", blob)
		}
	}
}
