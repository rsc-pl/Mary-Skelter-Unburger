package graphicsgo

import (
	"image"
	"math"
)

// ResizeLanczosNRGBA returns a new straight-alpha RGBA image with Pillow 12.3's
// RGBA LANCZOS semantics. Pillow converts RGBA to premultiplied 8-bit RGBa,
// filters all four channels with 22-bit fixed-point coefficients, then converts
// RGBa back to RGBA. This is research-only and does not alter game assets.
func ResizeLanczosNRGBA(src *image.NRGBA, dstW, dstH int) (*image.NRGBA, error) {
	return resizePremultipliedNRGBA(src, dstW, dstH, 3, lanczosWeight)
}

// ResizeBicubicNRGBA matches Pillow 12.3 RGBA bicubic resampling. This is a
// separable resize, NOT Pillow's affine BICUBIC rotate/shear/transform path.
func ResizeBicubicNRGBA(src *image.NRGBA, dstW, dstH int) (*image.NRGBA, error) {
	return resizePremultipliedNRGBA(src, dstW, dstH, 2, bicubicWeight)
}

func bicubicWeight(x float64) float64 {
	x = math.Abs(x)
	if x < 1 {
		return ((-0.5+2)*x-(-0.5+3))*x*x + 1
	}
	if x < 2 {
		return ((-0.5*x-5*(-0.5))*x+8*(-0.5))*x - 4*(-0.5)
	}
	return 0
}

// pillowChannelPremultiply is (straight*alpha+127)/255. Pillow's RGBa->RGBA
// conversion is truncating (premul*255)/alpha, capped at 255, not rounding.
// For alpha=0, Pillow leaves RGB bytes untouched in the conversion, although
// the preceding premultiplication makes them zero (when resampling).
func pillowChannelPremultiply(channel, alpha uint8) uint8 {
	return uint8((int(channel)*int(alpha) + 127) / 255)
}
func pillowChannelUnpremultiply(channel, alpha uint8) uint8 {
	if alpha == 0 {
		return channel
	}
	v := int(channel) * 255 / int(alpha)
	if v > 255 {
		v = 255
	}
	return uint8(v)
}
func colorSpans(srcN, dstN int, support float64, kernel func(float64) float64) ([]lanczosSpan, error) {
	if srcN <= 0 || dstN <= 0 || srcN > 32768 || dstN > 32768 {
		return nil, ErrInvalidRaster
	}
	scale := float64(srcN) / float64(dstN)
	fs := math.Max(scale, 1)
	radius := support * fs
	spans := make([]lanczosSpan, dstN)
	for out := 0; out < dstN; out++ {
		center := (float64(out) + 0.5) * scale
		lo := int(center - radius + 0.5)
		hi := int(center + radius + 0.5)
		if lo < 0 {
			lo = 0
		}
		if hi > srcN {
			hi = srcN
		}
		if hi <= lo {
			return nil, ErrInvalidRaster
		}
		ww := make([]float64, hi-lo)
		sum := 0.0
		for i := range ww {
			v := kernel((float64(lo+i) - center + 0.5) / fs)
			ww[i] = v
			sum += v
		}
		if sum == 0 {
			return nil, ErrInvalidRaster
		}
		weights := make([]int64, len(ww))
		for i, w := range ww {
			weights[i] = int64(math.Round(w / sum * float64(int64(1)<<lanczosPrecision)))
		}
		spans[out] = lanczosSpan{lo: lo, weights: weights}
	}
	return spans, nil
}
func colorPass(src *image.NRGBA, dstW, dstH int, horizontal bool, support float64, kernel func(float64) float64) (*image.NRGBA, error) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	nSrc, nDst := h, dstH
	if horizontal {
		nSrc, nDst = w, dstW
	}
	spans, err := colorSpans(nSrc, nDst, support, kernel)
	if err != nil {
		return nil, err
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			key := y
			if horizontal {
				key = x
			}
			span := spans[key]
			for c := 0; c < 4; c++ {
				sum := int64(1 << (lanczosPrecision - 1))
				for i, k := range span.weights {
					sx, sy := x, y
					if horizontal {
						sx = span.lo + i
					} else {
						sy = span.lo + i
					}
					sum += int64(src.Pix[sy*src.Stride+sx*4+c]) * k
				}
				dst.Pix[y*dst.Stride+x*4+c] = lanczosClip(sum)
			}
		}
	}
	return dst, nil
}
func resizePremultipliedNRGBA(src *image.NRGBA, dstW, dstH int, support float64, kernel func(float64) float64) (*image.NRGBA, error) {
	if err := validateNRGBA(src); err != nil {
		return nil, err
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if w > 32768 || h > 32768 || dstW < 1 || dstH < 1 || dstW > 32768 || dstH > 32768 || int64(w)*int64(h) > 64_000_000 || int64(dstW)*int64(dstH) > 64_000_000 {
		return nil, ErrInvalidRaster
	}
	curr := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := y*src.Stride + x*4
			di := y*curr.Stride + x*4
			a := src.Pix[si+3]
			for c := 0; c < 3; c++ {
				curr.Pix[di+c] = pillowChannelPremultiply(src.Pix[si+c], a)
			}
			curr.Pix[di+3] = a
		}
	}
	// Pillow's same-size resize fast path copies original RGBA, without
	// lossy 8-bit premultiply/unpremultiply conversion.
	if w == dstW && h == dstH {
		for y := 0; y < h; y++ {
			copy(curr.Pix[y*curr.Stride:y*curr.Stride+w*4], src.Pix[y*src.Stride:y*src.Stride+w*4])
		}
		return curr, nil
	}
	var err error
	if w != dstW {
		curr, err = colorPass(curr, dstW, h, true, support, kernel)
		if err != nil {
			return nil, err
		}
	}
	if h != dstH {
		curr, err = colorPass(curr, dstW, dstH, false, support, kernel)
		if err != nil {
			return nil, err
		}
	}
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			i := y*curr.Stride + x*4
			a := curr.Pix[i+3]
			for c := 0; c < 3; c++ {
				curr.Pix[i+c] = pillowChannelUnpremultiply(curr.Pix[i+c], a)
			}
		}
	}
	return curr, nil
}
