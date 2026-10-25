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

type affineOracleCase struct {
	Name   string     `json:"name"`
	SrcW   int        `json:"src_w"`
	SrcH   int        `json:"src_h"`
	DstW   int        `json:"dst_w"`
	DstH   int        `json:"dst_h"`
	Seed   int        `json:"seed"`
	Matrix [6]float64 `json:"matrix"`
	SHA    string     `json:"sha256"`
}

func TestAffineBicubicPillowOracles(t *testing.T) {
	data, err := os.ReadFile(privateFixturePath(t, "oracle-affine"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []affineOracleCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) != 32 {
		t.Fatalf("expected 32 cases, got %d", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			src := rgbaInput(tc.SrcW, tc.SrcH, tc.Seed)
			before := append([]byte(nil), src.Pix...)
			var actual []byte
			if len(tc.Name) > 2 && tc.Name[len(tc.Name)-2:] == "/L" {
				alpha := image.NewAlpha(image.Rect(0, 0, tc.SrcW, tc.SrcH))
				for y := 0; y < tc.SrcH; y++ {
					for x := 0; x < tc.SrcW; x++ {
						alpha.Pix[y*alpha.Stride+x] = src.Pix[y*src.Stride+x*4+3]
					}
				}
				im, e := AffineBicubicAlpha(alpha, tc.DstW, tc.DstH, tc.Matrix)
				if e != nil {
					t.Fatal(e)
				}
				actual = im.Pix
			} else {
				im, e := AffineBicubicNRGBA(src, tc.DstW, tc.DstH, tc.Matrix)
				if e != nil {
					t.Fatal(e)
				}
				actual = im.Pix
			}
			got := sha256.Sum256(actual)
			if hex.EncodeToString(got[:]) != tc.SHA {
				t.Fatalf("expected %s, got %x", tc.SHA, got)
			}
			if string(before) != string(src.Pix) {
				t.Fatal("mutated source")
			}
		})
	}
}
func TestAffineBicubicInvalidAndSubimages(t *testing.T) {
	src := rgbaInput(12, 10, 16)
	mat := [6]float64{1, -0.2, 0.1, 0, 1, 0}
	for _, bad := range [][6]float64{{math.NaN(), 0, 0, 0, 1, 0}, {1, 0, 0, 0, math.Inf(1), 0}, {1, 0, 1e10, 0, 1, 0}} {
		if _, err := AffineBicubicNRGBA(src, 10, 10, bad); err == nil {
			t.Fatal("accepted invalid transform")
		}
	}
	if _, err := AffineBicubicNRGBA(src, 0, 10, mat); err == nil {
		t.Fatal("accepted zero size")
	}
	if _, err := AffineBicubicNRGBA(&image.NRGBA{Pix: []byte{1}, Stride: 4, Rect: image.Rect(0, 0, 2, 2)}, 10, 10, mat); err == nil {
		t.Fatal("accepted broken source")
	}
	padded := rgbaInput(25, 22, 5)
	sub := padded.SubImage(image.Rect(4, 3, 17, 15)).(*image.NRGBA)
	flat := image.NewNRGBA(image.Rect(0, 0, 13, 12))
	for y := 0; y < 12; y++ {
		copy(flat.Pix[y*flat.Stride:y*flat.Stride+13*4], sub.Pix[y*sub.Stride:y*sub.Stride+13*4])
	}
	pre := append([]byte(nil), padded.Pix...)
	a, e := AffineBicubicNRGBA(sub, 15, 12, mat)
	if e != nil {
		t.Fatal(e)
	}
	b, e := AffineBicubicNRGBA(flat, 15, 12, mat)
	if e != nil {
		t.Fatal(e)
	}
	if string(a.Pix) != string(b.Pix) {
		t.Fatal("subimage differ")
	}
	if string(pre) != string(padded.Pix) {
		t.Fatal("changed parent")
	}
}
