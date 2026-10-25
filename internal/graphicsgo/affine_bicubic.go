package graphicsgo

import (
	"image"
	"math"
)

// AffineBicubicNRGBA applies Pillow 12.3.0's inverse-mapped AFFINE/BICUBIC
// transformation to an RGBA image. Matrix maps destination pixel centers
// to source coordinates: [a,b,c,d,e,f]. Unsupported/invalid geometry fails
// closed. The source is not modified. Output outside the source is transparent.
// This is a research primitive, NOT used by the production graphics builder.
func AffineBicubicNRGBA(src *image.NRGBA, dstW, dstH int, matrix [6]float64) (*image.NRGBA, error) {
	if err := validateNRGBA(src); err != nil {
		return nil, err
	}
	if !validAffine(src.Rect.Dx(), src.Rect.Dy(), dstW, dstH, matrix) {
		return nil, ErrInvalidRaster
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	// Pillow converts RGBA to 8-bit premultiplied RGBa before a BICUBIC
	// transform and then back to straight alpha. Preserve this quantization.
	pre := make([]byte, 4*w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*src.Stride + 4*x
			j := 4 * (y*w + x)
			a := src.Pix[i+3]
			for c := 0; c < 3; c++ {
				pre[j+c] = pillowChannelPremultiply(src.Pix[i+c], a)
			}
			pre[j+3] = a
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			sx := matrix[0]*(float64(x)+0.5) + matrix[1]*(float64(y)+0.5) + matrix[2]
			sy := matrix[3]*(float64(x)+0.5) + matrix[4]*(float64(y)+0.5) + matrix[5]
			if sx < 0 || sy < 0 || sx >= float64(w) || sy >= float64(h) {
				continue
			}
			off := y*dst.Stride + 4*x
			for c := 0; c < 4; c++ {
				dst.Pix[off+c] = affineSample(pre, w, h, 4, c, sx, sy)
			}
			a := dst.Pix[off+3]
			for c := 0; c < 3; c++ {
				dst.Pix[off+c] = pillowChannelUnpremultiply(dst.Pix[off+c], a)
			}
		}
	}
	return dst, nil
}

// AffineBicubicAlpha applies the same transform to a Pillow L-mode 8-bit mask.
func AffineBicubicAlpha(src *image.Alpha, dstW, dstH int, matrix [6]float64) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	if !validAffine(src.Rect.Dx(), src.Rect.Dy(), dstW, dstH, matrix) {
		return nil, ErrInvalidRaster
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	// Normalize the input; the source can be a Go subimage with an offset rect.
	mask := make([]byte, w*h)
	for y := 0; y < h; y++ {
		copy(mask[y*w:y*w+w], src.Pix[y*src.Stride:y*src.Stride+w])
	}
	out := image.NewAlpha(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			sx := matrix[0]*(float64(x)+0.5) + matrix[1]*(float64(y)+0.5) + matrix[2]
			sy := matrix[3]*(float64(x)+0.5) + matrix[4]*(float64(y)+0.5) + matrix[5]
			if sx < 0 || sy < 0 || sx >= float64(w) || sy >= float64(h) {
				continue
			}
			out.Pix[y*out.Stride+x] = affineSample(mask, w, h, 1, 0, sx, sy)
		}
	}
	return out, nil
}

func validAffine(w, h, dw, dh int, m [6]float64) bool {
	if w < 1 || h < 1 || dw < 1 || dh < 1 || w > 32768 || h > 32768 || dw > 32768 || dh > 32768 || int64(w)*int64(h) > 64_000_000 || int64(dw)*int64(dh) > 64_000_000 {
		return false
	}
	for _, v := range m {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 1e9 {
			return false
		}
	}
	return true
}

func affineCubic(a, b, c, d, t float64) float64 {
	p1 := b
	p2 := -a + c
	p3 := 2*(a-b) + c - d
	p4 := -a + b - c + d
	return p1 + t*(p2+t*(p3+t*p4))
}
func affineClamp(x, lo, hi int) int {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
func affineSample(src []byte, w, h, step, channel int, sx, sy float64) byte {
	// As in Pillow's geometry kernel, pixel centers are at n+.5. Cubic
	// neighborhoods are clamped at X, while missing Y rows reuse the previous
	// cubic interpolation row. Perform arithmetic in double precision.
	tx, ty := sx-0.5, sy-0.5
	ix, iy := int(math.Floor(tx))-1, int(math.Floor(ty))-1
	dx, dy := tx-math.Floor(tx), ty-math.Floor(ty)
	var rows [4]float64
	for r := 0; r < 4; r++ {
		yy := iy + r
		if r > 0 && (yy < 0 || yy >= h) {
			rows[r] = rows[r-1]
			continue
		}
		yy = affineClamp(yy, 0, h-1)
		var q [4]float64
		for k := 0; k < 4; k++ {
			xx := affineClamp(ix+k, 0, w-1)
			q[k] = float64(src[(yy*w+xx)*step+channel])
		}
		rows[r] = affineCubic(q[0], q[1], q[2], q[3], dx)
	}
	value := affineCubic(rows[0], rows[1], rows[2], rows[3], dy)
	if value <= 0 {
		return 0
	}
	if value >= 255 {
		return 255
	}
	return byte(value)
}
