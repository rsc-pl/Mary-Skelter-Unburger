package graphicsgo

import (
	"image"
	"math"
)

// R32C3Warp is the source-order geometry contract from the accepted
// r32c3_typography._warp_mask recipe. These values are preselected by the
// caller's fit logic; this does not rasterize or shape any font.
type R32C3Warp struct {
	HorizontalScale float64
	VerticalScale   float64
	ShearX          float64
	RotationDegrees float64
	Factor          int
}

// WarpR32C3Alpha composes the existing native-Go Pillow-compatible kernels
// in the exact order used by the legacy accepted R32C3 script:
// LANCZOS resize, BICUBIC affine shear, BICUBIC expanded rotation.
// This is research-only and deliberately not wired into game builds.
func WarpR32C3Alpha(src *image.Alpha, p R32C3Warp) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	if p.Factor < 4 || p.Factor > 16 || !finiteWarp(p.HorizontalScale) || !finiteWarp(p.VerticalScale) ||
		!finiteWarp(p.ShearX) || !finiteWarp(p.RotationDegrees) || p.HorizontalScale <= 0 || p.VerticalScale <= 0 ||
		p.HorizontalScale > 16 || p.VerticalScale > 16 || math.Abs(p.ShearX) > 8 || math.Abs(p.RotationDegrees) > 360000 {
		return nil, ErrInvalidRaster
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	copySource := image.NewAlpha(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		copy(copySource.Pix[y*copySource.Stride:y*copySource.Stride+w], src.Pix[y*src.Stride:y*src.Stride+w])
	}
	_, nonempty, err := BBoxAlpha(src)
	if err != nil {
		return nil, err
	}
	if !nonempty {
		return copySource, nil
	} // Pillow _warp_mask returns unchanged transparent source

	nw := int(math.RoundToEven(float64(w) * p.HorizontalScale))
	nh := int(math.RoundToEven(float64(h) * p.VerticalScale))
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	if nw > 32768 || nh > 32768 || int64(nw)*int64(nh) > 64_000_000 {
		return nil, ErrInvalidRaster
	}
	out, err := ResizeLanczosAlpha(copySource, nw, nh)
	if err != nil {
		return nil, err
	}
	if math.Abs(p.ShearX) > 1e-6 {
		extraF := math.Ceil(math.Abs(p.ShearX)*float64(out.Rect.Dy())) + float64(8*p.Factor)
		if extraF > 32768 || int64(nw+int(extraF))*int64(nh) > 64_000_000 {
			return nil, ErrInvalidRaster
		}
		extra := int(extraF)
		// Preserve Python -extra//2 floor division (NOT Go's truncation
		// toward zero for odd positive extra).
		offset := -float64((extra + 1) / 2)
		out, err = AffineBicubicAlpha(out, nw+extra, nh, [6]float64{1, -p.ShearX, offset, 0, 1, 0})
		if err != nil {
			return nil, err
		}
	}
	if math.Abs(p.RotationDegrees) > 1e-6 {
		return RotateBicubicAlphaExpand(out, p.RotationDegrees)
	}
	return out, nil
}

func finiteWarp(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }
