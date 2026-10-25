package graphicsgo

import (
	"fmt"
	"image"
	"image/color"
)

// GlyphMasks are alpha masks already rasterized at the same supersampled size.
// A TrueType/OpenType rasterizer is NOT implemented by this package yet.
// Face, Inner, and Outer are separate masks, as in R32C3's Pillow pipeline.
type GlyphMasks struct{ Face, Inner, Outer, Accents *image.Alpha }

type GlyphPaint struct {
	OuterEnabled, InnerEnabled, GradientEnabled, ShadowEnabled, AccentsEnabled bool
	OuterColor, InnerColor, FillColor, GradientTop, GradientBottom             color.NRGBA
	ShadowColor, AccentColor                                                   color.NRGBA
	ShadowOffset                                                               image.Point // offsets are in supersampled source pixels
	ShadowBlurRadius                                                           int         // explicitly rejected until Pillow blur parity exists
}

// PaintGlyphLayers reproduces the pure alpha/tint/gradient composition stages
// of R32C3 at supersampled resolution. It deliberately DOES NOT do outline
// rasterization, font sizing, transformations, blur, or LANCZOS downsampling.
// Unsupported requested effects fail closed, not silently degrade artwork.
func PaintGlyphLayers(m GlyphMasks, p GlyphPaint) (*image.NRGBA, error) {
	if err := validateAlpha(m.Face); err != nil {
		return nil, err
	}
	if err := validateAlpha(m.Inner); err != nil {
		return nil, err
	}
	if err := validateAlpha(m.Outer); err != nil {
		return nil, err
	}
	w, h := m.Face.Rect.Dx(), m.Face.Rect.Dy()
	same := func(v *image.Alpha) bool {
		return v != nil && v.Rect.Dx() == w && v.Rect.Dy() == h && validateAlpha(v) == nil
	}
	if !same(m.Inner) || !same(m.Outer) {
		return nil, fmt.Errorf("graphicsgo: glyph mask geometry mismatch")
	}
	if p.AccentsEnabled && !same(m.Accents) {
		return nil, fmt.Errorf("graphicsgo: accent mask missing/mismatched")
	}
	if p.ShadowBlurRadius < 0 || p.ShadowBlurRadius > 0 {
		return nil, fmt.Errorf("graphicsgo: shadow blur not ported")
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	apply := func(mask *image.Alpha, ink color.NRGBA, at image.Point) error {
		layer, err := TintMask(mask, ink)
		if err != nil {
			return err
		}
		return CompositeOver(out, layer, at)
	}
	if p.ShadowEnabled && p.ShadowColor.A > 0 && p.ShadowOffset != (image.Point{}) {
		if err := apply(m.Outer, p.ShadowColor, p.ShadowOffset); err != nil {
			return nil, err
		}
	}
	if p.OuterEnabled {
		if err := apply(m.Outer, p.OuterColor, image.Point{}); err != nil {
			return nil, err
		}
	}
	if p.InnerEnabled {
		if err := apply(m.Inner, p.InnerColor, image.Point{}); err != nil {
			return nil, err
		}
	}
	if p.GradientEnabled {
		layer, err := GradientMask(m.Face, p.GradientTop, p.GradientBottom)
		if err != nil {
			return nil, err
		}
		if err := CompositeOver(out, layer, image.Point{}); err != nil {
			return nil, err
		}
	} else {
		if err := apply(m.Face, p.FillColor, image.Point{}); err != nil {
			return nil, err
		}
	}
	if p.AccentsEnabled {
		if err := apply(m.Accents, p.AccentColor, image.Point{}); err != nil {
			return nil, err
		}
	}
	return out, nil
}
