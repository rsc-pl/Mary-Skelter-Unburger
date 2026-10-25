package graphicsgo

// Offline opt-in whole-label experiment to test whether BlueValues point
// alignment survives the stroke, warp, shadow, gradient and final Lanczos.
// Does not alter the accepted production or default research paths.
import (
	"image"
)

// RasterR32C3LatinMasksBlueExperimental requires a CFF1 source, uses the
// source's real per-glyph BlueValues/OtherBlues and fails closed on unsupported
// font/text geometry. It does not claim FreeType hinting parity.
func (f *SFNT) RasterR32C3LatinMasksBlueExperimental(text string, fit R32C3FitChoice) (GlyphMasks, error) {
	return f.rasterR32C3LatinMasks(text, fit, 0, 0, 0, false, true)
}

// RenderR32C3LatinLabelBlueExperimental is the grid-fitting pilot
// combined with the existing experimental subpixel EDT stroke path.
func (f *SFNT) RenderR32C3LatinLabelBlueExperimental(text string, spec R32C3FitSpec,
	warp R32C3Warp, comp R32C3CompositeSpec) (*image.NRGBA, R32C3FitChoice, error) {
	no := R32C3FitChoice{}
	if f == nil || f.Outline != CFF1Outline || warp.Factor != spec.Factor || comp.Factor != spec.Factor ||
		comp.Width != spec.Width || comp.Height != spec.Height || comp.AccentsEnabled {
		return nil, no, ErrInvalidRaster
	}
	fit, err := FitR32C3MaskPair(text, spec, func(px int) (image.Rectangle, error) {
		return f.MeasureLatinTextBBoxUnhinted(text, px)
	})
	if err != nil {
		return nil, no, err
	}
	inner, outer := spec.InnerWidth, spec.StrokeWidth
	if spec.OuterWidth != nil {
		outer = *spec.OuterWidth
	}
	masks, err := f.RasterR32C3LatinMasksBlueExperimental(text, fit)
	if err != nil {
		return nil, no, err
	}
	if inner > 0 || outer > 0 {
		masks.Inner, masks.Outer, err = SubpixelStrokeMasksR32C3Experimental(
			masks.Face, inner*spec.Factor, (inner+outer)*spec.Factor, 4)
		if err != nil {
			return nil, no, err
		}
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
