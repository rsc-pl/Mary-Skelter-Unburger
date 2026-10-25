package graphicsgo

// A source-independent research approximation to vector strokes by subpixel
// Euclidean distance to the ink silhouette. This is NOT FreeType's outline
// stroker and does not resolve face AA, hinting or shaping parity. The
// accepted production renderer must not call it without complete family QA.

import (
	"image"
	"math"
)

const subpixelStrokeMaxPixels = 4_000_000

// squaredDistance1D implements the exact squared Euclidean distance
// transform of f[i]+(x-i)^2 in linear time. No recursive searches or shader
// dependencies; f >= 0 with a finite large distance for absent source ink.
func squaredDistance1D(f []int, dst []int) {
	const inf = 1 << 27
	n := len(f)
	v := make([]int, n)
	z := make([]float64, n+1)
	k := 0
	v[0] = 0
	z[0] = math.Inf(-1)
	z[1] = math.Inf(1)
	for q := 1; q < n; q++ {
		var s float64
		for {
			vk := v[k]
			s = float64((f[q]+q*q)-(f[vk]+vk*vk)) / float64(2*(q-vk))
			if s > z[k] || k == 0 {
				break
			}
			k--
		}
		k++
		v[k] = q
		z[k] = s
		z[k+1] = math.Inf(1)
	}
	k = 0
	for q := 0; q < n; q++ {
		for z[k+1] < float64(q) {
			k++
		}
		d := q - v[k]
		val := f[v[k]] + d*d
		if val > inf {
			val = inf
		}
		dst[q] = val
	}
}

// SubpixelStrokeMasksR32C3Experimental builds inner/outer *filled* stroke
// masks from an already generated face mask, matching the R32C3 relationship
// outerRadius = innerWidth + outerWidth. This is not a stroke-only ring.
//
// Factor 4 is presently the only measured candidate. Upscale: native Pillow-
// matched Lanczos; use >127 as the subpixel ink seed; Euclidean EDT in 2D;
// downscale by uniform pixel-area BOX. Inputs are never mutated.
func SubpixelStrokeMasksR32C3Experimental(face *image.Alpha, innerRadius, outerRadius, factor int) (inner, outer *image.Alpha, err error) {
	if err = validateAlpha(face); err != nil {
		return nil, nil, err
	}
	w, h := face.Rect.Dx(), face.Rect.Dy()
	if w < 1 || h < 1 || factor != 4 || innerRadius < 0 || outerRadius < 0 || outerRadius < innerRadius || outerRadius > 128 ||
		int64(w)*int64(h)*int64(factor*factor) > subpixelStrokeMaxPixels || w*factor > 8192 || h*factor > 8192 {
		return nil, nil, ErrInvalidRaster
	}
	if innerRadius == 0 && outerRadius == 0 {
		cp := image.NewAlpha(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			copy(cp.Pix[y*cp.Stride:y*cp.Stride+w], face.Pix[y*face.Stride:y*face.Stride+w])
		}
		return cp, cp, nil
	}
	hi, err := ResizeLanczosAlpha(face, w*factor, h*factor)
	if err != nil {
		return nil, nil, err
	}
	hw, hh := hi.Rect.Dx(), hi.Rect.Dy()
	const inf = 1 << 27
	distances := make([]int, hw*hh)
	line := make([]int, max(hw, hh))
	result := make([]int, max(hw, hh))
	seeds := 0
	for y := 0; y < hh; y++ {
		for x := 0; x < hw; x++ {
			if hi.Pix[y*hi.Stride+x] > 127 {
				line[x] = 0
				seeds++
			} else {
				line[x] = inf
			}
		}
		squaredDistance1D(line[:hw], result[:hw])
		copy(distances[y*hw:y*hw+hw], result[:hw])
	}
	if seeds == 0 {
		return nil, nil, ErrInvalidRaster
	}
	for x := 0; x < hw; x++ {
		for y := 0; y < hh; y++ {
			line[y] = distances[y*hw+x]
		}
		squaredDistance1D(line[:hh], result[:hh])
		for y := 0; y < hh; y++ {
			distances[y*hw+x] = result[y]
		}
	}
	// The two masks are generated from the same distance field. Pixel average
	// is calculated before the 8-bit rounding to avoid repeated degradation.
	makeMask := func(radius int) *image.Alpha {
		if radius == 0 {
			out := image.NewAlpha(image.Rect(0, 0, w, h))
			for y := 0; y < h; y++ {
				copy(out.Pix[y*out.Stride:y*out.Stride+w], face.Pix[y*face.Stride:y*face.Stride+w])
			}
			return out
		}
		threshold := (radius * factor) * (radius * factor)
		out := image.NewAlpha(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				count := 0
				for j := 0; j < factor; j++ {
					start := (y*factor+j)*hw + x*factor
					for i := 0; i < factor; i++ {
						if distances[start+i] <= threshold {
							count++
						}
					}
				}
				out.Pix[y*out.Stride+x] = uint8((count*255 + factor*factor/2) / (factor * factor))
			}
		}
		return out
	}
	inner = makeMask(innerRadius)
	if outerRadius == innerRadius {
		outer = inner
	} else {
		outer = makeMask(outerRadius)
	}
	return inner, outer, nil
}

// RenderR32C3LatinLabelSubpixelStrokeExperimental is an offline, opt-in
// comparison target. It keeps all face rasterization, fit, warp, gradient,
// shadow and output geometry unchanged; ONLY inner/outer mask construction
// differs. It must not be wired to the production graphics builder.
func (f *SFNT) RenderR32C3LatinLabelSubpixelStrokeExperimental(text string, spec R32C3FitSpec,
	warp R32C3Warp, comp R32C3CompositeSpec, samples int) (*image.NRGBA, R32C3FitChoice, error) {
	no := R32C3FitChoice{}
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
	masks, err := f.RasterR32C3LatinMasksExperimental(text, fit, 0, 0, samples)
	if err != nil {
		return nil, no, err
	}
	if inner > 0 || outer > 0 {
		masks.Inner, masks.Outer, err = SubpixelStrokeMasksR32C3Experimental(masks.Face, inner*spec.Factor, (inner+outer)*spec.Factor, 4)
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
