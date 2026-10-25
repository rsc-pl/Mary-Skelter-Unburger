package graphicsgo

import (
	"image"
	"image/color"
	"math/rand"
	"testing"
)

func ReferenceMax(in *image.Alpha, k int) *image.Alpha {
	w, h := in.Rect.Dx(), in.Rect.Dy()
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	r := k / 2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var v uint8
			for yy := max(0, y-r); yy <= min(h-1, y+r); yy++ {
				for xx := max(0, x-r); xx <= min(w-1, x+r); xx++ {
					q := in.AlphaAt(in.Rect.Min.X+xx, in.Rect.Min.Y+yy).A
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

func TestSlidingMaxPillowEquivalent(t *testing.T) {
	rnd := rand.New(rand.NewSource(194))
	for _, dims := range [][2]int{{1, 1}, {2, 7}, {7, 2}, {13, 9}, {31, 17}, {90, 44}} {
		for _, k := range []int{3, 5, 9, 31, 63, 255} {
			w, h := dims[0], dims[1]
			whole := image.NewAlpha(image.Rect(0, 0, w+8, h+8))
			for i := range whole.Pix {
				whole.Pix[i] = uint8(rnd.Intn(256))
			}
			in := whole.SubImage(image.Rect(4, 4, w+4, h+4)).(*image.Alpha)
			got, err := MaxFilterAlphaFast(in, k)
			if err != nil {
				t.Fatalf("dims=%v kernel=%d: %v", dims, k, err)
			}
			want := ReferenceMax(in, k)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if got.AlphaAt(x, y).A != want.AlphaAt(x, y).A {
						t.Fatalf("dims=%v kernel=%d pixel=%d,%d got=%d want=%d", dims, k, x, y, got.AlphaAt(x, y).A, want.AlphaAt(x, y).A)
					}
				}
			}
		}
	}
}

func TestLargeRadiusBounded(t *testing.T) {
	src := image.NewAlpha(image.Rect(0, 0, 2048, 256))
	src.SetAlpha(500, 128, color.Alpha{A: 255})
	got, err := MaxFilterAlphaFast(src, 255)
	if err != nil {
		t.Fatal(err)
	}
	if got.AlphaAt(500, 128).A != 255 || got.AlphaAt(627, 128).A != 255 || got.AlphaAt(628, 128).A != 0 {
		t.Fatal("radius incorrect")
	}
}
