package graphicsgo

import (
	"math"
	"testing"
)

func TestNonzeroExactOverlap(t *testing.T) {
	rect := func(x0, y0, x1, y1 float64) GlyphContour {
		return closedContour([2]float64{x0, y0}, [2]float64{x1, y0}, [2]float64{x1, y1}, [2]float64{x0, y1})
	}
	reverse := func(c GlyphContour) GlyphContour {
		o := make(GlyphContour, len(c))
		for i := range c {
			o[i] = c[len(c)-1-i]
		}
		return o
	}
	at := func(g GlyphOutline, x, y int) uint8 {
		t.Helper()
		m, o, e := rasterContoursNonzeroArea(g, 1, 1)
		if e != nil {
			t.Fatal(e)
		}
		return m.AlphaAt(x-o.X, -y-o.Y-1).A
	}
	outer := rect(0, 0, 4, 4)
	if v := at(GlyphOutline{outer, outer}, 1, 1); v != 255 {
		t.Fatalf("same-winding overlap %d", v)
	}
	if v := at(GlyphOutline{outer, reverse(outer)}, 1, 1); v != 0 {
		t.Fatalf("opposite overlap %d", v)
	}
	if v := at(GlyphOutline{outer, rect(2, 0, 6, 4)}, 3, 1); v != 255 {
		t.Fatalf("overlap region %d", v)
	}
	if v := at(GlyphOutline{outer, reverse(rect(2, 0, 6, 4))}, 3, 1); v != 0 {
		t.Fatalf("cancel region %d", v)
	}
	if v := at(GlyphOutline{outer, reverse(rect(2, 0, 6, 4))}, 1, 1); v != 255 {
		t.Fatalf("uncovered region %d", v)
	}
	// Cross-cell intersection of crossing triangles must be resolved at the real y.
	a := closedContour([2]float64{0, 0}, [2]float64{2, 0}, [2]float64{0, 2})
	b := closedContour([2]float64{2, 2}, [2]float64{2, 0}, [2]float64{0, 2})
	if v := at(GlyphOutline{a, b}, 0, 0); v < 254 {
		t.Fatalf("triangles union %d", v)
	}
}
func TestNonzeroFractionalAndGuard(t *testing.T) {
	g := GlyphOutline{closedContour([2]float64{0, 0}, [2]float64{1, 0}, [2]float64{0, 1})}
	m, o, e := rasterContoursNonzeroArea(g, 1, 1)
	if e != nil {
		t.Fatal(e)
	}
	if v := m.AlphaAt(-o.X, -o.Y-1).A; v != 128 {
		t.Fatalf("half coverage %d", v)
	}
	if _, _, e := rasterContoursNonzeroArea(g, math.NaN(), 1); e == nil {
		t.Fatal("NaN accepted")
	}
	if _, _, e := rasterContoursNonzeroArea(GlyphOutline{closedContour([2]float64{math.Inf(1), 0})}, 34, 1); e == nil {
		t.Fatal("Inf accepted")
	}
}

// Analytic one-pixel overlap: A=[0,.8], B=[.2,1]. The union fills
// exactly 1 px; reversing B cancels their shared 0.6-pixel region.
func TestNonzeroFractionalOverlappingRects(t *testing.T) {
	rect := func(x0, x1 float64) GlyphContour {
		return closedContour([2]float64{x0, 0}, [2]float64{x1, 0}, [2]float64{x1, 1}, [2]float64{x0, 1})
	}
	a, b := rect(0, .8), rect(.2, 1)
	rev := make(GlyphContour, len(b))
	for i := range b {
		rev[i] = b[len(b)-i-1]
	}
	for _, tc := range []struct {
		outline GlyphOutline
		want    uint8
	}{{GlyphOutline{a, b}, 255}, {GlyphOutline{a, rev}, 102}} {
		m, o, e := rasterContoursNonzeroArea(tc.outline, 1, 1)
		if e != nil {
			t.Fatal(e)
		}
		if got := m.AlphaAt(-o.X, -o.Y-1).A; got != tc.want {
			t.Fatalf("fractional nonzero area: got %d want %d", got, tc.want)
		}
	}
}
