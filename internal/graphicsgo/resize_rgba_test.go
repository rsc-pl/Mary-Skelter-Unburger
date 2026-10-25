package graphicsgo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"strings"
	"testing"
)

type rgbaOracle struct {
	Name string `json:"name"`
	SHA  string `json:"sha256"`
}

func rgbaInput(w, h, seed int) *image.NRGBA {
	im := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*im.Stride + 4*x
			a := []int{0, 255, 1, 7, 128, 254, 63, (x*11 + y*17 + seed*5) % 256}[(x+3*y+seed)%8]
			im.Pix[i] = byte((x*17 + y*19 + seed*31 + (x*y)%17) % 256)
			im.Pix[i+1] = byte((x*61 + y*3 + seed*47) % 256)
			im.Pix[i+2] = byte((x*7 + y*77 + seed*9) % 256)
			im.Pix[i+3] = byte(a)
		}
	}
	return im
}
func TestResizeRGBAPillowOracles(t *testing.T) {
	data, err := os.ReadFile(privateFixturePath(t, "oracle-resize"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []rgbaOracle
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 24 {
		t.Fatalf("incomplete oracle: %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var typ string
			var w, h, dw, dh, seed int
			if _, err := fmt.Sscanf(tc.Name, "%[^/]/%dx%d_%dx%d_%d", &typ, &w, &h, &dw, &dh, &seed); err != nil { // Go scanf does not support scansets
				parts := strings.SplitN(tc.Name, "/", 2)
				typ = parts[0]
				if len(parts) != 2 {
					t.Fatalf("bad fixture: %s", tc.Name)
				}
				if _, err := fmt.Sscanf(parts[1], "%dx%d_%dx%d_%d", &w, &h, &dw, &dh, &seed); err != nil {
					t.Fatal(err)
				}
			}
			src := rgbaInput(w, h, seed)
			orig := append([]byte(nil), src.Pix...)
			var got *image.NRGBA
			if typ == "LANCZOS" {
				got, err = ResizeLanczosNRGBA(src, dw, dh)
			} else {
				got, err = ResizeBicubicNRGBA(src, dw, dh)
			}
			if err != nil {
				t.Fatal(err)
			}
			s := sha256.Sum256(got.Pix)
			if hex.EncodeToString(s[:]) != tc.SHA {
				t.Fatalf("Pillow expected %s, Go %x", tc.SHA, s)
			}
			if string(orig) != string(src.Pix) {
				t.Fatal("mutated input")
			}
		})
	}
}

// Prevent silent geometry corruption, writes outside the source's rectangle,
// and unexpected changes to private cached pixels when handed a subimage.
func TestResizeRGBASafetyAndSubimage(t *testing.T) {
	for _, method := range []func(*image.NRGBA, int, int) (*image.NRGBA, error){ResizeLanczosNRGBA, ResizeBicubicNRGBA} {
		for _, bad := range []*image.NRGBA{nil, {}, {Pix: []byte{1, 2, 3}, Stride: 4, Rect: image.Rect(0, 0, 2, 2)}, {Pix: make([]byte, 8), Stride: 2, Rect: image.Rect(0, 0, 2, 1)}} {
			if _, err := method(bad, 2, 2); err == nil {
				t.Fatal("invalid image accepted")
			}
		}
		good := rgbaInput(5, 9, 14)
		for _, size := range [][2]int{{0, 2}, {3, 0}, {32769, 1}, {1, 32769}, {32768, 32768}} {
			if _, err := method(good, size[0], size[1]); err == nil {
				t.Fatal("invalid target accepted", size)
			}
		}
		pad := image.NewNRGBA(image.Rect(-13, -7, 11, 16))
		for y := 0; y < pad.Rect.Dy(); y++ {
			for x := 0; x < pad.Rect.Dx(); x++ {
				n := y*pad.Stride + 4*x
				pad.Pix[n] = byte((x*11 + y*13) % 256)
				pad.Pix[n+1] = byte((x*3 + y*17) % 256)
				pad.Pix[n+2] = byte((x*5 + y*7) % 256)
				pad.Pix[n+3] = byte((x*19 + y*23) % 256)
			}
		}
		snapshot := append([]byte(nil), pad.Pix...)
		sub := pad.SubImage(image.Rect(-7, -2, 3, 8)).(*image.NRGBA)
		plain := image.NewNRGBA(image.Rect(0, 0, 10, 10))
		for y := 0; y < 10; y++ {
			copy(plain.Pix[y*plain.Stride:y*plain.Stride+40], sub.Pix[y*sub.Stride:y*sub.Stride+40])
		}
		a, err := method(sub, 8, 11)
		if err != nil {
			t.Fatal(err)
		}
		b, err := method(plain, 8, 11)
		if err != nil {
			t.Fatal(err)
		}
		if string(a.Pix) != string(b.Pix) {
			t.Fatal("subimage mismatch")
		}
		if string(snapshot) != string(pad.Pix) {
			t.Fatal("source image mutated")
		}
	}
}
