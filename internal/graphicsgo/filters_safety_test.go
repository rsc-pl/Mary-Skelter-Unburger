package graphicsgo

import (
	"bytes"
	"image"
	"math"
	"testing"
)

func TestResizeBlurAlphaSafetyAndSubimage(t *testing.T) {
	base := image.NewAlpha(image.Rect(0, 0, 19, 14))
	for y := 0; y < 14; y++ {
		for x := 0; x < 19; x++ {
			base.Pix[y*base.Stride+x] = uint8((x*73 + y*41 + x*y*13) % 256)
		}
	}
	view := base.SubImage(image.Rect(4, 2, 15, 12)).(*image.Alpha)
	reference := image.NewAlpha(image.Rect(0, 0, 11, 10))
	for y := 0; y < 10; y++ {
		copy(reference.Pix[y*reference.Stride:y*reference.Stride+11], view.Pix[y*view.Stride:y*view.Stride+11])
	}
	before := append([]byte(nil), base.Pix...)
	for _, p := range []struct{ w, h int }{{11, 10}, {3, 17}, {35, 2}, {1, 1}} {
		a, err := ResizeLanczosAlpha(view, p.w, p.h)
		if err != nil {
			t.Fatal(err)
		}
		b, err := ResizeLanczosAlpha(reference, p.w, p.h)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a.Pix, b.Pix) {
			t.Fatalf("subimage resize mismatch %v", p)
		}
	}
	for _, r := range []float64{0, 0.01, 0.8, 1.7, 7.5} {
		a, err := GaussianBlurAlpha(view, r)
		if err != nil {
			t.Fatal(err)
		}
		b, err := GaussianBlurAlpha(reference, r)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a.Pix, b.Pix) {
			t.Fatalf("subimage blur mismatch r=%g", r)
		}
	}
	if !bytes.Equal(base.Pix, before) {
		t.Fatal("mutated source")
	}
	for _, sz := range [][2]int{{0, 10}, {2, 0}, {-1, 3}, {35000, 1}, {10000, 10000}} {
		if _, err := ResizeLanczosAlpha(view, sz[0], sz[1]); err == nil {
			t.Fatalf("accepted invalid resize %v", sz)
		}
	}
	for _, r := range []float64{-1, math.NaN(), math.Inf(1), 150} {
		if _, err := GaussianBlurAlpha(view, r); err == nil {
			t.Fatalf("accepted invalid blur radius %v", r)
		}
	}
	if _, err := ResizeLanczosAlpha(nil, 2, 2); err == nil {
		t.Fatal("accepted nil resize")
	}
	if _, err := GaussianBlurAlpha(nil, 1); err == nil {
		t.Fatal("accepted nil blur")
	}
	bad := &image.Alpha{Pix: []byte{2}, Stride: 2, Rect: image.Rect(0, 0, 3, 4)}
	if _, err := ResizeLanczosAlpha(bad, 2, 2); err == nil {
		t.Fatal("accepted corrupt resize backing")
	}
	if _, err := GaussianBlurAlpha(bad, 1); err == nil {
		t.Fatal("accepted corrupt blur backing")
	}
}
