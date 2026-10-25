package graphicsgo

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	"testing"
)

func TestResizeLanczosAlphaPillowOracles(t *testing.T) {
	cases := []struct {
		w, h, dw, dh int
		expected     string
	}{
		{7, 5, 13, 11, "b5d07b07fba30146833e5cd6b5e7184d7536a7a6f184ce6681bc79b33e326253"},
		{37, 15, 8, 8, "d97df873f68c2b24859265eba354ffe787551b122c74aaf12bcb1b2b1e8f0305"},
		{13, 40, 7, 9, "3dfa062c26909338ca991960166c20e8db0845c1a31dec18af805fd548e47daa"},
		{4, 4, 4, 4, "e5ea54ebb783c8acc7bbc8af3ea82621e141dbd7e4f98cf90ebd4b06781b0f82"},
		{9, 9, 1, 1, "5ee0dd4d4840229fab4a86438efbcaf1b9571af94f5ace5acc94de19e98ea9ab"},
		{11, 2, 200, 150, "ee90b90e1de1d06c9107e0c779451613e7dd2bade3fe5a2d7bb88e9475612842"},
		{192, 76, 42, 19, "fccb6720a3e42bdd057f5b07cd5b9c50c277dc4fcb4f9f0f71fa3660796aac9c"},
		{17, 28, 17, 9, "0ed73e4d3a3caf8f81cf361372919ffa0f6b8f441dd87265ed3180406eac166f"},
		{1, 10, 15, 1, "573d38347920e6ef36d35facdf341fd189a966ea5f52756f6eac5ae1d9ac55ea"},
		{32, 33, 32, 33, "2ac7c84ac9a3abcb9519b763e9ba2c07b8e98b3bb69481040a5af9da6eba7690"},
	}
	for _, tc := range cases {
		src := image.NewAlpha(image.Rect(0, 0, tc.w, tc.h))
		for y := 0; y < tc.h; y++ {
			for x := 0; x < tc.w; x++ {
				src.Pix[y*src.Stride+x] = uint8((x*x*17 + y*y*29 + x*y*11 + 97*x + 31*y) % 256)
			}
		}
		out, e := ResizeLanczosAlpha(src, tc.dw, tc.dh)
		if e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256(out.Pix)
		got := hex.EncodeToString(hash[:])
		t.Logf("%dx%d -> %dx%d: %s", tc.w, tc.h, tc.dw, tc.dh, got)
		if tc.expected != got {
			t.Errorf("mismatch: %s != %s", got, tc.expected)
		}
	}
}
