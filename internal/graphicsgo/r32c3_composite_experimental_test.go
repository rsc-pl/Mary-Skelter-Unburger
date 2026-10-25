package graphicsgo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"math"
	"os"
	"testing"
)

func TestR32C3PostWarpRGBAAgainstIndependentPillow123(t *testing.T) {
	raw, e := os.ReadFile(privateFixturePath(t, "oracle-composite"))
	if e != nil {
		t.Fatal(e)
	}
	type c struct {
		ID                                                                                                      string `json:"id"`
		Seed, MaskW, MaskH, Width, Height, Factor                                                               int
		OuterEnabled, InnerEnabled, StrokeFallback, GradientEnabled, AccentsEnabled                             bool
		OuterRGBA, InnerRGBA, StrokeRGBA, FillRGBA, GradientTopRGBA, GradientBottomRGBA, ShadowRGBA, AccentRGBA [4]uint8
		ShadowOffset                                                                                            [2]int    `json:"shadow_offset"`
		ShadowBlurRadius                                                                                        float64   `json:"shadow_blur_radius"`
		MaskSHA                                                                                                 [4]string `json:"mask_sha256"`
		OutputSHA                                                                                               string    `json:"output_sha256"`
		OutputBBox                                                                                              [4]int    `json:"output_bbox"`
	}
	var fixture struct {
		Schema        int
		PillowVersion string `json:"pillow_version"`
		Cases         []struct {
			ID                 string    `json:"id"`
			Seed               int       `json:"seed"`
			MaskW              int       `json:"mask_width"`
			MaskH              int       `json:"mask_height"`
			Width              int       `json:"width"`
			Height             int       `json:"height"`
			Factor             int       `json:"factor"`
			OuterEnabled       bool      `json:"outer_enabled"`
			InnerEnabled       bool      `json:"inner_enabled"`
			StrokeFallback     bool      `json:"stroke_fallback"`
			GradientEnabled    bool      `json:"gradient_enabled"`
			AccentsEnabled     bool      `json:"accents_enabled"`
			OuterRGBA          [4]uint8  `json:"outer_rgba"`
			InnerRGBA          [4]uint8  `json:"inner_rgba"`
			StrokeRGBA         [4]uint8  `json:"stroke_rgba"`
			FillRGBA           [4]uint8  `json:"fill_rgba"`
			GradientTopRGBA    [4]uint8  `json:"gradient_top_rgba"`
			GradientBottomRGBA [4]uint8  `json:"gradient_bottom_rgba"`
			ShadowRGBA         [4]uint8  `json:"shadow_rgba"`
			AccentRGBA         [4]uint8  `json:"accent_rgba"`
			ShadowOffset       [2]int    `json:"shadow_offset"`
			ShadowBlurRadius   float64   `json:"shadow_blur_radius"`
			MaskSHA            [4]string `json:"mask_sha256"`
			OutputSHA          string    `json:"output_sha256"`
			OutputBBox         [4]int    `json:"output_bbox"`
			Warp               *struct {
				HorizontalScale float64 `json:"horizontal_scale"`
				VerticalScale   float64 `json:"vertical_scale"`
				ShearX          float64 `json:"shear_x"`
				RotationDegrees float64 `json:"rotation_degrees"`
			} `json:"warp"`
		}
	}
	if e = json.Unmarshal(raw, &fixture); e != nil {
		t.Fatal(e)
	}
	if fixture.Schema != 1 || fixture.PillowVersion != "12.3.0" || len(fixture.Cases) != 11 {
		t.Fatalf("unexpected reference provenance/count: %d, %s, %d", fixture.Schema, fixture.PillowVersion, len(fixture.Cases))
	}
	hash := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	col := func(p [4]uint8) color.NRGBA { return color.NRGBA{R: p[0], G: p[1], B: p[2], A: p[3]} }
	for _, tt := range fixture.Cases {
		t.Run(tt.ID, func(t *testing.T) {
			ms := make([]*image.Alpha, 4)
			for kind, border := range []int{9, 7, 5, 12} {
				a := image.NewAlpha(image.Rect(0, 0, tt.MaskW, tt.MaskH))
				for y := 0; y < tt.MaskH; y++ {
					for x := 0; x < tt.MaskW; x++ {
						v := 0
						if x >= border && x < tt.MaskW-border && y >= border/2 && y < tt.MaskH-border/2 {
							v = (17*x + 13*y + 19*tt.Seed + (x*y)%17 + kind*41) % 256
							if kind < 3 {
								if v < 45 {
									v = 45
								}
							} else if (x+y+tt.Seed)%7 != 0 {
								v = 0
							}
						}
						a.Pix[y*a.Stride+x] = uint8(v)
					}
				}
				if got := hash(a.Pix); got != tt.MaskSHA[kind] {
					t.Fatalf("input mask[%d] SHA mismatch got %s", kind, got)
				}
				ms[kind] = a
			}
			originalHashes := [4]string{hash(ms[0].Pix), hash(ms[1].Pix), hash(ms[2].Pix), hash(ms[3].Pix)}
			comp := R32C3CompositeSpec{
				Width: tt.Width, Height: tt.Height, Factor: tt.Factor,
				OuterEnabled: tt.OuterEnabled, InnerEnabled: tt.InnerEnabled, StrokeFallback: tt.StrokeFallback,
				GradientEnabled: tt.GradientEnabled, AccentsEnabled: tt.AccentsEnabled,
				OuterColor: col(tt.OuterRGBA), InnerColor: col(tt.InnerRGBA), StrokeColor: col(tt.StrokeRGBA),
				FillColor: col(tt.FillRGBA), GradientTop: col(tt.GradientTopRGBA), GradientBottom: col(tt.GradientBottomRGBA),
				ShadowColor: col(tt.ShadowRGBA), ShadowOffset: image.Pt(tt.ShadowOffset[0], tt.ShadowOffset[1]), ShadowBlurRadius: tt.ShadowBlurRadius,
				AccentColor: col(tt.AccentRGBA),
			}
			masks := GlyphMasks{Face: ms[0], Inner: ms[1], Outer: ms[2], Accents: ms[3]}
			var got *image.NRGBA
			var e error
			if tt.Warp == nil {
				got, e = CompositeR32C3WarpedMasks(masks, comp)
			} else {
				w := R32C3Warp{HorizontalScale: tt.Warp.HorizontalScale, VerticalScale: tt.Warp.VerticalScale, ShearX: tt.Warp.ShearX, RotationDegrees: tt.Warp.RotationDegrees, Factor: tt.Factor}
				if w.HorizontalScale == 0 {
					w.HorizontalScale = 1
				}
				if w.VerticalScale == 0 {
					w.VerticalScale = 1
				}
				masks.Accents = nil
				got, e = WarpAndCompositeR32C3Masks(masks, w, comp)
			}
			if e != nil {
				t.Fatal(e)
			}
			if got.Rect.Dx() != tt.Width || got.Rect.Dy() != tt.Height {
				t.Fatalf("wrong size %v", got.Rect)
			}
			bb, ok, err := BBoxNRGBA(got)
			if err != nil || !ok || bb != image.Rect(tt.OutputBBox[0], tt.OutputBBox[1], tt.OutputBBox[2], tt.OutputBBox[3]) {
				t.Errorf("bbox %v versus %v: %v", bb, tt.OutputBBox, err)
			}
			if actual := hash(got.Pix); actual != tt.OutputSHA {
				t.Errorf("Pillow RGBA pixel mismatch: Go=%s Pillow=%s", actual, tt.OutputSHA)
			}
			for i, m := range ms {
				if hash(m.Pix) != originalHashes[i] {
					t.Fatalf("mutated mask %d", i)
				}
			}
		})
	}
}

func TestR32C3PostWarpFailClosed(t *testing.T) {
	small := image.NewAlpha(image.Rect(0, 0, 22, 18))
	for y := 4; y < 14; y++ {
		for x := 5; x < 17; x++ {
			small.Pix[y*small.Stride+x] = 255
		}
	}
	m := GlyphMasks{Face: small, Inner: small, Outer: small}
	s := R32C3CompositeSpec{Width: 40, Height: 20, Factor: 4, FillColor: color.NRGBA{255, 255, 255, 255}}
	bad := []R32C3CompositeSpec{
		{}, {Width: 40, Height: 20, Factor: 3}, {Width: 40, Height: 20, Factor: 20},
		{Width: 40, Height: 20, Factor: 4, ShadowBlurRadius: -1},
		{Width: 40, Height: 20, Factor: 4, ShadowBlurRadius: math.NaN()},
		{Width: 2048, Height: 2048, Factor: 16},
		{Width: 40, Height: 20, Factor: 4, AccentsEnabled: true},
	}
	for i, p := range bad {
		if _, err := CompositeR32C3WarpedMasks(m, p); err == nil {
			t.Errorf("invalid case %d accepted", i)
		}
	}
	invalid := m
	invalid.Face = nil
	if _, err := CompositeR32C3WarpedMasks(invalid, s); err == nil {
		t.Fatal("nil face accepted")
	}
	invisible := image.NewAlpha(image.Rect(0, 0, 22, 18))
	invalid = m
	invalid.Outer = invisible
	if _, err := CompositeR32C3WarpedMasks(invalid, s); err == nil {
		t.Fatal("empty outer accepted")
	}
	// All effects disabled, fully transparent fill must be rejected by bbox.
	s.FillColor.A = 0
	if _, err := CompositeR32C3WarpedMasks(m, s); err == nil {
		t.Fatal("fully transparent accepted")
	}
}
