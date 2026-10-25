package graphicsgo

import (
	"image"
	"math"
)

// RasterGlyphArea is a research-only continuous polygon-coverage rasterizer.
// It is deliberately NOT the production font backend and is not asserted to
// reproduce FreeType hinting or its rasterization algorithm.
func (f *SFNT) RasterGlyphArea(gid uint16, px float64) (*image.Alpha, image.Point, error) {
	if f == nil {
		return nil, image.Point{}, fontErr("nil font")
	}
	var contours GlyphOutline
	var err error
	switch f.Outline {
	case TrueTypeOutline:
		contours, err = f.GlyphOutlineTTF(gid)
	case CFF1Outline:
		contours, err = f.GlyphOutlineCFF1(gid)
	default:
		return nil, image.Point{}, fontErr("unsupported outline")
	}
	if err != nil {
		return nil, image.Point{}, err
	}
	return rasterContoursArea(contours, px, float64(f.UnitsPerEm))
}

func signedPolygonArea(p []edgePoint) float64 {
	if len(p) < 3 {
		return 0
	}
	sum := 0.0
	for i := range p {
		j := (i + 1) % len(p)
		sum += p[i].x*p[j].y - p[j].x*p[i].y
	}
	return sum * .5
}

// clipCoveragePolygon clips a polygon against a pixel's four closed halfplanes.
// Its signed area preserves the outline's winding sign, so counter-wound holes
// subtract coverage. No font input bytes or pixel fixtures are embedded.
func clipCoveragePolygon(poly []edgePoint, left, top float64) float64 {
	const eps = 1e-12
	right, bottom := left+1, top+1
	type boundary struct {
		axis    int
		value   float64
		greater bool
	}
	bounds := [4]boundary{{0, left, true}, {0, right, false}, {1, top, true}, {1, bottom, false}}
	working := poly
	for _, b := range bounds {
		if len(working) < 3 {
			return 0
		}
		next := make([]edgePoint, 0, len(working)+4)
		axisVal := func(p edgePoint) float64 {
			if b.axis == 0 {
				return p.x
			}
			return p.y
		}
		inside := func(p edgePoint) bool {
			v := axisVal(p)
			if b.greater {
				return v >= b.value-eps
			}
			return v <= b.value+eps
		}
		intersection := func(a, c edgePoint) edgePoint {
			av, bv := axisVal(a), axisVal(c)
			if bv == av {
				return a
			}
			t := (b.value - av) / (bv - av)
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
			return edgePoint{a.x + t*(c.x-a.x), a.y + t*(c.y-a.y)}
		}
		prev := working[len(working)-1]
		prevInside := inside(prev)
		for _, curr := range working {
			currInside := inside(curr)
			if currInside != prevInside {
				next = append(next, intersection(prev, curr))
			}
			if currInside {
				next = append(next, curr)
			}
			prev, prevInside = curr, currInside
		}
		working = next
	}
	return signedPolygonArea(working)
}

func rasterContoursArea(contours GlyphOutline, px, units float64) (*image.Alpha, image.Point, error) {
	if px <= 0 || px > 2048 || units <= 0 || math.IsNaN(px) || math.IsInf(px, 0) {
		return nil, image.Point{}, fontErr("raster settings")
	}
	if len(contours) == 0 {
		return image.NewAlpha(image.Rect(0, 0, 1, 1)), image.Point{}, nil
	}
	scale := px / units
	minx, miny, maxx, maxy := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range contours {
		for _, p := range c {
			if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
				return nil, image.Point{}, fontErr("outline not finite")
			}
			minx = math.Min(minx, p.X*scale)
			maxx = math.Max(maxx, p.X*scale)
			miny = math.Min(miny, -p.Y*scale)
			maxy = math.Max(maxy, -p.Y*scale)
		}
	}
	if math.IsInf(minx, 1) || math.IsInf(miny, 1) {
		return image.NewAlpha(image.Rect(0, 0, 1, 1)), image.Point{}, nil
	}
	x0, y0 := int(math.Floor(minx))-1, int(math.Floor(miny))-1
	x1, y1 := int(math.Ceil(maxx))+1, int(math.Ceil(maxy))+1
	w, h := x1-x0, y1-y0
	if w < 1 || h < 1 || w > 4096 || h > 4096 || uint64(w)*uint64(h) > 1<<20 {
		return nil, image.Point{}, fontErr("raster budget")
	}
	edges, err := flattenOutline(contours, scale, -float64(x0), -float64(y0))
	if err != nil {
		return nil, image.Point{}, err
	}
	if len(edges) > 100000 {
		return nil, image.Point{}, fontErr("edge budget")
	}
	var groups [][]edgePoint
	var curr []edgePoint
	for _, e := range edges {
		if len(curr) == 0 {
			curr = []edgePoint{e.a}
		} else if curr[len(curr)-1] != e.a {
			return nil, image.Point{}, fontErr("noncontiguous contour")
		}
		curr = append(curr, e.b)
		if e.b == curr[0] {
			if len(curr) >= 4 {
				groups = append(groups, curr[:len(curr)-1])
			}
			curr = nil
		}
	}
	if len(curr) > 0 {
		return nil, image.Point{}, fontErr("unclosed contour")
	}
	// Choose winding of the largest area as the exterior winding sign.
	// This exploratory method is not a substitute for a full nonzero rasterizer
	// when the outline contains overlapping same-winding contours.
	dominant := 0.0
	for _, g := range groups {
		a := signedPolygonArea(g)
		if math.Abs(a) > math.Abs(dominant) {
			dominant = a
		}
	}
	if dominant == 0 {
		return image.NewAlpha(image.Rect(0, 0, w, h)), image.Pt(x0, y0), nil
	}
	sign := math.Copysign(1, dominant)
	cover := make([]float64, w*h)
	for _, g := range groups {
		if len(g) < 3 {
			continue
		}
		gminx, gminy, gmaxx, gmaxy := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, p := range g {
			gminx = math.Min(gminx, p.x)
			gminy = math.Min(gminy, p.y)
			gmaxx = math.Max(gmaxx, p.x)
			gmaxy = math.Max(gmaxy, p.y)
		}
		ix0, iy0 := max(0, int(math.Floor(gminx))), max(0, int(math.Floor(gminy)))
		ix1, iy1 := min(w, int(math.Ceil(gmaxx))), min(h, int(math.Ceil(gmaxy)))
		for y := iy0; y < iy1; y++ {
			for x := ix0; x < ix1; x++ {
				cover[y*w+x] += sign * clipCoveragePolygon(g, float64(x), float64(y))
			}
		}
	}
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	for i, v := range cover {
		if v < 0 {
			v = 0
		}
		if v > 1 {
			v = 1
		}
		mask.Pix[i] = uint8(math.Floor(v*255 + 0.5))
	}
	return mask, image.Pt(x0, y0), nil
}
