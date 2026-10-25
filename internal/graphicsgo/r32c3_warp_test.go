package graphicsgo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"math"
	"os"
	"testing"
)

func TestR32C3WarpPillow123Oracles(t *testing.T) {
	data, err := os.ReadFile(privateFixturePath(t, "oracle-warp"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema int    `json:"schema"`
		Engine string `json:"engine"`
		Cases  []struct {
			Width    int     `json:"width"`
			Height   int     `json:"height"`
			Seed     int     `json:"seed"`
			Empty    bool    `json:"empty"`
			SX       float64 `json:"sx"`
			SY       float64 `json:"sy"`
			Shear    float64 `json:"shear"`
			Rotation float64 `json:"rotation"`
			Factor   int     `json:"factor"`
			Input    string  `json:"input_sha256"`
			Output   string  `json:"output_sha256"`
			OutW     int     `json:"out_width"`
			OutH     int     `json:"out_height"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != 1 || fixture.Engine != "Pillow 12.3.0" || len(fixture.Cases) != 64 {
		t.Fatal("unexpected oracle provenance/count")
	}
	for i, c := range fixture.Cases {
		m := image.NewAlpha(image.Rect(0, 0, c.Width, c.Height))
		for y := 0; y < c.Height; y++ {
			for x := 0; x < c.Width; x++ {
				if !c.Empty && (x+y+c.Seed)%7 != 0 {
					m.Pix[y*m.Stride+x] = byte((x*17 + y*37 + c.Seed*13) % 256)
				}
			}
		}
		hash := func(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
		if hash(m.Pix) != c.Input {
			t.Fatalf("case %d input provenance", i)
		}
		out, err := WarpR32C3Alpha(m, R32C3Warp{HorizontalScale: c.SX, VerticalScale: c.SY, ShearX: c.Shear, RotationDegrees: c.Rotation, Factor: c.Factor})
		if err != nil {
			t.Fatalf("case %d warp: %v", i, err)
		}
		if out.Rect.Dx() != c.OutW || out.Rect.Dy() != c.OutH {
			t.Errorf("case %d size got %dx%d expected %dx%d", i, out.Rect.Dx(), out.Rect.Dy(), c.OutW, c.OutH)
			continue
		}
		if actual := hash(out.Pix); actual != c.Output {
			t.Errorf("case %d mismatch Go %s Pillow %s", i, actual, c.Output)
		}
		if hash(m.Pix) != c.Input {
			t.Fatalf("case %d input mutated", i)
		}
	}
}
func TestR32C3WarpRejectsInvalid(t *testing.T) {
	m := image.NewAlpha(image.Rect(0, 0, 12, 13))
	m.Pix[11] = 210
	base := R32C3Warp{HorizontalScale: 1, VerticalScale: 1, Factor: 4}
	bad := []R32C3Warp{
		{}, {HorizontalScale: 1, VerticalScale: 1, Factor: 3},
		{HorizontalScale: 0, VerticalScale: 1, Factor: 4},
		{HorizontalScale: 1, VerticalScale: -1, Factor: 4},
		{HorizontalScale: math.NaN(), VerticalScale: 1, Factor: 4},
		{HorizontalScale: 1, VerticalScale: 1, ShearX: math.Inf(1), Factor: 4},
		{HorizontalScale: 1, VerticalScale: 1, RotationDegrees: math.NaN(), Factor: 4},
		{HorizontalScale: 1e5, VerticalScale: 1, Factor: 4},
	}
	for i, p := range bad {
		if _, err := WarpR32C3Alpha(m, p); err == nil {
			t.Errorf("invalid %d accepted", i)
		}
	}
	if _, err := WarpR32C3Alpha(nil, base); err == nil {
		t.Fatal("nil accepted")
	}
	mm := &image.Alpha{Pix: []byte{1}, Stride: 12, Rect: image.Rect(0, 0, 12, 13)}
	if _, err := WarpR32C3Alpha(mm, base); err == nil {
		t.Fatal("truncated pix accepted")
	}
	imageWithStride := image.NewAlpha(image.Rect(0, 0, 17, 19))
	imageWithStride.Pix[3*imageWithStride.Stride+2] = 100
	sub := imageWithStride.SubImage(image.Rect(1, 2, 11, 13)).(*image.Alpha)
	if _, err := WarpR32C3Alpha(sub, base); err != nil {
		t.Fatalf("subimage: %v", err)
	}
}
