package graphicsgo

// This is an isolated, opt-in CFF1 stem-grid-fit pilot. It is NOT Adobe/FreeType
// hinting, and is intentionally disconnected from the accepted text/graphic
// builders. It rejects masks changing the active stems until contour segments
// can be associated with their Type2 hint mask state. All coordinates here are
// in font units, with Y increasing upward.
import (
	"image"
	"math"
	"sort"
)

type cffSnapAnchor struct{ original, snapped float64 }

func cffStemAnchors(stems []CFFHintStem, axis string, scale float64) ([]cffSnapAnchor, error) {
	var a []cffSnapAnchor
	intervals := make([][2]float64, 0, len(stems))
	for _, stem := range stems {
		if stem.Axis != axis {
			continue
		}
		low, high := math.Min(stem.Start, stem.End)*scale, math.Max(stem.Start, stem.End)*scale
		if math.IsNaN(low) || math.IsInf(low, 0) || math.IsNaN(high) || math.IsInf(high, 0) || high <= low || high-low > 512 {
			return nil, fontErr("CFF grid fit invalid stem")
		}
		for _, previous := range intervals {
			if low < previous[1]-1e-6 && high > previous[0]+1e-6 {
				return nil, fontErr("CFF grid fit overlapping source stems")
			}
		}
		intervals = append(intervals, [2]float64{low, high})
		width := math.Max(1, math.Round(high-low))
		left := math.Round((low + high - width) / 2)
		a = append(a, cffSnapAnchor{low, left}, cffSnapAnchor{high, left + width})
	}
	sort.SliceStable(a, func(i, j int) bool { return a[i].original < a[j].original })
	// Reject competing anchors, rather than silently introducing a folding or
	// non-monotonic transform that would change the winding of the glyph.
	for i := 1; i < len(a); i++ {
		if a[i].original-a[i-1].original < 1e-6 {
			return nil, fontErr("CFF grid fit overlapping stem anchors")
		}
		if a[i].snapped < a[i-1].snapped {
			return nil, fontErr("CFF grid fit non-monotonic anchors")
		}
	}
	return a, nil
}

// cffGridCoordinate interpolates *device*-pixel coordinates between source
// stem edges. Outside the supported intervals it translates by the nearest
// snapped edge instead of changing the glyph's nominal advance or origin.
func cffGridCoordinate(v float64, a []cffSnapAnchor) float64 {
	if len(a) == 0 {
		return v
	}
	if v <= a[0].original {
		return v + a[0].snapped - a[0].original
	}
	last := a[len(a)-1]
	if v >= last.original {
		return v + last.snapped - last.original
	}
	i := sort.Search(len(a), func(i int) bool { return a[i].original >= v })
	left, right := a[i-1], a[i]
	t := (v - left.original) / (right.original - left.original)
	return left.snapped + (right.snapped-left.snapped)*t
}

// CFFStemGridFitOutlineExperimental moves source glyph points into a snapped
// font-unit outline, preserving contour point count and rejecting unsupported
// active-mask combinations. It never mutates the source outline or font.
func (f *SFNT) CFFStemGridFitOutlineExperimental(gid uint16, px float64) (GlyphOutline, error) {
	return f.cffStemGridFitOutline(gid, px, true)
}

// Light CFF grid-fit research variant: only Y is changed (as observed in the
// reference FreeType light CFF raster on the selected private fonts).
func (f *SFNT) CFFStemGridFitOutlineYExperimental(gid uint16, px float64) (GlyphOutline, error) {
	return f.cffStemGridFitOutline(gid, px, false)
}

func (f *SFNT) cffStemGridFitOutline(gid uint16, px float64, snapX bool) (GlyphOutline, error) {
	if f == nil || f.Outline != CFF1Outline || px < 4 || px > 512 || math.IsNaN(px) || math.IsInf(px, 0) || f.UnitsPerEm == 0 {
		return nil, fontErr("CFF grid fit invalid source")
	}
	plan, err := f.GlyphCFFHintPlan(gid)
	if err != nil {
		return nil, err
	}
	if len(plan.Stems) > 96 {
		return nil, fontErr("CFF grid fit stem limit")
	}
	for _, mask := range plan.Masks {
		if mask.Counter || len(mask.Active) != len(plan.Stems) {
			return nil, fontErr("CFF grid fit dynamic/counter mask unsupported")
		}
		for i, active := range mask.Active {
			if i != active {
				return nil, fontErr("CFF grid fit sparse mask unsupported")
			}
		}
	}
	source, err := f.GlyphOutlineCFF1(gid)
	if err != nil {
		return nil, err
	}
	scale := px / float64(f.UnitsPerEm)
	var xAnchors []cffSnapAnchor
	if snapX {
		xAnchors, err = cffStemAnchors(plan.Stems, "x", scale)
		if err != nil {
			return nil, err
		}
	}
	yAnchors, err := cffStemAnchors(plan.Stems, "y", scale)
	if err != nil {
		return nil, err
	}
	out := make(GlyphOutline, len(source))
	for j, contour := range source {
		out[j] = make(GlyphContour, len(contour))
		for k, pt := range contour {
			out[j][k] = GlyphPoint{X: cffGridCoordinate(pt.X*scale, xAnchors) / scale, Y: cffGridCoordinate(pt.Y*scale, yAnchors) / scale, On: pt.On}
		}
	}
	return out, nil
}

// RasterGlyphCFFStemGridExperimental allows independent numerical comparison
// to the FreeType raster, without ever entering the production renderer.
func (f *SFNT) RasterGlyphCFFStemGridExperimental(gid uint16, px float64) (*image.Alpha, image.Point, error) {
	c, err := f.CFFStemGridFitOutlineExperimental(gid, px)
	if err != nil {
		return nil, image.Point{}, err
	}
	return rasterContoursNonzeroArea(c, px, float64(f.UnitsPerEm))
}

func (f *SFNT) RasterGlyphCFFStemGridYExperimental(gid uint16, px float64) (*image.Alpha, image.Point, error) {
	c, err := f.CFFStemGridFitOutlineYExperimental(gid, px)
	if err != nil {
		return nil, image.Point{}, err
	}
	return rasterContoursNonzeroArea(c, px, float64(f.UnitsPerEm))
}

// CFFBlueAnchorOutlineExperimental is a separate blue-zone experiment. It
// respects the source CFF Private DICT but does not attempt full stem execution.
// Only actual glyph vertical extrema within a declared bottom/top zone are
// snapped; everything else is linearly interpolated from those two anchors.
// The original source outline is returned unchanged if no blue anchor matches.
func (f *SFNT) CFFBlueAnchorOutlineExperimental(gid uint16, px float64) (GlyphOutline, error) {
	if f == nil || f.Outline != CFF1Outline || f.UnitsPerEm == 0 || px < 4 || px > 512 || math.IsNaN(px) || math.IsInf(px, 0) {
		return nil, fontErr("CFF blue anchor invalid source")
	}
	plan, err := f.GlyphCFFHintPlan(gid)
	if err != nil {
		return nil, err
	}
	src, err := f.GlyphOutlineCFF1(gid)
	if err != nil {
		return nil, err
	}
	// Do not impose blue-zone grid-fitting when Type2 source supplies no stems.
	// Such outlines are not safe evidence for this partial hint pilot.
	if len(plan.Stems) == 0 {
		return src, nil
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, contour := range src {
		for _, pt := range contour {
			lo = math.Min(lo, pt.Y)
			hi = math.Max(hi, pt.Y)
		}
	}
	if math.IsInf(lo, 0) {
		return src, nil
	}
	scale := px / float64(f.UnitsPerEm)
	var anchors []cffSnapAnchor
	zones := [][]float64{plan.Private.BlueValues, plan.Private.OtherBlues}
	fuzz := plan.Private.BlueFuzz
	if math.IsNaN(fuzz) || math.IsInf(fuzz, 0) || fuzz < 0 || fuzz > 100 {
		return nil, fontErr("CFF blue fuzz invalid")
	}
	for _, extreme := range []float64{lo, hi} {
		for _, pairs := range zones {
			for i := 0; i+1 < len(pairs); i += 2 {
				if extreme >= pairs[i]-fuzz && extreme <= pairs[i+1]+fuzz {
					v := extreme * scale
					anchors = append(anchors, cffSnapAnchor{original: v, snapped: math.Round(v)})
					goto nextExtreme
				}
			}
		}
	nextExtreme:
	}
	if len(anchors) == 0 {
		return src, nil
	}
	sort.Slice(anchors, func(i, j int) bool { return anchors[i].original < anchors[j].original })
	if len(anchors) == 2 && math.Abs(anchors[0].original-anchors[1].original) < 1e-7 {
		anchors = anchors[:1]
	}
	out := make(GlyphOutline, len(src))
	for j, contour := range src {
		out[j] = make(GlyphContour, len(contour))
		for k, pt := range contour {
			out[j][k] = GlyphPoint{X: pt.X, Y: cffGridCoordinate(pt.Y*scale, anchors) / scale, On: pt.On}
		}
	}
	return out, nil
}
func (f *SFNT) RasterGlyphCFFBlueAnchorExperimental(gid uint16, px float64) (*image.Alpha, image.Point, error) {
	c, err := f.CFFBlueAnchorOutlineExperimental(gid, px)
	if err != nil {
		return nil, image.Point{}, err
	}
	return rasterContoursNonzeroArea(c, px, float64(f.UnitsPerEm))
}
