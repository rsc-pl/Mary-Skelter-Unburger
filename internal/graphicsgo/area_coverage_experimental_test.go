package graphicsgo

import (
	"math"
	"testing"
)

func closedContour(xy ...[2]float64) GlyphContour {
	c := make(GlyphContour, len(xy))
	for i, p := range xy {
		c[i] = GlyphPoint{X: p[0], Y: p[1], On: true}
	}
	return c
}

func TestContinuousAreaSyntheticTriangleHalfPixel(t *testing.T) {
	// Exact right triangle of area 1/2 in one 1x1 pixel.
	shape := GlyphOutline{closedContour([2]float64{0, 0}, [2]float64{1, 0}, [2]float64{0, 1})}
	mask, origin, err := rasterContoursArea(shape, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := mask.AlphaAt(-origin.X, -origin.Y-1).A; got != 128 {
		t.Fatalf("half pixel = %d, want 128", got)
	}
}
func TestContinuousAreaHoleAndSeparatedContours(t *testing.T) {
	outer := closedContour([2]float64{0, 0}, [2]float64{8, 0}, [2]float64{8, 8}, [2]float64{0, 8})
	// Reverse orientation for a cutout that spans exact integer pixel edges.
	inner := closedContour([2]float64{2, 2}, [2]float64{2, 6}, [2]float64{6, 6}, [2]float64{6, 2})
	mask, org, err := rasterContoursArea(GlyphOutline{outer, inner}, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	at := func(x, y int) uint8 { return mask.AlphaAt(x-org.X, -y-org.Y-1).A }
	if got := at(1, 1); got != 255 {
		t.Fatalf("outer alpha %d", got)
	}
	if got := at(3, 3); got != 0 {
		t.Fatalf("hole alpha %d", got)
	}
	if got := at(7, 7); got != 255 {
		t.Fatalf("outside hole alpha %d", got)
	}
}
func TestContinuousAreaRejectInvalidAndDeterminism(t *testing.T) {
	shape := GlyphOutline{closedContour([2]float64{0, 0}, [2]float64{4, 0}, [2]float64{4, 4}, [2]float64{0, 4})}
	if _, _, e := rasterContoursArea(shape, math.NaN(), 10); e == nil {
		t.Fatal("NaN accepted")
	}
	if _, _, e := rasterContoursArea(shape, 10000, 10); e == nil {
		t.Fatal("huge px accepted")
	}
	if _, _, e := rasterContoursArea(GlyphOutline{closedContour([2]float64{math.Inf(1), 0})}, 1, 1); e == nil {
		t.Fatal("infinite coordinate accepted")
	}
	a, _, err := rasterContoursArea(shape, 34, 10)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := rasterContoursArea(shape, 34, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Pix) != len(b.Pix) {
		t.Fatal("size drift")
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			t.Fatalf("nondeterminism byte %d", i)
		}
	}
}
