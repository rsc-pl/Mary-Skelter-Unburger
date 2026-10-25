package graphicsgo

import (
	"encoding/json"
	"errors"
	"image"
	"math"
	"os"
	"strconv"
	"testing"
)

type FitCase struct {
	Font   string `json:"font"`
	Text   string `json:"text"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Factor int    `json:"factor"`
	Preset struct {
		NominalSize        int     `json:"nominal_size"`
		MinSize            int     `json:"min_size"`
		HorizontalScale    float64 `json:"horizontal_scale"`
		MinHorizontalScale float64 `json:"min_horizontal_scale"`
		VerticalScale      float64 `json:"vertical_scale"`
		ShearX             float64 `json:"shear_x"`
		InnerWidth         int     `json:"r32c3_inner_width"`
		OuterWidth         int     `json:"r32c3_outer_width"`
	} `json:"preset"`
	BBoxes   map[string][4]int `json:"bboxes"`
	Expected *struct {
		Size     int     `json:"size"`
		BBox     [4]int  `json:"bbox"`
		Pad      int     `json:"pad"`
		MaskSize [2]int  `json:"mask_size"`
		FitSX    float64 `json:"fit_sx"`
	} `json:"expected"`
}

func TestR32C3FitMatchesAcceptedPythonWithRealFontBBoxes(t *testing.T) {
	b, e := os.ReadFile(privateFixturePath(t, "oracle-fit"))
	if e != nil {
		t.Fatal(e)
	}
	var data struct {
		Schema        int       `json:"schema"`
		PillowVersion string    `json:"pillow_version"`
		Cases         []FitCase `json:"cases"`
	}
	if e = json.Unmarshal(b, &data); e != nil {
		t.Fatal(e)
	}
	if data.Schema != 1 || data.PillowVersion != "12.3.0" || len(data.Cases) != 400 {
		t.Fatalf("bad oracle header/cases: %#v %d", data.PillowVersion, len(data.Cases))
	}
	failures, fallbacks := 0, 0
	for i, c := range data.Cases {
		out, e := FitR32C3MaskPair(c.Text, R32C3FitSpec{
			Width: c.Width, Height: c.Height, Factor: c.Factor,
			NominalSize: c.Preset.NominalSize, MinSize: c.Preset.MinSize,
			HorizontalScale: c.Preset.HorizontalScale, MinHorizontalScale: c.Preset.MinHorizontalScale,
			VerticalScale: c.Preset.VerticalScale, ShearX: c.Preset.ShearX,
			InnerWidth: c.Preset.InnerWidth, OuterWidth: &c.Preset.OuterWidth,
		}, func(px int) (image.Rectangle, error) {
			rect, ok := c.BBoxes[strconv.Itoa(px)]
			if !ok {
				return image.Rectangle{}, ErrInvalidRaster
			}
			return image.Rect(rect[0], rect[1], rect[2], rect[3]), nil
		})
		if c.Expected == nil {
			failures++
			if !errors.Is(e, ErrR32C3NoFit) {
				t.Fatalf("case %d %s: expected no-fit, got %#v %v", i, c.Text, out, e)
			}
			continue
		}
		exp := c.Expected
		if e != nil || out.NominalSize != exp.Size || out.FontPixelSize != exp.Size*c.Factor ||
			out.TextBBox != image.Rect(exp.BBox[0], exp.BBox[1], exp.BBox[2], exp.BBox[3]) ||
			out.Pad != exp.Pad || out.MaskSize != image.Pt(exp.MaskSize[0], exp.MaskSize[1]) ||
			math.Abs(out.HorizontalScale-exp.FitSX) > 1e-12 {
			t.Fatalf("case %d %s %s %dx%d x%d: result=%#v err=%v expected=%#v", i, c.Font, c.Text, c.Width, c.Height, c.Factor, out, e, exp)
		}
		if out.UsedFallback {
			fallbacks++
		}
	}
	if failures != 78 || fallbacks != 2 {
		t.Fatalf("unexpected oracle coverage failures=%d fallback=%d", failures, fallbacks)
	}
}

func TestR32C3FitInputSafetyAndCallback(t *testing.T) {
	bbox := func(px int) (image.Rectangle, error) { return image.Rect(-4, -8, 60, 30), nil }
	good := R32C3FitSpec{Width: 300, Height: 100, Factor: 4, NominalSize: 35, MinSize: 17, HorizontalScale: 1, MinHorizontalScale: .7, VerticalScale: 1}
	if _, e := FitR32C3MaskPair("Sample", good, bbox); e != nil {
		t.Fatal(e)
	}
	bad := []R32C3FitSpec{
		{Width: 300, Height: 100, Factor: 1, NominalSize: 35, HorizontalScale: 1},
		{Width: 300, Height: 100, Factor: 4, NominalSize: 0, HorizontalScale: 1},
		{Width: 300, Height: 100, Factor: 4, NominalSize: 35, HorizontalScale: math.NaN()},
		{Width: 300, Height: 100, Factor: 4, NominalSize: 35, HorizontalScale: 0.8, MinHorizontalScale: 1},
		{Width: 300, Height: 100, Factor: 4, NominalSize: 35, HorizontalScale: 1, ShearX: math.Inf(1)},
		{Width: 300, Height: 100, Factor: 4, NominalSize: 35, HorizontalScale: 1, StrokeWidth: -1},
	}
	for i, b := range bad {
		if _, e := FitR32C3MaskPair("Sample", b, bbox); e == nil {
			t.Fatalf("invalid spec %d accepted", i)
		}
	}
	if _, e := FitR32C3MaskPair("x", good, nil); e == nil {
		t.Fatal("nil callback accepted")
	}
	if _, e := FitR32C3MaskPair("x", good, func(int) (image.Rectangle, error) { return image.Rectangle{}, errors.New("measure failed") }); e == nil || e.Error() != "measure failed" {
		t.Fatal("callback error not propagated", e)
	}
	if _, e := FitR32C3MaskPair("x", good, func(int) (image.Rectangle, error) { return image.Rectangle{}, nil }); e == nil {
		t.Fatal("empty measured box accepted")
	}
}
