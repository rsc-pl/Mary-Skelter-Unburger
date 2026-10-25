package graphicsgo

import (
	"math"
	"testing"
)

// The two final operands of rcurveline form a line *after* the preceding
// six-operand curves. Skipping a curve can corrupt not only the direct glyph,
// but every glyph calling a shared local/global CFF subroutine.
func TestCFFRCurveLineGeometryAndSubroutines(t *testing.T) {
	encode := func(xs ...int) []byte {
		out := make([]byte, 0, len(xs))
		for _, v := range xs {
			if v < -107 || v > 107 {
				t.Fatalf("test operand range: %d", v)
			}
			out = append(out, byte(v+139))
		}
		return out
	}
	curveAndLine := append(encode(20, 0, 10, 30, 10, 30, 20, 0), byte(24))
	twice := append(encode(20, 0, 10, 30, 10, 30, 10, 0, 10, -30, 20, -30, 20, 0), byte(24))
	cases := []struct {
		name           string
		program        []byte
		local          [][]byte
		expectedEnd    cffPt
		expectedPoints int
	}{
		{"one_curve_then_line", append(append(encode(0, 0), 21), append(curveAndLine, 14)...), nil, cffPt{60, 60}, 18},
		{"two_curves_then_line", append(append(encode(0, 0), 21), append(twice, 14)...), nil, cffPt{100, 0}, 34},
		{"local_subroutine", append(append(encode(0, 0), 21), encode(-107)[0], 10, 14), [][]byte{append(append([]byte{}, curveAndLine...), 11)}, cffPt{60, 60}, 18},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := &cffBuilder{}
			if err := c.execute(tt.program, &cffData{local: tt.local}, 0); err != nil {
				t.Fatal(err)
			}
			if len(c.paths) != 1 {
				t.Fatalf("contours = %d", len(c.paths))
			}
			got := c.paths[0]
			if len(got) != tt.expectedPoints {
				t.Fatalf("points = %d, want %d", len(got), tt.expectedPoints)
			}
			last := got[len(got)-1]
			if math.Abs(last.X-tt.expectedEnd.x) > 1e-6 || math.Abs(last.Y-tt.expectedEnd.y) > 1e-6 {
				t.Fatalf("endpoint (%v,%v), want %+v", last.X, last.Y, tt.expectedEnd)
			}
		})
	}
}
