package graphicsgo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"strings"
	"testing"
)

type rotateOracleCase struct {
	Name  string  `json:"name"`
	SrcW  int     `json:"src_w"`
	SrcH  int     `json:"src_h"`
	Seed  int     `json:"seed"`
	Angle float64 `json:"angle"`
	DstW  int     `json:"dst_w"`
	DstH  int     `json:"dst_h"`
	SHA   string  `json:"sha256"`
}

func TestRotateBicubicPillowOracles(t *testing.T) {
	b, err := os.ReadFile(privateFixturePath(t, "oracle-rotate"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []rotateOracleCase
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 28 {
		t.Fatalf("expected 28 got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			src := rgbaInput(tc.SrcW, tc.SrcH, tc.Seed)
			var pix []byte
			var gotW, gotH int
			if strings.HasSuffix(tc.Name, "/L") {
				alpha := image.NewAlpha(image.Rect(0, 0, tc.SrcW, tc.SrcH))
				for y := 0; y < tc.SrcH; y++ {
					for x := 0; x < tc.SrcW; x++ {
						alpha.Pix[y*alpha.Stride+x] = src.Pix[y*src.Stride+4*x+3]
					}
				}
				out, e := RotateBicubicAlphaExpand(alpha, tc.Angle)
				if e != nil {
					t.Fatal(e)
				}
				pix = out.Pix
				gotW, gotH = out.Rect.Dx(), out.Rect.Dy()
			} else {
				out, e := RotateBicubicNRGBAExpand(src, tc.Angle)
				if e != nil {
					t.Fatal(e)
				}
				pix = out.Pix
				gotW, gotH = out.Rect.Dx(), out.Rect.Dy()
			}
			if gotW != tc.DstW || gotH != tc.DstH {
				t.Fatalf("size %dx%d, expected %dx%d", gotW, gotH, tc.DstW, tc.DstH)
			}
			actual := sha256.Sum256(pix)
			if fmt.Sprintf("%x", actual) != tc.SHA {
				t.Fatalf("want %s actual %x", tc.SHA, actual)
			}
		})
	}
}
func TestRotateBicubicBadInput(t *testing.T) {
	src := rgbaInput(7, 6, 5)
	for _, angle := range []float64{math.NaN(), math.Inf(1), 1e10} {
		if _, err := RotateBicubicNRGBAExpand(src, angle); err == nil {
			t.Fatal("accepted bad angle")
		}
	}
}
