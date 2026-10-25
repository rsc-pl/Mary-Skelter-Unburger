package graphicsgo

import (
	"reflect"
	"testing"
)

// Hint state must belong to the individual Type2 drawing operations. Applying
// the final mask to the whole outline would corrupt a glyph with mid-path
// mask transitions; cntrmask must not replace the hintmask state.
func TestCFFHintGeometryMaskTransitionsAndCounters(t *testing.T) {
	code := append(t2HintCode(0, 20), 1) // y stem 0
	code = append(code, t2HintCode(50, 15)...)
	code = append(code, 3)        // x stem 1
	code = append(code, 19, 0x80) // first hintmask: stem 0
	code = append(code, t2HintCode(0, 0)...)
	code = append(code, 21) // moveto
	code = append(code, t2HintCode(10, 0)...)
	code = append(code, 5)        // first segment
	code = append(code, 19, 0x40) // switch to stem 1
	code = append(code, t2HintCode(0, 10)...)
	code = append(code, 5)        // second segment
	code = append(code, 20, 0xc0) // counter mask: both
	code = append(code, t2HintCode(-10, 0)...)
	code = append(code, 5) // must still use stem 1
	code = append(code, t2HintCode(0, 5, 2, 2, -2, 1)...)
	code = append(code, 8, 14) // cubic curve, close
	rec := &cffHintRecorder{}
	builder := &cffBuilder{hintRecorder: rec}
	if err := builder.execute(code, &cffData{}, 0); err != nil {
		t.Fatal(err)
	}
	if len(rec.masks) != 3 || len(rec.geometry) != 5 {
		t.Fatalf("masks=%d geometry=%d", len(rec.masks), len(rec.geometry))
	}
	want := [][]int{{0}, {1}, {1}, {1}, {1}}
	for i, seg := range rec.geometry {
		if !reflect.DeepEqual(seg.Active, want[i]) {
			t.Fatalf("segment %d active=%v want=%v", i, seg.Active, want[i])
		}
		if seg.MaskIndex != []int{0, 1, 1, 1, 1}[i] {
			t.Fatalf("segment %d mask index=%d", i, seg.MaskIndex)
		}
		contour := builder.paths[seg.Contour]
		if seg.StartPoint < 0 || seg.StartPoint >= len(contour) || seg.EndPoint < 0 || seg.EndPoint >= len(contour) {
			t.Fatalf("segment %d invalid span %+v", i, seg)
		}
	}
	if !rec.geometry[4].Closing || rec.geometry[3].Operation != "curve" || rec.geometry[3].EndPoint-rec.geometry[3].StartPoint != 16 {
		t.Fatalf("curve/closure: %+v", rec.geometry)
	}
	plain := &cffBuilder{}
	if err := plain.execute(code, &cffData{}, 0); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(builder.paths, plain.paths) {
		t.Fatal("read-only tracing modified glyph geometry")
	}
}

func TestCFFHintGeometryImplicitAndSubroutine(t *testing.T) {
	// Subroutine draws a segment while the mask is still implicit; the main
	// charstring then switches to an explicit empty mask, then draws again.
	sub := append(t2HintCode(10, 0), 5, 11)
	refs := &cffData{local: make([][]byte, 108)}
	refs.local[0] = sub
	code := append(t2HintCode(0, 20), 1)
	code = append(code, t2HintCode(0, 0)...)
	code = append(code, 21)
	code = append(code, t2HintCode(-107)...)
	code = append(code, 10)
	code = append(code, 19, 0x00)
	code = append(code, t2HintCode(0, 10)...)
	code = append(code, 5, 14)
	rec := &cffHintRecorder{}
	c := &cffBuilder{hintRecorder: rec}
	if e := c.execute(code, refs, 0); e != nil {
		t.Fatal(e)
	}
	if len(rec.geometry) != 3 {
		t.Fatalf("got %d segments", len(rec.geometry))
	}
	if rec.geometry[0].MaskIndex != -1 || !reflect.DeepEqual(rec.geometry[0].Active, []int{0}) {
		t.Fatalf("implicit: %+v", rec.geometry[0])
	}
	if rec.geometry[1].MaskIndex != 0 || len(rec.geometry[1].Active) != 0 {
		t.Fatalf("empty mask not honored: %+v", rec.geometry[1])
	}
	if !rec.geometry[2].Closing {
		t.Fatal("missing implicit closing edge")
	}
}
