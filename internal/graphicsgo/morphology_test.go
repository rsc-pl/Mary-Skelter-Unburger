package graphicsgo

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"testing"
)

func pixSHA(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func TestPillowMaxFilterOracle(t *testing.T) {
	a := image.NewAlpha(image.Rect(0, 0, 19, 13))
	for y := 0; y < 13; y++ {
		for x := 0; x < 19; x++ {
			a.Pix[y*a.Stride+x] = uint8(((x*53 + y*71 + x*y*13) ^ ((x + 1) * 19)) & 255)
		}
	}
	want := map[int]string{
		3:  "d0552f6a56226de3cde4520c6bafee8a156649da98c5890aaded827364f475ee",
		5:  "63b9187a308b9b7c5965e259ea3a0dfa3bfa8eb1d8a1114a77d7aaf8cc2c6a31",
		11: "13b009fc3473698476b2d56cc94da9137b1a6ce1b9bf2a3d3bf14e26b1b62469",
	}
	for k, hash := range want {
		got, e := MaxFilterAlpha(a, k)
		if e != nil || pixSHA(got.Pix) != hash {
			t.Errorf("MaxFilter %d: %v %s expected %s", k, e, pixSHA(got.Pix), hash)
		}
	}
	if _, e := MaxFilterAlpha(a, 2); e == nil {
		t.Error("even kernel accepted")
	}
	if _, e := MaxFilterAlpha(nil, 3); e == nil {
		t.Error("nil accepted")
	}
	// Non-zero image origin and row stride work without mutating the source.
	sub := image.NewAlpha(image.Rect(10, 10, 35, 30)).SubImage(image.Rect(12, 13, 31, 26)).(*image.Alpha)
	for y := 0; y < 13; y++ {
		copy(sub.Pix[y*sub.Stride:y*sub.Stride+19], a.Pix[y*a.Stride:y*a.Stride+19])
	}
	got, e := MaxFilterAlpha(sub, 5)
	if e != nil || pixSHA(got.Pix) != want[5] {
		t.Errorf("subimage mismatch %v %s", e, pixSHA(got.Pix))
	}
}
func TestPillowRGBAOutlineOracle(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 19, 13))
	for y := 0; y < 13; y++ {
		for x := 0; x < 19; x++ {
			i := y*src.Stride + x*4
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = uint8((x*43+y*17)&255), uint8((x*13+y*27)&255), uint8((x*3+y*101)&255), uint8((x*x+4*y*17)&255)
		}
	}
	want := map[int]string{
		3:  "4c37409fcaa6636b6c6c8ad71ad00baf0439679f10eaf2dc9c4b68668e012ccd",
		5:  "7072496828ead155986adbdbb56af20b89a05a0adeed305b520f03e221659b2b",
		11: "3db6c34dcef020254401c0c8cf1640b82112e9f2d10b64f76123b27959e197dd",
	}
	original := pixSHA(src.Pix)
	for k, hash := range want {
		out, e := AddRGBAOutline(src, color.NRGBA{R: 115, G: 213, B: 17, A: 199}, k)
		if e != nil {
			t.Fatalf("outline %d: %v", k, e)
		}
		if pixSHA(out.Pix) != hash {
			t.Errorf("outline %d: %s != %s", k, pixSHA(out.Pix), hash)
		}
	}
	if pixSHA(src.Pix) != original {
		t.Fatal("mutated input")
	}
	if _, e := AddRGBAOutline(src, color.NRGBA{}, 0); e == nil {
		t.Error("zero radius accepted")
	}
}
