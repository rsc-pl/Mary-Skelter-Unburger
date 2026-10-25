package graphicsgo

// Research-only native-Go implementation of Pillow's 8-bit L-mode Lanczos-3
// resampling convention (center alignment, edge normalization, fixed-point
// coefficients, intermediate 8-bit quantization, and horizontal-before-vertical intermediate quantization).
// This does not imply that RGBA resampling, font hinting or output atlases are
// production ready. It deliberately consumes an already-rendered alpha mask.
import (
	"fmt"
	"image"
	"math"
)

const lanczosPrecision = 22

type lanczosSpan struct {
	lo      int
	weights []int64
}

func lanczosWeight(x float64) float64 {
	if x <= -3 || x >= 3 {
		return 0
	}
	if x == 0 {
		return 1
	}
	piX := x * math.Pi
	return (math.Sin(piX) / piX) * (math.Sin(piX/3) / (piX / 3))
}
func lanczosSpans(srcN, dstN int) ([]lanczosSpan, error) {
	if srcN <= 0 || dstN <= 0 || srcN > 32768 || dstN > 32768 {
		return nil, ErrInvalidRaster
	}
	scale := float64(srcN) / float64(dstN)
	fs := math.Max(scale, 1)
	support := 3 * fs
	spans := make([]lanczosSpan, dstN)
	for out := 0; out < dstN; out++ {
		center := (float64(out) + 0.5) * scale
		lo := int(center - support + 0.5)
		hi := int(center + support + 0.5)
		if lo < 0 {
			lo = 0
		}
		if hi > srcN {
			hi = srcN
		}
		if hi <= lo {
			return nil, fmt.Errorf("%w: empty Lanczos support", ErrInvalidRaster)
		}
		f := make([]float64, hi-lo)
		total := 0.0
		for i := range f {
			f[i] = lanczosWeight((float64(lo+i) - center + 0.5) / fs)
			total += f[i]
		}
		if total == 0 {
			return nil, ErrInvalidRaster
		}
		quant := make([]int64, len(f))
		for i, v := range f {
			quant[i] = int64(math.Round((v / total) * float64(int64(1)<<lanczosPrecision)))
		}
		spans[out] = lanczosSpan{lo, quant}
	}
	return spans, nil
}
func lanczosClip(v int64) uint8 {
	v >>= lanczosPrecision
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}
func lanczosPass(src *image.Alpha, dstW, dstH int, horizontal bool) (*image.Alpha, error) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	nSrc, nDst := h, dstH
	if horizontal {
		nSrc, nDst = w, dstW
	}
	spans, err := lanczosSpans(nSrc, nDst)
	if err != nil {
		return nil, err
	}
	dst := image.NewAlpha(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			key := y
			if horizontal {
				key = x
			}
			span := spans[key]
			sum := int64(1 << (lanczosPrecision - 1))
			for i, k := range span.weights {
				sx, sy := x, y
				if horizontal {
					sx = span.lo + i
				} else {
					sy = span.lo + i
				}
				sum += int64(src.Pix[sy*src.Stride+sx]) * k
			}
			dst.Pix[y*dst.Stride+x] = lanczosClip(sum)
		}
	}
	return dst, nil
}

// ResizeLanczosAlpha returns an independent 8-bit L-mode mask. Pixel-level
// equivalence to Image.Resampling.LANCZOS from Pillow is tested on independent
// deterministic oracles. Coordinates are local to the supplied image rectangle.
// The current implementation is deliberately not used for RGBA/premultiplied
// colors or in the accepted Mary Skelter production graphics builder.
func ResizeLanczosAlpha(src *image.Alpha, dstW, dstH int) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if w > 32768 || h > 32768 || dstW < 1 || dstH < 1 || dstW > 32768 || dstH > 32768 || int64(dstW)*int64(dstH) > 64_000_000 || int64(w)*int64(h) > 64_000_000 {
		return nil, ErrInvalidRaster
	}
	if dstW == w && dstH == h {
		dst := image.NewAlpha(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			copy(dst.Pix[y*dst.Stride:y*dst.Stride+w], src.Pix[y*src.Stride:y*src.Stride+w])
		}
		return dst, nil
	}
	// Pillow 12.3.0 performs the horizontal 8-bit pass first, then the
	// vertical pass, even when height shrinks more than width. The exact
	// order matters because intermediate pixels are quantized to uint8.
	curr := src
	var err error
	if w != dstW {
		curr, err = lanczosPass(curr, dstW, h, true)
		if err != nil {
			return nil, err
		}
	}
	if h != dstH {
		curr, err = lanczosPass(curr, dstW, dstH, false)
		if err != nil {
			return nil, err
		}
	}
	// Clone if caller supplied a stride/rectangle view without rescaling.
	return curr, nil
}
