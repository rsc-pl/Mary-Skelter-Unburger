package graphicsgo

import (
	"reflect"
	"testing"
)

// The unflattened cubic and hint events form a source chronology. 's
// flattened metadata is retained for cross-checking, not used to derive them.
func TestCFFSourceOperationChronologyAndCubicControls(t *testing.T) {
	code := append(t2HintCode(0, 20), 1) // hstem 0
	code = append(code, t2HintCode(50, 15)...)
	code = append(code, 3) // vstem 1
	code = append(code, 19, 0x80)
	code = append(code, t2HintCode(5, 7)...)
	code = append(code, 21) // initial (5,7)
	code = append(code, t2HintCode(0, 0)...)
	code = append(code, 5) // zero-length line MUST survive
	code = append(code, t2HintCode(3, 4, 5, 6, 7, 8)...)
	code = append(code, 8)        // cubic: (5,7) (8,11) (13,17) (20,25)
	code = append(code, 20, 0xc0) // counter mask; drawing mask stays stem 0
	code = append(code, 19, 0x40) // second mask: stem 1
	code = append(code, t2HintCode(0, 0)...)
	code = append(code, 5) // zero-length under second mask
	code = append(code, t2HintCode(-15, -18)...)
	code = append(code, 5, 14) // end coincident with start, close still recorded
	rec := &cffHintRecorder{}
	builder := &cffBuilder{hintRecorder: rec}
	if err := builder.execute(code, &cffData{}, 0); err != nil {
		t.Fatal(err)
	}
	ops := rec.sourceOperations
	kinds := make([]string, 0, len(ops))
	for _, op := range ops {
		kinds = append(kinds, op.Kind)
	}
	want := []string{"hintmask", "move", "line", "curve", "cntrmask", "hintmask", "line", "line", "close"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds=%v want=%v", kinds, want)
	}
	cubic := ops[3]
	points := []CFFHintSourcePoint{{5, 7}, {8, 11}, {13, 17}, {20, 25}}
	if !reflect.DeepEqual(cubic.Points, points) || cubic.GeometryIndex != 1 || cubic.MaskIndex != 0 || !reflect.DeepEqual(cubic.Active, []int{0}) {
		t.Fatalf("source cubic=%+v", cubic)
	}
	if !ops[2].Degenerate || !ops[6].Degenerate || !ops[8].Degenerate {
		t.Fatalf("zero line/closing lost: %+v", ops)
	}
	if ops[8].GeometryIndex != -1 {
		t.Fatal("degenerate closing generated a spurious segment")
	}
	if ops[4].MaskEventIndex != 1 || ops[4].MaskIndex != 0 || !reflect.DeepEqual(ops[4].Active, []int{0}) {
		t.Fatalf("counter switched drawing state: %+v", ops[4])
	}
	if ops[5].MaskEventIndex != 2 || ops[5].MaskIndex != 2 || !reflect.DeepEqual(ops[5].Active, []int{1}) {
		t.Fatalf("new hintmask not recorded: %+v", ops[5])
	}
	if len(rec.geometry) != 4 {
		t.Fatalf("geometry=%d", len(rec.geometry))
	}
	if ops[2].GeometryIndex != 0 || ops[6].GeometryIndex != 2 || ops[7].GeometryIndex != 3 {
		t.Fatalf("source→index mismatch: %+v", ops)
	}
	plain := &cffBuilder{}
	if err := plain.execute(code, &cffData{}, 0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(builder.paths, plain.paths) {
		t.Fatal("trace changed legacy unhinted glyph outline")
	}
}

func TestCFFSourceOperationSubroutineImplicitAndOpenContour(t *testing.T) {
	sub := append(t2HintCode(3, 0, 4, 0, 2, 5), 8, 11) // cubic in subroutine
	refs := &cffData{local: make([][]byte, 108)}
	refs.local[0] = sub
	code := append(t2HintCode(0, 20), 1)
	code = append(code, t2HintCode(0, 0)...)
	code = append(code, 21) // implicit active stem 0
	code = append(code, t2HintCode(-107)...)
	code = append(code, 10)    // subroutine cubic
	code = append(code, 19, 0) // explicit empty mask
	code = append(code, t2HintCode(1, 2)...)
	code = append(code, 21) // closes previous contour then starts second
	code = append(code, 14)
	rec := &cffHintRecorder{}
	c := &cffBuilder{hintRecorder: rec}
	if err := c.execute(code, refs, 0); err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, op := range rec.sourceOperations {
		kinds = append(kinds, op.Kind)
	}
	want := []string{"move", "curve", "hintmask", "close", "move", "close"}
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("got %v want %v", kinds, want)
	}
	if rec.sourceOperations[1].MaskIndex != -1 || !reflect.DeepEqual(rec.sourceOperations[1].Active, []int{0}) {
		t.Fatal("subroutine implicit mask lost")
	}
	if len(rec.sourceOperations[1].Points) != 4 {
		t.Fatal("source cubic lost within subroutine")
	}
	if rec.sourceOperations[4].Contour != 1 || rec.sourceOperations[4].MaskIndex != 0 || len(rec.sourceOperations[4].Active) != 0 {
		t.Fatal("empty mask not propagated to next contour")
	}
	if rec.sourceOperations[5].GeometryIndex != -1 || !rec.sourceOperations[5].Degenerate {
		t.Fatal("moveto-only contour close lost")
	}
}
