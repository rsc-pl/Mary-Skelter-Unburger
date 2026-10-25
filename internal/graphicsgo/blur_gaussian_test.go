package graphicsgo

import (
	"crypto/sha256"
	"fmt"
	"image"
	"testing"
)

func TestGaussianBlurAlphaPillowOracles(t *testing.T) {
	cases := []struct {
		w, h int
		r    float64
		want string
	}{
		{7, 5, 0.8, "d697c9b67e966fbdb5b4d08b5b325da1965d25d86b94a234e90b0554adc5dbd1"}, {37, 15, 1.7, "4dd672f0b0a5fbc82679eae1a0bb17c044fcf92d8043874d80ec826e684fb57e"}, {13, 40, 0.1, "bcdb1d1be4936edd7fa9d6ac04305d80bbd8cd97abb8f053b01f8cc0de1daef8"}, {4, 4, 0, "e5ea54ebb783c8acc7bbc8af3ea82621e141dbd7e4f98cf90ebd4b06781b0f82"}, {9, 9, 4.0, "5f68717e343b53c96b84be27ef0b546b1f4b5f6dd2805526cc0c1e0ce2319fd9"}, {11, 2, 1.25, "2365e8396d636ec21e4f570189e13d7d746a76313381b5d2a9336689a90d49b6"}, {192, 76, 2.8, "31f1392453b77123063d2851f1ac22cf35100295e763763c11f644a59c5c7561"}, {1, 10, 1.7, "750e699fd80ff8dcb02a6c9d2530630a06229969eaae61436306c0497abdd78b"}, {10, 1, 10.0, "3c334b67212fb9682abdbbcec004e9ddc68443dfbf10892d1b7b999f82d1b563"},
	}
	for _, tc := range cases {
		src := image.NewAlpha(image.Rect(0, 0, tc.w, tc.h))
		for y := 0; y < tc.h; y++ {
			for x := 0; x < tc.w; x++ {
				src.Pix[y*src.Stride+x] = uint8((x*x*17 + y*y*29 + x*y*11 + 97*x + 31*y) % 256)
			}
		}
		got, err := GaussianBlurAlpha(src, tc.r)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(got.Pix)
		value := fmt.Sprintf("%x", sum)
		t.Logf("%dx%d r%g: %s", tc.w, tc.h, tc.r, value)
		if value != tc.want {
			t.Errorf("Pillow mismatch %s vs %s", value, tc.want)
		}
	}
}
