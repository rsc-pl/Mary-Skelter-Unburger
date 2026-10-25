package graphicsgo

import (
	"image"
	"testing"
)

func TestSquaredDistance1D(t *testing.T) {
	f := []int{10000, 10000, 10000, 0, 10000, 10000, 10000}
	d := make([]int, len(f))
	squaredDistance1D(f, d)
	for i := range f {
		want := (i - 3) * (i - 3)
		if d[i] != want {
			t.Fatalf("distance index %d: %d != %d", i, d[i], want)
		}
	}
}
func TestSubpixelStrokeMasksBoundsAndInput(t *testing.T) {
	a := image.NewAlpha(image.Rect(0, 0, 24, 24))
	a.Pix[12*a.Stride+12] = 255
	orig := append([]byte(nil), a.Pix...)
	inner, outer, err := SubpixelStrokeMasksR32C3Experimental(a, 2, 5, 4)
	if err != nil {
		t.Fatal(err)
	}
	sumI, sumO := 0, 0
	for i := range orig {
		if a.Pix[i] != orig[i] {
			t.Fatal("mutated input")
		}
		if inner.Pix[i] > outer.Pix[i] {
			t.Fatal("outer smaller than inner")
		}
		sumI += int(inner.Pix[i])
		sumO += int(outer.Pix[i])
	}
	if sumI <= 255 || sumO <= sumI {
		t.Fatalf("no increasing stroke areas: %d %d", sumI, sumO)
	}
	if _, _, err = SubpixelStrokeMasksR32C3Experimental(a, 2, 5, 8); err == nil {
		t.Fatal("unsupported factor accepted")
	}
	if _, _, err = SubpixelStrokeMasksR32C3Experimental(a, 8, 5, 4); err == nil {
		t.Fatal("inverted radii accepted")
	}
	if _, _, err = SubpixelStrokeMasksR32C3Experimental(image.NewAlpha(image.Rect(0, 0, 8, 8)), 1, 2, 4); err == nil {
		t.Fatal("empty face accepted")
	}
}
func TestSubpixelStrokeMasksSubimageSafety(t *testing.T) {
	parent := image.NewAlpha(image.Rect(0, 0, 48, 48))
	parent.Pix[22*parent.Stride+22] = 255
	sub := parent.SubImage(image.Rect(10, 10, 34, 34)).(*image.Alpha)
	_, _, err := SubpixelStrokeMasksR32C3Experimental(sub, 2, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSquaredDistance1DBruteForce(t *testing.T) {
	const inf = 1 << 27
	for n := 1; n <= 80; n++ {
		for seed := 0; seed < n; seed++ {
			f := make([]int, n)
			for i := 0; i < n; i++ {
				f[i] = inf
			}
			f[seed] = 0
			if n > 1 && seed%2 == 0 {
				f[n-1] = 0
			}
			d := make([]int, n)
			squaredDistance1D(f, d)
			for x := range d {
				best := inf
				for src, v := range f {
					if v == 0 {
						delta := x - src
						dist := delta * delta
						if dist < best {
							best = dist
						}
					}
				}
				if d[x] != best {
					t.Fatalf("n=%d seed=%d x=%d got=%d want=%d", n, seed, x, d[x], best)
				}
			}
		}
	}
}
