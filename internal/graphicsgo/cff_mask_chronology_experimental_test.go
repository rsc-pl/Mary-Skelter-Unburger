package graphicsgo

import (
	"reflect"
	"testing"
)

// Two counter masks in source order must retain both their independent
// counter groups and the hintmask that controls subsequent drawing. A mask
// encountered in a subroutine is still chronological and must not make an
// implicit contour closure jump to the next drawing mask.
func TestCFFCounterMaskChronologyThroughSubroutine(t *testing.T) {
	sub := append([]byte{20, 0x40}, t2HintCode(0, 0)...)
	sub = append(sub, 5, 11) // cntrmask, zero-length line, return
	refs := &cffData{local: make([][]byte, 108)}
	refs.local[0] = sub
	code := append(t2HintCode(10, 20), 1) // horizontal stem index 0
	code = append(code, t2HintCode(25, 8)...)
	code = append(code, 3)        // vertical stem index 1
	code = append(code, 19, 0x80) // initial drawing mask: stem 0
	code = append(code, t2HintCode(1, 2)...)
	code = append(code, 21)       // move
	code = append(code, 20, 0x80) // counter group 0 (draw mask unchanged)
	code = append(code, t2HintCode(-107)...)
	code = append(code, 10)       // subroutine with different counter group
	code = append(code, 19, 0x40) // new drawing mask: stem 1
	code = append(code, t2HintCode(3, 4)...)
	code = append(code, 5, 14) // line then close
	rec := &cffHintRecorder{}
	b := &cffBuilder{hintRecorder: rec}
	if err := b.execute(code, refs, 0); err != nil {
		t.Fatal(err)
	}
	ops := rec.sourceOperations
	wantKinds := []string{"hintmask", "move", "cntrmask", "cntrmask", "line", "hintmask", "line", "close"}
	kinds := make([]string, 0, len(ops))
	for _, op := range ops {
		kinds = append(kinds, op.Kind)
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("kinds %v", kinds)
	}
	wantActive := [][]int{{0}, {0}, {0}, {0}, {0}, {1}, {1}, {1}}
	for i, op := range ops {
		if !reflect.DeepEqual(op.Active, wantActive[i]) {
			t.Fatalf("event %d %s: active %v", i, op.Kind, op.Active)
		}
	}
	if len(rec.masks) != 4 || rec.masks[0].Counter || !rec.masks[1].Counter || !rec.masks[2].Counter || rec.masks[3].Counter {
		t.Fatalf("counter classification %+v", rec.masks)
	}
	if !reflect.DeepEqual(rec.masks[1].Active, []int{0}) || !reflect.DeepEqual(rec.masks[2].Active, []int{1}) {
		t.Fatalf("counter groups %+v", rec.masks)
	}
	if !ops[4].Degenerate || ops[4].MaskIndex != 0 {
		t.Fatalf("subroutine line under wrong mask %+v", ops[4])
	}
	plain := &cffBuilder{}
	if err := plain.execute(code, refs, 0); err != nil {
		t.Fatal(err)
	}
	b.finish()
	plain.finish()
	if !reflect.DeepEqual(b.paths, plain.paths) {
		t.Fatal("tracing changed unhinted contours")
	}
}
