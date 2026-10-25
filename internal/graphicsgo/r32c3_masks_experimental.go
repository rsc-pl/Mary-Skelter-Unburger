package graphicsgo

// Native Latin R32C3 face/inner/outer mask construction, research-only.
// The baseline and canvas follow _draw_mask in the accepted Python recipe.
// Font hinting, full OpenType shaping and FreeType's vector stroker remain
// unresolved; the radial mask stroker here is deliberately not claimed exact.

import (
	"image"
	"math"
	"unicode/utf8"
)

// RasterR32C3LatinMasksExperimental places all unhinted glyphs on the SAME
// padded high-resolution canvas selected by FitR32C3MaskPair. Inner/outer
// masks are radial dilations of the whole face, not disconnected character
// bitmaps. This differs from FreeType's true vector-outline stroke expansion.
// samples==0 selects the continuous nonzero-area research rasterizer,
// while 2/4/8 select the original grid supersampler.
func (f *SFNT) RasterR32C3LatinMasksExperimental(text string, fit R32C3FitChoice,
	innerWidth, outerWidth, samples int) (GlyphMasks, error) {
	return f.rasterR32C3LatinMasks(text, fit, innerWidth, outerWidth, samples, false, false)
}

// RasterR32C3LatinMasksWholeOutlineExperimental investigates rasterizing the
// entire positioned text outline in a SINGLE coverage pass, rather than
// integer-quantizing every glyph's independent alpha mask. It is research-only
// and is not source-exact FreeType hinting, stroking or complex text shaping.
func (f *SFNT) RasterR32C3LatinMasksWholeOutlineExperimental(text string, fit R32C3FitChoice,
	innerWidth, outerWidth int) (GlyphMasks, error) {
	return f.rasterR32C3LatinMasks(text, fit, innerWidth, outerWidth, 0, true, false)
}

func (f *SFNT) rasterR32C3LatinMasks(text string, fit R32C3FitChoice,
	innerWidth, outerWidth, samples int, wholeOutline, blue bool) (GlyphMasks, error) {
	empty := GlyphMasks{}
	if f == nil || !utf8.ValidString(text) || len(text) == 0 || utf8.RuneCountInString(text) > 64 ||
		fit.FontPixelSize < 1 || fit.FontPixelSize > 1024 || fit.MaskSize.X < 1 || fit.MaskSize.Y < 1 ||
		int64(fit.MaskSize.X)*int64(fit.MaskSize.Y) > 24_000_000 || fit.Pad < 0 ||
		innerWidth < 0 || outerWidth < 0 || innerWidth+outerWidth > 128 ||
		(samples != 0 && samples != 2 && samples != 4 && samples != 8) || f.UnitsPerEm == 0 ||
		fit.TextBBox.Empty() || (blue && (wholeOutline || samples != 0 || f.Outline != CFF1Outline)) {
		return empty, ErrInvalidRaster
	}
	w, h := fit.MaskSize.X, fit.MaskSize.Y
	if w != fit.TextBBox.Dx()+fit.Pad*2 || h != fit.TextBBox.Dy()+fit.Pad*2 {
		return empty, ErrInvalidRaster
	}
	glyphs := make([]uint16, 0, len(text))
	for _, r := range text {
		if r < 32 || r > 126 {
			return empty, fontErr("unsupported R32C3 Latin glyph")
		}
		gid, err := f.GlyphIndex(r)
		if err != nil {
			return empty, err
		}
		glyphs = append(glyphs, gid)
	}
	face := image.NewAlpha(image.Rect(0, 0, w, h))
	scale := float64(fit.FontPixelSize) / float64(f.UnitsPerEm)
	asc := math.Ceil(float64(f.Ascent) * scale)
	pen := 0.0
	left := fit.Pad - fit.TextBBox.Min.X
	baseline := float64(fit.Pad-fit.TextBBox.Min.Y) + asc
	// Whole-outline method preserves fractional glyph origins before rasterization.
	// The old per-glyph compositor remains bit-for-bit the default candidate.
	var whole GlyphOutline
	for i, gid := range glyphs {
		advance, _, err := f.HMetric(gid)
		if err != nil {
			return empty, err
		}
		var placement, kern int
		if i+1 < len(glyphs) {
			p, err := f.GPOSPairLatin(gid, glyphs[i+1])
			if err != nil {
				return empty, err
			}
			if p.FirstYAdvance != 0 || p.FirstYPlacement != 0 || p.SecondYAdvance != 0 || p.SecondYPlacement != 0 || p.SecondXAdvance != 0 || p.SecondXPlacement != 0 {
				return empty, fontErr("unsupported shaping positioning")
			}
			placement, kern = p.FirstXPlacement, p.FirstXAdvance
		}
		if wholeOutline {
			var contours GlyphOutline
			if f.Outline == CFF1Outline {
				contours, err = f.GlyphOutlineCFF1(gid)
			} else if f.Outline == TrueTypeOutline {
				contours, err = f.GlyphOutlineTTF(gid)
			} else {
				return empty, fontErr("unsupported whole-outline font")
			}
			if err != nil {
				return empty, err
			}
			if len(whole)+len(contours) > 4096 {
				return empty, ErrInvalidRaster
			}
			shift := pen/scale + float64(placement)
			for _, c := range contours {
				moved := make(GlyphContour, len(c))
				for j, point := range c {
					point.X += shift
					moved[j] = point
				}
				whole = append(whole, moved)
			}
			pen += float64(int(advance)+kern) * scale
			continue
		}
		var raster *image.Alpha
		var origin image.Point
		if blue {
			raster, origin, err = f.RasterGlyphCFFBlueAnchorExperimental(gid, float64(fit.FontPixelSize))
		} else if samples == 0 {
			raster, origin, err = f.RasterGlyphNonzeroArea(gid, float64(fit.FontPixelSize))
		} else {
			raster, origin, err = f.RasterGlyph(gid, float64(fit.FontPixelSize), samples)
		}
		if err != nil {
			return empty, err
		}
		gx := left + int(math.Round(pen+float64(placement)*scale)) + origin.X
		gy := int(baseline) + origin.Y
		for y := 0; y < raster.Rect.Dy(); y++ {
			ty := gy + y
			if ty < 0 || ty >= h {
				continue
			}
			for x := 0; x < raster.Rect.Dx(); x++ {
				tx := gx + x
				if tx < 0 || tx >= w {
					continue
				}
				v := raster.Pix[y*raster.Stride+x]
				i := ty*face.Stride + tx
				if v > face.Pix[i] {
					face.Pix[i] = v
				}
			}
		}
		pen += float64(int(advance)+kern) * scale
	}
	if wholeOutline {
		if len(whole) == 0 {
			return empty, fontErr("empty whole-run glyph outline")
		}
		raster, origin, err := rasterContoursNonzeroArea(whole, float64(fit.FontPixelSize), float64(f.UnitsPerEm))
		if err != nil {
			return empty, err
		}
		for y := 0; y < raster.Rect.Dy(); y++ {
			ty := int(baseline) + origin.Y + y
			if ty < 0 || ty >= h {
				continue
			}
			for x := 0; x < raster.Rect.Dx(); x++ {
				tx := left + origin.X + x
				if tx < 0 || tx >= w {
					continue
				}
				face.Pix[ty*face.Stride+tx] = raster.Pix[y*raster.Stride+x]
			}
		}
	}
	inner := face
	if innerWidth > 0 {
		var err error
		inner, err = radialDilateAlphaR32C3(face, innerWidth)
		if err != nil {
			return empty, err
		}
	}
	outer := inner
	if outerWidth > 0 {
		var err error
		outer, err = radialDilateAlphaR32C3(face, innerWidth+outerWidth)
		if err != nil {
			return empty, err
		}
	}
	return GlyphMasks{Face: face, Inner: inner, Outer: outer}, nil
}

// radialDilateAlphaR32C3 is an explicitly *approximate* Euclidean pixel-space
// dilation, used only for studying native strokes. It does not alter its input.
func radialDilateAlphaR32C3(in *image.Alpha, radius int) (*image.Alpha, error) {
	if err := validateAlpha(in); err != nil {
		return nil, err
	}
	if radius < 0 || radius > 128 || int64(in.Rect.Dx())*int64(in.Rect.Dy())*int64((2*radius+1)*(2*radius+1)) > 180_000_000 {
		return nil, ErrInvalidRaster
	}
	w, h := in.Rect.Dx(), in.Rect.Dy()
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	if radius == 0 {
		for y := 0; y < h; y++ {
			copy(out.Pix[y*out.Stride:y*out.Stride+w], in.Pix[y*in.Stride:y*in.Stride+w])
		}
		return out, nil
	}
	offsets := make([]image.Point, 0, (2*radius+1)*(2*radius+1))
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radius*radius {
				offsets = append(offsets, image.Pt(dx, dy))
			}
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var max uint8
			for _, o := range offsets {
				xx, yy := x+o.X, y+o.Y
				if xx >= 0 && xx < w && yy >= 0 && yy < h {
					v := in.Pix[yy*in.Stride+xx]
					if v > max {
						max = v
						if max == 255 {
							break
						}
					}
				}
			}
			out.Pix[y*out.Stride+x] = max
		}
	}
	return out, nil
}

// RenderR32C3LatinLabelExperimental joins the existing experimental Go
// modules without invoking the Python backend. It is not production-ready:
// source hinting, FreeType vector strokes, accents and all-family parity are
// required before any accepted graphics recipe may depend on this function.
func (f *SFNT) RenderR32C3LatinLabelExperimental(text string, spec R32C3FitSpec,
	warp R32C3Warp, comp R32C3CompositeSpec, samples int) (*image.NRGBA, R32C3FitChoice, error) {
	var no R32C3FitChoice
	if f == nil || warp.Factor != spec.Factor || comp.Factor != spec.Factor || comp.Width != spec.Width || comp.Height != spec.Height || comp.AccentsEnabled {
		return nil, no, ErrInvalidRaster
	}
	fit, err := FitR32C3MaskPair(text, spec, func(px int) (image.Rectangle, error) { return f.MeasureLatinTextBBoxUnhinted(text, px) })
	if err != nil {
		return nil, no, err
	}
	inner, outer := spec.InnerWidth, spec.StrokeWidth
	if spec.OuterWidth != nil {
		outer = *spec.OuterWidth
	}
	masks, err := f.RasterR32C3LatinMasksExperimental(text, fit, inner*spec.Factor, outer*spec.Factor, samples)
	if err != nil {
		return nil, no, err
	}
	warp.HorizontalScale = fit.HorizontalScale
	warp.VerticalScale = spec.VerticalScale
	if warp.VerticalScale == 0 {
		warp.VerticalScale = 1
	}
	warp.ShearX = spec.ShearX
	comp.InnerEnabled = inner > 0
	comp.OuterEnabled = outer > 0
	out, err := WarpAndCompositeR32C3Masks(masks, warp, comp)
	if err != nil {
		return nil, no, err
	}
	return out, fit, nil
}
