package graphicsgo

// legacy purely opt-in research renderer. It retains all face placement,
// fitted dimensions, warp, colors, shadow/gradient compositing; ONLY the
// radial grayscale max-kernel is accelerated, with pixel-equivalence tests.
import "image"

func (f *SFNT) RenderR32C3LatinLabelFastRadial(text string, spec R32C3FitSpec,
	warp R32C3Warp, comp R32C3CompositeSpec, samples int) (*image.NRGBA, R32C3FitChoice, error) {
	var zero R32C3FitChoice
	if f == nil || warp.Factor != spec.Factor || comp.Factor != spec.Factor || comp.Width != spec.Width || comp.Height != spec.Height || comp.AccentsEnabled {
		return nil, zero, ErrInvalidRaster
	}
	fit, err := FitR32C3MaskPair(text, spec, func(px int) (image.Rectangle, error) { return f.MeasureLatinTextBBoxUnhinted(text, px) })
	if err != nil {
		return nil, zero, err
	}
	masks, err := f.RasterR32C3LatinMasksExperimental(text, fit, 0, 0, samples)
	if err != nil {
		return nil, zero, err
	}
	inner, outer := spec.InnerWidth, spec.StrokeWidth
	if spec.OuterWidth != nil {
		outer = *spec.OuterWidth
	}
	if inner > 0 {
		masks.Inner, err = RadialDilateAlphaFast(masks.Face, inner*spec.Factor)
		if err != nil {
			return nil, zero, err
		}
	}
	if outer > 0 {
		masks.Outer, err = RadialDilateAlphaFast(masks.Face, (inner+outer)*spec.Factor)
		if err != nil {
			return nil, zero, err
		}
	}
	if outer == 0 {
		masks.Outer = masks.Inner
	}
	warp.HorizontalScale = fit.HorizontalScale
	warp.VerticalScale = spec.VerticalScale
	if warp.VerticalScale == 0 {
		warp.VerticalScale = 1
	}
	warp.ShearX = spec.ShearX
	comp.InnerEnabled = inner > 0
	comp.OuterEnabled = outer > 0
	im, err := WarpAndCompositeR32C3Masks(masks, warp, comp)
	return im, fit, err
}
