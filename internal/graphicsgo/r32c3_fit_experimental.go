package graphicsgo

import (
	"errors"
	"image"
	"math"
	"unicode/utf8"
)

// R32C3FitSpec is a source-order port of _fit_mask_pair's geometry decisions.
// This is a RESEARCH helper. Its caller must provide source-faithful font
// textbbox measurements; it does not assert FreeType metrics/shaping parity.
// An absent OuterWidth uses StrokeWidth; a present zero overrides it.
type R32C3FitSpec struct {
	Width, Height, Factor                                      int
	NominalSize, MinSize                                       int
	InnerWidth, StrokeWidth                                    int
	OuterWidth                                                 *int
	HorizontalScale, MinHorizontalScale, VerticalScale, ShearX float64
}

type R32C3FitChoice struct {
	NominalSize, FontPixelSize int
	TextBBox                   image.Rectangle
	Pad                        int
	MaskSize                   image.Point
	HorizontalScale            float64
	UsedFallback               bool
}

var ErrR32C3NoFit = errors.New("graphicsgo: R32C3 text cannot fit its declared slot/scale floor")

// FitR32C3MaskPair reproduces the existing R32C3 normal-fit-first policy:
// exhaust every size at the declared horizontal scale BEFORE trying any
// smaller horizontal scale down to MinHorizontalScale. A fallback cannot
// silently displace a smaller but fully fitting nominal text size.
// bbox(pixelSize) should return the ImageDraw.textbbox(...,stroke_width=0)
// for the desired text/face; the same selected bbox is reused for all strokes.
func FitR32C3MaskPair(text string, p R32C3FitSpec, bbox func(pixelSize int) (image.Rectangle, error)) (R32C3FitChoice, error) {
	bad := R32C3FitChoice{}
	if !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > 128 || bbox == nil ||
		p.Width < 10 || p.Height < 10 || p.Width > 2048 || p.Height > 2048 ||
		p.Factor < 4 || p.Factor > 16 || p.NominalSize < 1 || p.NominalSize > 512 ||
		p.MinSize < 0 || p.MinSize > 512 || p.InnerWidth < 0 || p.InnerWidth > 256 ||
		p.StrokeWidth < 0 || p.StrokeWidth > 256 ||
		(p.OuterWidth != nil && (*p.OuterWidth < 0 || *p.OuterWidth > 256)) {
		return bad, ErrInvalidRaster
	}
	sx, minSX, sy := p.HorizontalScale, p.MinHorizontalScale, p.VerticalScale
	if sx == 0 {
		sx = 1
	}
	if minSX == 0 {
		minSX = sx
	}
	if sy == 0 {
		sy = 1
	}
	if !finiteWarp(sx) || !finiteWarp(minSX) || !finiteWarp(sy) || !finiteWarp(p.ShearX) ||
		sx <= 0 || sx > 16 || minSX <= 0 || minSX > sx || sy <= 0 || sy > 16 || math.Abs(p.ShearX) > 8 {
		return bad, ErrInvalidRaster
	}
	minSize := p.MinSize
	if minSize == 0 {
		minSize = 16
	}
	ring := p.InnerWidth + p.StrokeWidth
	if p.OuterWidth != nil {
		ring = p.InnerWidth + *p.OuterWidth
	}
	px := r32Max(2, int(float64(p.Width)*0.025)) * p.Factor
	py := r32Max(2, int(float64(p.Height)*0.05)) * p.Factor
	// Python's original size sweep must be the same during BOTH passes.
	// Cache by nominal size: querying a different set of bboxes on pass two
	// can falsely 'fit' non-deterministic measurement providers.
	type result struct {
		box image.Rectangle
		err error
	}
	cache := make(map[int]result)
	for pass := 0; pass < 2; pass++ {
		for sz := p.NominalSize; sz >= minSize; sz-- {
			r, ok := cache[sz]
			if !ok {
				box, err := bbox(sz * p.Factor)
				r = result{box, err}
				cache[sz] = r
			}
			if r.err != nil {
				return bad, r.err
			}
			bb := r.box
			tw, th := bb.Dx()+2*ring*p.Factor, bb.Dy()+2*ring*p.Factor
			if bb.Empty() || tw <= 0 || th <= 0 || int64(bb.Dx()+2*ring*p.Factor)*int64(bb.Dy()+2*ring*p.Factor) > 24_000_000 {
				return bad, ErrInvalidRaster
			}
			if float64(th)*sy+float64(4*p.Factor) > float64(p.Height*p.Factor-2*py) {
				continue
			}
			available := float64(p.Width*p.Factor-2*px-4*p.Factor) - math.Abs(p.ShearX)*float64(th)*sy
			if available <= 0 {
				continue
			}
			fitSX := sx
			if pass != 0 {
				fitSX = math.Min(sx, available/float64(tw))
			}
			if fitSX < minSX-1e-8 || float64(tw)*fitSX > available+1e-6 {
				continue
			}
			pad := r32Max(16*p.Factor, (sz/2+ring+10)*p.Factor)
			maskW, maskH := bb.Dx()+2*pad, bb.Dy()+2*pad
			if maskW <= 0 || maskH <= 0 || int64(maskW)*int64(maskH) > 24_000_000 {
				return bad, ErrInvalidRaster
			}
			return R32C3FitChoice{
				NominalSize: sz, FontPixelSize: sz * p.Factor, TextBBox: bb,
				Pad: pad, MaskSize: image.Pt(maskW, maskH),
				HorizontalScale: fitSX, UsedFallback: pass != 0,
			}, nil
		}
	}
	return bad, ErrR32C3NoFit
}
