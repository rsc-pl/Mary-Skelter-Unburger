package graphicsgo

import (
	"image"
	"math"
)

// RotateBicubicNRGBAExpand reproduces Pillow rotate(degrees,
// resample=BICUBIC, expand=True) with default center and transparent fill.
// Right-angle rotations follow Pillow's exact transpose fast paths.
func RotateBicubicNRGBAExpand(src *image.NRGBA, degrees float64) (*image.NRGBA, error) {
	if err := validateNRGBA(src); err != nil {
		return nil, err
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh, m, quarter, err := pillowExpandedRotation(w, h, degrees)
	if err != nil {
		return nil, err
	}
	if quarter >= 0 {
		return quarterTurnNRGBA(src, quarter), nil
	}
	return AffineBicubicNRGBA(src, dw, dh, m)
}

// RotateBicubicAlphaExpand matches the same Pillow call on an 8-bit L mask.
func RotateBicubicAlphaExpand(src *image.Alpha, degrees float64) (*image.Alpha, error) {
	if err := validateAlpha(src); err != nil {
		return nil, err
	}
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh, m, quarter, err := pillowExpandedRotation(w, h, degrees)
	if err != nil {
		return nil, err
	}
	if quarter >= 0 {
		return quarterTurnAlpha(src, quarter), nil
	}
	return AffineBicubicAlpha(src, dw, dh, m)
}

func pillowExpandedRotation(w, h int, degrees float64) (int, int, [6]float64, int, error) {
	if math.IsInf(degrees, 0) || math.IsNaN(degrees) || math.Abs(degrees) > 1e9 {
		return 0, 0, [6]float64{}, -1, ErrInvalidRaster
	}
	angle := math.Mod(degrees, 360)
	if angle < 0 {
		angle += 360
	}
	if angle == 0 {
		return w, h, [6]float64{}, 0, nil
	}
	if angle == 90 {
		return h, w, [6]float64{}, 1, nil
	}
	if angle == 180 {
		return w, h, [6]float64{}, 2, nil
	}
	if angle == 270 {
		return h, w, [6]float64{}, 3, nil
	}
	rad := -angle * (math.Pi / 180)
	r15 := func(v float64) float64 { return math.RoundToEven(v*1e15) / 1e15 }
	m := [6]float64{r15(math.Cos(rad)), r15(math.Sin(rad)), 0, r15(-math.Sin(rad)), r15(math.Cos(rad)), 0}
	cx, cy := float64(w)/2, float64(h)/2
	m[2] = m[0]*-cx + m[1]*-cy + cx
	m[5] = m[3]*-cx + m[4]*-cy + cy
	points := [4][2]float64{{0, 0}, {float64(w), 0}, {float64(w), float64(h)}, {0, float64(h)}}
	minx, miny := math.Inf(1), math.Inf(1)
	maxx, maxy := math.Inf(-1), math.Inf(-1)
	for _, p := range points {
		x := m[0]*p[0] + m[1]*p[1] + m[2]
		y := m[3]*p[0] + m[4]*p[1] + m[5]
		minx = math.Min(minx, x)
		miny = math.Min(miny, y)
		maxx = math.Max(maxx, x)
		maxy = math.Max(maxy, y)
	}
	dw, dh := int(math.Ceil(maxx)-math.Floor(minx)), int(math.Ceil(maxy)-math.Floor(miny))
	if !validAffine(w, h, dw, dh, m) {
		return 0, 0, [6]float64{}, -1, ErrInvalidRaster
	}
	shiftx, shifty := -(float64(dw-w))/2, -(float64(dh-h))/2
	m[2], m[5] = m[0]*shiftx+m[1]*shifty+m[2], m[3]*shiftx+m[4]*shifty+m[5]
	return dw, dh, m, -1, nil
}

func quarterCoords(x, y, w, h, quarter int) (int, int) {
	switch quarter {
	case 1:
		return y, w - 1 - x
	case 2:
		return w - 1 - x, h - 1 - y
	case 3:
		return h - 1 - y, x
	}
	return x, y
}
func quarterTurnNRGBA(src *image.NRGBA, quarter int) *image.NRGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if quarter%2 == 1 {
		dw, dh = h, w
	}
	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			xx, yy := quarterCoords(x, y, w, h, quarter)
			copy(out.Pix[yy*out.Stride+4*xx:yy*out.Stride+4*xx+4], src.Pix[y*src.Stride+4*x:y*src.Stride+4*x+4])
		}
	}
	return out
}
func quarterTurnAlpha(src *image.Alpha, quarter int) *image.Alpha {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dw, dh := w, h
	if quarter%2 == 1 {
		dw, dh = h, w
	}
	out := image.NewAlpha(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			xx, yy := quarterCoords(x, y, w, h, quarter)
			out.Pix[yy*out.Stride+xx] = src.Pix[y*src.Stride+x]
		}
	}
	return out
}
