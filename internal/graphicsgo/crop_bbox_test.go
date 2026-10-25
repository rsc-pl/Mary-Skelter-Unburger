package graphicsgo

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"testing"
)

type cropCase struct {
	Name, Mode string
	W, H       int
	Src        string
	Rect       [4]int
	BBox       *[4]int
	CW, CH     int
	SHA256     string
}

func TestPillow123BBoxCropOracle(t *testing.T) {
	b, e := os.ReadFile(privateFixturePath(t, "oracle-crop"))
	if e != nil {
		t.Fatal(e)
	}
	var doc struct {
		Schema        int
		PillowVersion string `json:"pillow_version"`
		Cases         []cropCase
	}
	if e = json.Unmarshal(b, &doc); e != nil {
		t.Fatal(e)
	}
	if doc.Schema != 1 || doc.PillowVersion != "12.3.0" || len(doc.Cases) != 96 {
		t.Fatalf("oracle meta: %d %s %d", doc.Schema, doc.PillowVersion, len(doc.Cases))
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			src, e := base64.StdEncoding.DecodeString(c.Src)
			if e != nil {
				t.Fatal(e)
			}
			r := image.Rect(c.Rect[0], c.Rect[1], c.Rect[2], c.Rect[3])
			var bounds image.Rectangle
			var ok bool
			var out []byte
			switch c.Mode {
			case "L":
				im := image.NewAlpha(image.Rect(0, 0, c.W, c.H))
				copy(im.Pix, src)
				bounds, ok, e = BBoxAlpha(im)
				if e != nil {
					t.Fatal(e)
				}
				crop, e := CropAlphaPillow(im, r)
				if e != nil {
					t.Fatal(e)
				}
				if crop.Rect.Dx() != c.CW || crop.Rect.Dy() != c.CH {
					t.Fatal("crop dimensions")
				}
				out = crop.Pix
			case "RGBA":
				im := image.NewNRGBA(image.Rect(0, 0, c.W, c.H))
				copy(im.Pix, src)
				bounds, ok, e = BBoxNRGBA(im)
				if e != nil {
					t.Fatal(e)
				}
				crop, e := CropNRGBAPillow(im, r)
				if e != nil {
					t.Fatal(e)
				}
				if crop.Rect.Dx() != c.CW || crop.Rect.Dy() != c.CH {
					t.Fatal("crop dimensions")
				}
				out = crop.Pix
			default:
				t.Fatal("mode")
			}
			if (c.BBox != nil) != ok {
				t.Fatalf("bbox presence: %v", ok)
			}
			if c.BBox != nil && bounds != image.Rect(c.BBox[0], c.BBox[1], c.BBox[2], c.BBox[3]) {
				t.Fatalf("bbox: %v want %v", bounds, c.BBox)
			}
			s := sha256.Sum256(out)
			if hex.EncodeToString(s[:]) != c.SHA256 {
				t.Fatalf("crop hash %x want %s", s, c.SHA256)
			}
		})
	}
}

func TestPillowCropSubimageAndBadMetadata(t *testing.T) {
	from := image.NewNRGBA(image.Rect(-11, -9, 20, 24))
	for y := from.Rect.Min.Y; y < from.Rect.Max.Y; y++ {
		for x := from.Rect.Min.X; x < from.Rect.Max.X; x++ {
			off := (y-from.Rect.Min.Y)*from.Stride + 4*(x-from.Rect.Min.X)
			from.Pix[off] = uint8(x + 25)
			from.Pix[off+1] = uint8(y + 37)
			from.Pix[off+2] = 200
			from.Pix[off+3] = 255
		}
	}
	sub := from.SubImage(image.Rect(-3, -4, 7, 9)).(*image.NRGBA)
	// Offsets on a subimage must use its own rectangle, never assume (0,0).
	got, e := CropNRGBAPillow(sub, image.Rect(-5, -5, 0, 0))
	if e != nil {
		t.Fatal(e)
	}
	if got.Pix[0] != 0 || got.Pix[1] != 0 || got.Pix[2] != 0 || got.Pix[3] != 0 {
		t.Fatal("outside crop nonzero")
	}
	off := (1*got.Stride + 4*2)
	if got.Pix[off] != uint8(-3+25) || got.Pix[off+1] != uint8(-4+37) {
		t.Fatal("subimage pixel mismatch")
	}
	b, ok, e := BBoxNRGBA(sub)
	if e != nil || !ok || b != sub.Rect {
		t.Fatalf("subimage bbox: %v %v %v", b, ok, e)
	}
	corrupt := &image.NRGBA{Pix: make([]byte, 5), Stride: 4, Rect: image.Rect(0, 0, 3, 5)}
	if _, _, e = BBoxNRGBA(corrupt); e == nil {
		t.Fatal("accepted corrupt RGBA")
	}
	if _, e = CropNRGBAPillow(corrupt, image.Rect(0, 0, 2, 2)); e == nil {
		t.Fatal("accepted corrupt RGBA crop")
	}
	if _, e = CropNRGBAPillow(sub, image.Rect(2, 2, 2, 2)); e == nil {
		t.Fatal("accepted empty crop")
	}
	if _, e = CropNRGBAPillow(sub, image.Rect(-1<<30, 0, 1<<30, 10)); e == nil {
		t.Fatal("accepted excessive crop")
	}
	if _, e = CropNRGBAPillow(sub, image.Rect(0, 0, 1<<31-1, 3)); e == nil {
		t.Fatal("accepted oversized crop")
	}
	a := image.NewAlpha(image.Rect(-3, -2, 8, 10))
	a.SetAlpha(0, 2, color.Alpha{A: 175})
	asub := a.SubImage(image.Rect(-1, 0, 4, 6)).(*image.Alpha)
	ab, ok, e := BBoxAlpha(asub)
	if e != nil || !ok || ab != image.Rect(0, 2, 1, 3) {
		t.Fatalf("alpha bbox: %v %v %v", ab, ok, e)
	}
	crop, e := CropAlphaPillow(asub, image.Rect(-2, 0, 2, 4))
	if e != nil || crop.AlphaAt(2, 2).A != 175 {
		t.Fatal("alpha crop")
	}
	if _, e = CropAlphaPillow(&image.Alpha{Pix: []byte{0}, Stride: 1, Rect: image.Rect(0, 0, 5, 5)}, image.Rect(0, 0, 1, 1)); e == nil {
		t.Fatal("accepted truncated alpha")
	}
}
