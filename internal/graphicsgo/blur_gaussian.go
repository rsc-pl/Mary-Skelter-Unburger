package graphicsgo

import (
	"fmt"
	"image"
	"math"
)

// pillowGaussianBoxRadius calculates the fractional box radius of the
// three-pass, piecewise-constant Pillow GaussianBlur approximation. Explicit
// float32 steps follow the reference's C float intermediate precision.
func pillowGaussianBoxRadius(radius float32) float32 {
	if radius == 0 {
		return 0
	}
	sigma2 := radius * radius / 3
	L := float32(math.Sqrt(float64(12*sigma2 + 1)))
	l := float32(math.Floor(float64((L - 1) / 2)))
	a := (2*l + 1) * (l*(l+1) - 3*sigma2)
	a /= 6 * (sigma2 - (l+1)*(l+1))
	return l + a
}

func blurBoxAxis(src *image.Alpha, radius float32, horizontal bool) (*image.Alpha, error) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	r := int(radius)
	if r > 128 {
		return nil, fmt.Errorf("%w: blur radius exceeds bounded work", ErrInvalidRaster)
	}
	ww := uint64(float32(1<<24) / (radius*2 + 1))
	fw := (uint64(1<<24) - uint64(r*2+1)*ww) / 2
	out := image.NewAlpha(image.Rect(0, 0, w, h))
	clamp := func(v, max int) int {
		if v < 0 {
			return 0
		}
		if v >= max {
			return max - 1
		}
		return v
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			acc := uint64(0)
			n := w
			if !horizontal {
				n = h
			}
			for i := -r; i <= r; i++ {
				xx, yy := x, y
				if horizontal {
					xx = clamp(x+i, n)
				} else {
					yy = clamp(y+i, n)
				}
				acc += uint64(src.Pix[yy*src.Stride+xx])
			}
			xx0, yy0, xx1, yy1 := x, y, x, y
			if horizontal {
				xx0 = clamp(x-r-1, w)
				xx1 = clamp(x+r+1, w)
			} else {
				yy0 = clamp(y-r-1, h)
				yy1 = clamp(y+r+1, h)
			}
			far := uint64(src.Pix[yy0*src.Stride+xx0]) + uint64(src.Pix[yy1*src.Stride+xx1])
			bulk := acc*ww + far*fw
			result := (bulk + (1 << 23)) >> 24
			if result > 255 {
				result = 255
			}
			out.Pix[y*out.Stride+x] = uint8(result)
		}
	}
	return out, nil
}

// GaussianBlurAlpha is a bounded native-Go 8-bit mask blur for the specific
// ImageFilter.GaussianBlur(radius) 3-pass path used by the Python graphics
// builder. Edges are extended. Unsupported geometry and radius fail closed.
// Until independent byte parity oracles pass, this must remain research-only.
func GaussianBlurAlpha(src *image.Alpha, radius float64) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if w > 32768 || h > 32768 || int64(w)*int64(h) > 64_000_000 || math.IsNaN(radius) || math.IsInf(radius, 0) || radius < 0 || radius > 128 {
		return nil, ErrInvalidRaster
	}
	r := pillowGaussianBoxRadius(float32(radius))
	if math.IsNaN(float64(r)) || r < 0 || r > 128 {
		return nil, ErrInvalidRaster
	}
	cp := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		copy(cp.Pix[y*cp.Stride:y*cp.Stride+w], src.Pix[y*src.Stride:y*src.Stride+w])
	}
	if radius == 0 {
		return cp, nil
	}
	for i := 0; i < 3; i++ {
		next, e := blurBoxAxis(cp, r, true)
		if e != nil {
			return nil, e
		}
		cp = next
	}
	for i := 0; i < 3; i++ {
		next, e := blurBoxAxis(cp, r, false)
		if e != nil {
			return nil, e
		}
		cp = next
	}
	return cp, nil
}
