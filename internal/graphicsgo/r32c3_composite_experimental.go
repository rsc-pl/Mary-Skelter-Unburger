package graphicsgo

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

// R32C3CompositeSpec describes the RGBA-stage parameters of the accepted
// Python R32C3 renderer. All mask inputs have already been rasterized and warped
// at Factor times final resolution. This is a research-only stage, not a font
// renderer or a production graphics-family builder.
type R32C3CompositeSpec struct {
	Width, Height, Factor                                                       int
	OuterEnabled, InnerEnabled, StrokeFallback, GradientEnabled, AccentsEnabled bool
	OuterColor, InnerColor, StrokeColor, FillColor, GradientTop, GradientBottom color.NRGBA
	ShadowColor                                                                 color.NRGBA
	ShadowOffset                                                                image.Point // at final resolution; multiplied by Factor
	ShadowBlurRadius                                                            float64     // at final resolution; multiplied by Factor
	AccentColor                                                                 color.NRGBA
}

func r32Even(v float64) int { return int(math.RoundToEven(v)) }
func r32Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func r32Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func r32PasteMask(dst, src *image.Alpha, xOff, yOff int) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	for y := r32Max(0, -yOff); y < r32Min(h, dst.Rect.Dy()-yOff); y++ {
		x0 := r32Max(0, -xOff)
		x1 := r32Min(w, dst.Rect.Dx()-xOff)
		if x1 <= x0 {
			continue
		}
		from := (y)*src.Stride + x0
		to := (y+yOff)*dst.Stride + (x0 + xOff)
		copy(dst.Pix[to:to+x1-x0], src.Pix[from:from+x1-x0])
	}
}

// CompositeR32C3WarpedMasks ports the complete post-warp stages of
// r32c3_typography.render_label: border/padding, shadow with GaussianBlur,
// stroke layering, face gradient/tint, optional precomputed accents, alpha bbox,
// bounded high-res crop/refit, centering, and one final RGBA Lanczos downsample.
// The caller must supply source-faithful warped masks at the same dimensions.
// Output is not a production-valid replacement until text shaping, fit and
// all graphics-family results independently match the reference.
func CompositeR32C3WarpedMasks(m GlyphMasks, p R32C3CompositeSpec) (*image.NRGBA, error) {
	if p.Width < 10 || p.Height < 10 || p.Width > 2048 || p.Height > 2048 || p.Factor < 4 || p.Factor > 16 ||
		int64(p.Width)*int64(p.Height)*int64(p.Factor)*int64(p.Factor) > 24_000_000 ||
		math.IsNaN(p.ShadowBlurRadius) || math.IsInf(p.ShadowBlurRadius, 0) || p.ShadowBlurRadius < 0 || p.ShadowBlurRadius*float64(p.Factor) > 128 ||
		p.ShadowOffset.X < -512 || p.ShadowOffset.X > 512 || p.ShadowOffset.Y < -512 || p.ShadowOffset.Y > 512 {
		return nil, ErrInvalidRaster
	}
	if err := validateAlpha(m.Face); err != nil {
		return nil, err
	}
	if err := validateAlpha(m.Inner); err != nil {
		return nil, err
	}
	if err := validateAlpha(m.Outer); err != nil {
		return nil, err
	}
	w, h := m.Outer.Rect.Dx(), m.Outer.Rect.Dy()
	if w != m.Face.Rect.Dx() || h != m.Face.Rect.Dy() || w != m.Inner.Rect.Dx() || h != m.Inner.Rect.Dy() ||
		int64(w)*int64(h) > 24_000_000 {
		return nil, ErrInvalidRaster
	}
	if p.AccentsEnabled && (validateAlpha(m.Accents) != nil || m.Accents.Rect.Dx() != w || m.Accents.Rect.Dy() != h) {
		return nil, ErrInvalidRaster
	}
	ox, oy := p.ShadowOffset.X*p.Factor, p.ShadowOffset.Y*p.Factor
	bbox, ok, err := BBoxAlpha(m.Outer)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("graphicsgo: R32C3 empty outer mask")
	}
	bbox = bbox.Sub(m.Outer.Rect.Min)
	border := r32Max(6*p.Factor, r32Even(float64(r32Max(p.Width, p.Height))*0.03*float64(p.Factor)))
	left := r32Max(0, -r32Min(bbox.Min.X, bbox.Min.X+ox)) + border
	top := r32Max(0, -r32Min(bbox.Min.Y, bbox.Min.Y+oy)) + border
	right := r32Max(0, r32Max(bbox.Max.X, bbox.Max.X+ox)-w) + border
	bottom := r32Max(0, r32Max(bbox.Max.Y, bbox.Max.Y+oy)-h) + border
	W, H := w+left+right, h+top+bottom
	if W <= 0 || H <= 0 || int64(W)*int64(H) > 24_000_000 {
		return nil, ErrInvalidRaster
	}
	padded := func(a *image.Alpha) *image.Alpha {
		n := image.NewAlpha(image.Rect(0, 0, W, H))
		r32PasteMask(n, a, left, top)
		return n
	}
	face, inner, outer := padded(m.Face), padded(m.Inner), padded(m.Outer)
	layers := image.NewNRGBA(image.Rect(0, 0, W, H))
	put := func(mask *image.Alpha, ink color.NRGBA) error {
		layer, e := TintMask(mask, ink)
		if e != nil {
			return e
		}
		return CompositeOver(layers, layer, image.Point{})
	}
	if p.ShadowColor.A > 0 && (ox != 0 || oy != 0) {
		shadow := image.NewAlpha(image.Rect(0, 0, W, H))
		r32PasteMask(shadow, outer, ox, oy)
		if p.ShadowBlurRadius > 0 {
			var e error
			shadow, e = GaussianBlurAlpha(shadow, p.ShadowBlurRadius*float64(p.Factor))
			if e != nil {
				return nil, e
			}
		}
		if e := put(shadow, p.ShadowColor); e != nil {
			return nil, e
		}
	}
	if p.OuterEnabled {
		if e := put(outer, p.OuterColor); e != nil {
			return nil, e
		}
	}
	if p.InnerEnabled {
		if e := put(inner, p.InnerColor); e != nil {
			return nil, e
		}
	}
	if !p.InnerEnabled && !p.OuterEnabled && p.StrokeFallback {
		if e := put(outer, p.StrokeColor); e != nil {
			return nil, e
		}
	}
	if p.GradientEnabled {
		gradient, e := GradientMask(face, p.GradientTop, p.GradientBottom)
		if e != nil {
			return nil, e
		}
		if e = CompositeOver(layers, gradient, image.Point{}); e != nil {
			return nil, e
		}
	} else if e := put(face, p.FillColor); e != nil {
		return nil, e
	}
	if p.AccentsEnabled {
		if e := put(padded(m.Accents), p.AccentColor); e != nil {
			return nil, e
		}
	}
	bounds, ok, err := BBoxNRGBA(layers)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("graphicsgo: R32C3 fully transparent")
	}
	pad := r32Max(2*p.Factor, r32Even(float64(r32Min(p.Width, p.Height))*0.06*float64(p.Factor)))
	cr := image.Rect(r32Max(0, bounds.Min.X-pad), r32Max(0, bounds.Min.Y-pad), r32Min(W, bounds.Max.X+pad), r32Min(H, bounds.Max.Y+pad))
	crop, err := CropNRGBAPillow(layers, cr)
	if err != nil {
		return nil, err
	}
	limW, limH := p.Width*p.Factor-2*pad, p.Height*p.Factor-2*pad
	if limW <= 0 || limH <= 0 {
		return nil, ErrInvalidRaster
	}
	scale := math.Min(1, math.Min(float64(limW)/float64(crop.Rect.Dx()), float64(limH)/float64(crop.Rect.Dy())))
	if scale < 1 {
		nw := r32Max(1, r32Even(float64(crop.Rect.Dx())*scale))
		nh := r32Max(1, r32Even(float64(crop.Rect.Dy())*scale))
		crop, err = ResizeLanczosNRGBA(crop, nw, nh)
		if err != nil {
			return nil, err
		}
	}
	hi := image.NewNRGBA(image.Rect(0, 0, p.Width*p.Factor, p.Height*p.Factor))
	if err := CompositeOver(hi, crop, image.Pt((hi.Rect.Dx()-crop.Rect.Dx())/2, (hi.Rect.Dy()-crop.Rect.Dy())/2)); err != nil {
		return nil, err
	}
	final, err := ResizeLanczosNRGBA(hi, p.Width, p.Height)
	if err != nil {
		return nil, err
	}
	b, ok, err := BBoxNRGBA(final)
	if err != nil {
		return nil, err
	}
	if !ok || b.Min.X < 1 || b.Min.Y < 1 || b.Max.X > p.Width-1 || b.Max.Y > p.Height-1 {
		return nil, fmt.Errorf("graphicsgo: R32C3 clipping")
	}
	return final, nil
}

// WarpAndCompositeR32C3Masks joins the already-Pillow-oracled geometry and
// high-resolution RGBA stages. Face, Inner and Outer are the source-sized
// masks output by the *caller*'s source-faithful font/stroke rasterizer;
// Accents (if present) must already be drawn in the post-warp face coordinate
// space, just as _solid_accents in the Python reference. Font selection, fit,
// FreeType stroke rasterization and game-family placement remain outside.
func WarpAndCompositeR32C3Masks(raw GlyphMasks, warp R32C3Warp, comp R32C3CompositeSpec) (*image.NRGBA, error) {
	if warp.Factor != comp.Factor || warp.Factor < 4 {
		return nil, ErrInvalidRaster
	}
	face, err := WarpR32C3Alpha(raw.Face, warp)
	if err != nil {
		return nil, err
	}
	inner, err := WarpR32C3Alpha(raw.Inner, warp)
	if err != nil {
		return nil, err
	}
	outer, err := WarpR32C3Alpha(raw.Outer, warp)
	if err != nil {
		return nil, err
	}
	return CompositeR32C3WarpedMasks(GlyphMasks{Face: face, Inner: inner, Outer: outer, Accents: raw.Accents}, comp)
}
