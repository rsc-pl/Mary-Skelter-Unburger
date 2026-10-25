package graphicsgo

import (
	"image"
	"math"
	"sort"
)

// RasterGlyphNonzeroArea integrates flattened contours according to the true
// nonzero winding fill rule, including overlapping outlines and counter-wound
// holes. Research only: curve flattening and FreeType hinting are separate
// unsolved fidelity gates. This does not alter the production rasterizer.
func (f *SFNT) RasterGlyphNonzeroArea(gid uint16, px float64) (*image.Alpha, image.Point, error) {
	if f == nil {
		return nil, image.Point{}, fontErr("nil font")
	}
	var c GlyphOutline
	var err error
	switch f.Outline {
	case TrueTypeOutline:
		c, err = f.GlyphOutlineTTF(gid)
	case CFF1Outline:
		c, err = f.GlyphOutlineCFF1(gid)
	default:
		return nil, image.Point{}, fontErr("unsupported outline")
	}
	if err != nil {
		return nil, image.Point{}, err
	}
	return rasterContoursNonzeroArea(c, px, float64(f.UnitsPerEm))
}

type nonzeroSegment struct {
	x0, y0, dx, dy, low, high float64
	delta                     int
}

func (s nonzeroSegment) x(y float64) float64 { return s.x0 + (y-s.y0)*s.dx/s.dy }

type nonzeroCross struct {
	x     float64
	delta int
}

func nonzeroRowCoverage(edges []fillEdge, w, y int, accumulated []float64) error {
	lower, upper := float64(y), float64(y+1)
	segs := make([]nonzeroSegment, 0, 64)
	events := []float64{lower, upper}
	for _, edge := range edges {
		a, b := edge.a, edge.b
		dy := b.y - a.y
		if dy == 0 {
			continue
		}
		low, high := math.Min(a.y, b.y), math.Max(a.y, b.y)
		if high <= lower || low >= upper {
			continue
		}
		lo, hi := math.Max(low, lower), math.Min(high, upper)
		if lo >= hi {
			continue
		}
		s := nonzeroSegment{a.x, a.y, b.x - a.x, dy, lo, hi, 1}
		if dy < 0 {
			s.delta = -1
		}
		segs = append(segs, s)
		if lo > lower {
			events = append(events, lo)
		}
		if hi < upper {
			events = append(events, hi)
		}
		// Every crossing of a vertical pixel boundary is a potential change of
		// piecewise-linear clipped width. Include it to make midpoint integration
		// exact within each resulting y slab.
		xa, xb := s.x(lo), s.x(hi)
		left, right := math.Min(xa, xb), math.Max(xa, xb)
		if right-left > 1e-12 {
			for k := max(0, int(math.Ceil(left))); k <= min(w, int(math.Floor(right))); k++ {
				v := float64(k)
				yy := s.y0 + (v-s.x0)*s.dy/s.dx
				if yy > lo+1e-10 && yy < hi-1e-10 {
					events = append(events, yy)
				}
			}
		}
	}
	if len(segs) > 512 {
		return fontErr("nonzero active-edge budget")
	}
	// Edge order changes only when two segments cross. Those y-coordinates must
	// also split slabs, including intersections across unrelated contours.
	for i := 0; i < len(segs); i++ {
		for j := i + 1; j < len(segs); j++ {
			a, b := segs[i], segs[j]
			lo, hi := math.Max(a.low, b.low), math.Min(a.high, b.high)
			if hi <= lo {
				continue
			}
			ma, mb := a.dx/a.dy, b.dx/b.dy
			if math.Abs(ma-mb) < 1e-12 {
				continue
			}
			interceptA := a.x0 - ma*a.y0
			interceptB := b.x0 - mb*b.y0
			at := (interceptB - interceptA) / (ma - mb)
			if at > lo+1e-10 && at < hi-1e-10 {
				events = append(events, at)
			}
		}
	}
	if len(events) > 100000 {
		return fontErr("nonzero event budget")
	}
	sort.Float64s(events)
	unique := events[:0]
	for _, v := range events {
		if len(unique) == 0 || v-unique[len(unique)-1] > 1e-10 {
			unique = append(unique, v)
		}
	}
	crosses := make([]nonzeroCross, 0, len(segs))
	for slab := 0; slab+1 < len(unique); slab++ {
		lo, hi := unique[slab], unique[slab+1]
		if hi-lo < 1e-10 {
			continue
		}
		mid := (lo + hi) / 2
		crosses = crosses[:0]
		for _, s := range segs {
			if mid > s.low && mid < s.high {
				crosses = append(crosses, nonzeroCross{s.x(mid), s.delta})
			}
		}
		sort.Slice(crosses, func(i, j int) bool { return crosses[i].x < crosses[j].x })
		winding := 0
		for idx := 0; idx+1 < len(crosses); idx++ {
			winding += crosses[idx].delta
			if winding == 0 {
				continue
			}
			l, r := math.Max(0, crosses[idx].x), math.Min(float64(w), crosses[idx+1].x)
			if r <= l {
				continue
			}
			a, b := max(0, int(math.Floor(l))), min(w, int(math.Ceil(r)))
			for x := a; x < b; x++ {
				cover := math.Min(r, float64(x+1)) - math.Max(l, float64(x))
				if cover > 0 {
					accumulated[x] += cover * (hi - lo)
				}
			}
		}
	}
	return nil
}

func rasterContoursNonzeroArea(contours GlyphOutline, px, units float64) (*image.Alpha, image.Point, error) {
	if !(px > 0 && px <= 2048 && units > 0) || math.IsNaN(px) || math.IsInf(px, 0) || math.IsNaN(units) || math.IsInf(units, 0) {
		return nil, image.Point{}, fontErr("nonzero raster settings")
	}
	if len(contours) == 0 {
		return image.NewAlpha(image.Rect(0, 0, 1, 1)), image.Point{}, nil
	}
	scale := px / units
	minx, miny, maxx, maxy := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range contours {
		for _, p := range c {
			x, y := p.X*scale, -p.Y*scale
			if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
				return nil, image.Point{}, fontErr("non-finite nonzero outline")
			}
			minx = math.Min(minx, x)
			maxx = math.Max(maxx, x)
			miny = math.Min(miny, y)
			maxy = math.Max(maxy, y)
		}
	}
	if math.IsInf(minx, 1) {
		return image.NewAlpha(image.Rect(0, 0, 1, 1)), image.Point{}, nil
	}
	if maxx-minx > 4090 || maxy-miny > 4090 {
		return nil, image.Point{}, fontErr("nonzero raster size")
	}
	x0, y0 := int(math.Floor(minx))-1, int(math.Floor(miny))-1
	w, h := int(math.Ceil(maxx))+1-x0, int(math.Ceil(maxy))+1-y0
	if w < 1 || h < 1 || w > 4096 || h > 4096 || uint64(w)*uint64(h) > 1<<20 {
		return nil, image.Point{}, fontErr("nonzero raster budget")
	}
	edges, err := flattenOutline(contours, scale, -float64(x0), -float64(y0))
	if err != nil {
		return nil, image.Point{}, err
	}
	if len(edges) > 100000 {
		return nil, image.Point{}, fontErr("nonzero edge budget")
	}
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	row := make([]float64, w)
	for y := 0; y < h; y++ {
		clear(row)
		if err := nonzeroRowCoverage(edges, w, y, row); err != nil {
			return nil, image.Point{}, err
		}
		for x, a := range row {
			a = math.Max(0, math.Min(1, a))
			mask.Pix[y*mask.Stride+x] = uint8(math.Floor(a*255 + .5))
		}
	}
	return mask, image.Pt(x0, y0), nil
}
