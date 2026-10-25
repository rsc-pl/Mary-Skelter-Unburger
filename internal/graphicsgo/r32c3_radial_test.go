package graphicsgo

import (
	"image"
	"math/rand"
	"testing"
)

func NaiveRadial(a *image.Alpha, r int) *image.Alpha {
	w, h := a.Rect.Dx(), a.Rect.Dy()
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var v uint8
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					xx, yy := x+dx, y+dy
					if xx < 0 || xx >= w || yy < 0 || yy >= h || dx*dx+dy*dy > r*r {
						continue
					}
					q := a.AlphaAt(a.Rect.Min.X+xx, a.Rect.Min.Y+yy).A
					if q > v {
						v = q
					}
				}
			}
			out.Pix[y*out.Stride+x] = v
		}
	}
	return out
}
func TestRadialExactGrayscale(t *testing.T) {
	rnd := rand.New(rand.NewSource(9194))
	for _, d := range [][2]int{{1, 1}, {3, 17}, {24, 8}, {45, 37}} {
		for _, r := range []int{0, 1, 2, 3, 5, 11, 21, 48} {
			w, h := d[0], d[1]
			whole := image.NewAlpha(image.Rect(0, 0, w+4, h+4))
			for i := range whole.Pix {
				whole.Pix[i] = uint8(rnd.Intn(256))
			}
			in := whole.SubImage(image.Rect(2, 2, w+2, h+2)).(*image.Alpha)
			got, e := RadialDilateAlphaFast(in, r)
			if e != nil {
				t.Fatalf("dims=%v r=%d: %v", d, r, e)
			}
			want := NaiveRadial(in, r)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if got.AlphaAt(x, y).A != want.AlphaAt(x, y).A {
						t.Fatalf("dims=%v r=%d xy=%d,%d got=%d want=%d", d, r, x, y, got.AlphaAt(x, y).A, want.AlphaAt(x, y).A)
					}
				}
			}
		}
	}
}
func TestRadialLargeRadiusBounded(t *testing.T) {
	a := image.NewAlpha(image.Rect(0, 0, 480, 340))
	a.Pix[150*a.Stride+300] = 230
	got, e := RadialDilateAlphaFast(a, 48)
	if e != nil {
		t.Fatal(e)
	}
	if got.AlphaAt(300, 150).A != 230 || got.AlphaAt(348, 150).A != 230 || got.AlphaAt(349, 150).A != 0 {
		t.Fatal("incorrect 48px circle")
	}
}
