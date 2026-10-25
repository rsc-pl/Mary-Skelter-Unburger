package graphicsgo

import (
	"image"
	"math"
	"sort"
)

// GlyphPoint is expressed in unscaled font units, Y increasing upwards.
type GlyphPoint struct {
	X, Y float64
	On   bool
}
type GlyphContour []GlyphPoint
type GlyphOutline []GlyphContour

func (f *SFNT) glyphLocation(gid uint16) (int, int, error) {
	if f == nil || f.Outline != TrueTypeOutline || gid >= f.NumGlyphs {
		return 0, 0, fontErr("not a TrueType outline")
	}
	loca, _ := f.RawTable("loca")
	glyf, _ := f.RawTable("glyf")
	at := int(gid)
	var a, b uint32
	if f.indexToLocFormat == 0 {
		a = uint32(u16sf(loca, 2*at)) * 2
		b = uint32(u16sf(loca, 2*at+2)) * 2
	} else {
		a = u32sf(loca, 4*at)
		b = u32sf(loca, 4*at+4)
	}
	if b < a || b > uint32(len(glyf)) {
		return 0, 0, fontErr("invalid loca offsets")
	}
	return int(a), int(b), nil
}

// GlyphOutlineTTF parses simple and XY-offset composite glyf outlines. It
// rejects point-matched components, unsupported transforms and deep recursion
// rather than inventing a visually incorrect font. CFF1 remains a separate gate.
func (f *SFNT) GlyphOutlineTTF(gid uint16) (GlyphOutline, error) { return f.glyphOutline(gid, 0) }
func (f *SFNT) glyphOutline(gid uint16, depth int) (GlyphOutline, error) {
	if depth > 8 {
		return nil, fontErr("compound glyph recursion")
	}
	a, b, e := f.glyphLocation(gid)
	if e != nil {
		return nil, e
	}
	if a == b {
		return GlyphOutline{}, nil
	}
	glyf, _ := f.RawTable("glyf")
	t := glyf[a:b]
	if len(t) < 10 {
		return nil, fontErr("short glyf header")
	}
	count := int(i16sf(t, 0))
	if count >= 0 {
		if count > 2048 || 10+count*2+2 > len(t) {
			return nil, fontErr("contour count")
		}
		ends := make([]int, count)
		prev := -1
		for i := 0; i < count; i++ {
			v := int(u16sf(t, 10+i*2))
			if v <= prev || v > 65535 {
				return nil, fontErr("contour endpoint")
			}
			ends[i] = v
			prev = v
		}
		pos := 10 + count*2
		ins := int(u16sf(t, pos))
		pos += 2
		if ins > len(t)-pos {
			return nil, fontErr("instructions")
		}
		pos += ins
		if count == 0 {
			return GlyphOutline{}, nil
		}
		n := ends[count-1] + 1
		if n > 65535 {
			return nil, fontErr("point count")
		}
		flags := make([]byte, n)
		for i := 0; i < n; {
			if pos >= len(t) {
				return nil, fontErr("truncated point flags")
			}
			v := t[pos]
			pos++
			run := 1
			if v&8 != 0 {
				if pos >= len(t) {
					return nil, fontErr("truncated flag repeat")
				}
				run += int(t[pos])
				pos++
			}
			if run > n-i {
				return nil, fontErr("flag repeat overrun")
			}
			for j := 0; j < run; j++ {
				flags[i+j] = v
			}
			i += run
		}
		xs, ys := make([]float64, n), make([]float64, n)
		x, y := int32(0), int32(0)
		for i, flag := range flags {
			var d int32
			if flag&2 != 0 {
				if pos >= len(t) {
					return nil, fontErr("short X delta")
				}
				d = int32(t[pos])
				pos++
				if flag&16 == 0 {
					d = -d
				}
			} else if flag&16 == 0 {
				if pos+2 > len(t) {
					return nil, fontErr("short X long")
				}
				d = int32(i16sf(t, pos))
				pos += 2
			}
			x += d
			xs[i] = float64(x)
		}
		for i, flag := range flags {
			var d int32
			if flag&4 != 0 {
				if pos >= len(t) {
					return nil, fontErr("short Y delta")
				}
				d = int32(t[pos])
				pos++
				if flag&32 == 0 {
					d = -d
				}
			} else if flag&32 == 0 {
				if pos+2 > len(t) {
					return nil, fontErr("short Y long")
				}
				d = int32(i16sf(t, pos))
				pos += 2
			}
			y += d
			ys[i] = float64(y)
		}
		out := make(GlyphOutline, count)
		start := 0
		for i, end := range ends {
			out[i] = make(GlyphContour, end-start+1)
			for j := start; j <= end; j++ {
				out[i][j-start] = GlyphPoint{xs[j], ys[j], flags[j]&1 != 0}
			}
			start = end + 1
		}
		return out, nil
	}
	if count != -1 {
		return nil, fontErr("unknown negative glyf contour count")
	}
	// Composite glyf format, OpenType glyf spec flags.
	const (
		argWords = 1
		argsXY   = 2
		scale    = 8
		more     = 32
		xyScale  = 64
		matrix   = 128
		instr    = 256
	)
	pos := 10
	var result GlyphOutline
	for parts := 0; ; parts++ {
		if parts > 128 || pos+4 > len(t) {
			return nil, fontErr("compound components")
		}
		flags := int(u16sf(t, pos))
		child := u16sf(t, pos+2)
		pos += 4
		var dx, dy float64
		if flags&argsXY == 0 {
			return nil, fontErr("unsupported compound point-matched components")
		}
		if flags&argWords != 0 {
			if pos+4 > len(t) {
				return nil, fontErr("short component offsets")
			}
			dx = float64(i16sf(t, pos))
			dy = float64(i16sf(t, pos+2))
			pos += 4
		} else {
			if pos+2 > len(t) {
				return nil, fontErr("short component offsets")
			}
			dx = float64(int8(t[pos]))
			dy = float64(int8(t[pos+1]))
			pos += 2
		}
		xx, yy, xy, yx := 1.0, 1.0, 0.0, 0.0
		f214 := func(at int) float64 { return float64(i16sf(t, at)) / 16384 }
		switch {
		case flags&scale != 0:
			if pos+2 > len(t) {
				return nil, fontErr("scale")
			}
			xx = f214(pos)
			yy = xx
			pos += 2
		case flags&xyScale != 0:
			if pos+4 > len(t) {
				return nil, fontErr("XY scale")
			}
			xx = f214(pos)
			yy = f214(pos + 2)
			pos += 4
		case flags&matrix != 0:
			if pos+8 > len(t) {
				return nil, fontErr("2x2 transform")
			}
			xx = f214(pos)
			xy = f214(pos + 2)
			yx = f214(pos + 4)
			yy = f214(pos + 6)
			pos += 8
		}
		if math.Abs(xx)+math.Abs(xy)+math.Abs(yx)+math.Abs(yy) > 32 {
			return nil, fontErr("compound transform range")
		}
		if child == gid {
			return nil, fontErr("compound self-cycle")
		}
		contours, e := f.glyphOutline(child, depth+1)
		if e != nil {
			return nil, e
		}
		for _, c := range contours {
			trans := make(GlyphContour, len(c))
			for i, p := range c {
				trans[i] = GlyphPoint{X: xx*p.X + xy*p.Y + dx, Y: yx*p.X + yy*p.Y + dy, On: p.On}
			}
			result = append(result, trans)
		}
		if flags&more == 0 {
			if flags&instr != 0 {
				if pos+2 > len(t) {
					return nil, fontErr("composite instructions")
				}
				ins := int(u16sf(t, pos))
				pos += 2
				if ins > len(t)-pos {
					return nil, fontErr("short composite instructions")
				}
			}
			break
		}
	}
	return result, nil
}

type edgePoint struct{ x, y float64 }
type fillEdge struct{ a, b edgePoint }

func flattenOutline(contours GlyphOutline, scale float64, originX, originY float64) ([]fillEdge, error) {
	edges := make([]fillEdge, 0, 128)
	point := func(p GlyphPoint) edgePoint { return edgePoint{p.X*scale + originX, originY - p.Y*scale} }
	line := func(a, b edgePoint) {
		if a != b {
			edges = append(edges, fillEdge{a, b})
		}
	}
	curve := func(a, c, b edgePoint) {
		distance := math.Hypot(a.x-2*c.x+b.x, a.y-2*c.y+b.y)
		parts := int(math.Ceil(math.Sqrt(distance) * 4))
		if parts < 4 {
			parts = 4
		}
		if parts > 128 {
			parts = 128
		}
		last := a
		for i := 1; i <= parts; i++ {
			t := float64(i) / float64(parts)
			m := 1 - t
			p := edgePoint{m*m*a.x + 2*m*t*c.x + t*t*b.x, m*m*a.y + 2*m*t*c.y + t*t*b.y}
			line(last, p)
			last = p
		}
	}
	for _, contour := range contours {
		n := len(contour)
		if n == 0 {
			continue
		}
		if n > 65535 {
			return nil, fontErr("large contour")
		}
		// A TrueType contour can begin with an off-curve control point.
		var first edgePoint
		idx := 0
		if contour[0].On {
			first = point(contour[0])
			idx = 1
		} else if contour[n-1].On {
			first = point(contour[n-1])
		} else {
			a, b := point(contour[n-1]), point(contour[0])
			first = edgePoint{(a.x + b.x) / 2, (a.y + b.y) / 2}
		}
		current := first
		var control edgePoint
		pending := false
		for j := 0; j < n; j++ {
			p := point(contour[(idx+j)%n])
			isOn := contour[(idx+j)%n].On
			if isOn {
				if pending {
					curve(current, control, p)
					pending = false
				} else {
					line(current, p)
				}
				current = p
			} else if pending {
				mid := edgePoint{(control.x + p.x) / 2, (control.y + p.y) / 2}
				curve(current, control, mid)
				current = mid
				control = p
			} else {
				control = p
				pending = true
			}
		}
		if pending {
			curve(current, control, first)
		} else {
			line(current, first)
		}
	}
	return edges, nil
}
func windingAt(edges []fillEdge, x, y float64) bool {
	w := 0
	for _, e := range edges {
		a, b := e.a, e.b
		if a.y <= y && y < b.y {
			if (b.x-a.x)*(y-a.y)-(x-a.x)*(b.y-a.y) > 0 {
				w++
			}
		} else if b.y <= y && y < a.y {
			if (b.x-a.x)*(y-a.y)-(x-a.x)*(b.y-a.y) < 0 {
				w--
			}
		}
	}
	return w != 0
}

// RasterGlyphTTF rasterizes TrueType-glyf outlines without external tools.
func (f *SFNT) RasterGlyphTTF(gid uint16, px float64, samples int) (*image.Alpha, image.Point, error) {
	contours, err := f.GlyphOutlineTTF(gid)
	if err != nil {
		return nil, image.Point{}, err
	}
	return rasterContours(contours, px, float64(f.UnitsPerEm), samples)
}

// RasterGlyphCFF1 rasterizes Type2 CFF1 outlines. It rejects unsupported
// CID/charstrings; full FreeType/Pillow coverage parity remains OPEN.
func (f *SFNT) RasterGlyphCFF1(gid uint16, px float64, samples int) (*image.Alpha, image.Point, error) {
	contours, err := f.GlyphOutlineCFF1(gid)
	if err != nil {
		return nil, image.Point{}, err
	}
	return rasterContours(contours, px, float64(f.UnitsPerEm), samples)
}

// RasterGlyph dispatches by parsed outline format; it is a research-only,
// unhinted 4x-style antialiaser, not yet wired to production.
func (f *SFNT) RasterGlyph(gid uint16, px float64, samples int) (*image.Alpha, image.Point, error) {
	if f == nil {
		return nil, image.Point{}, fontErr("nil font")
	}
	if f.Outline == TrueTypeOutline {
		return f.RasterGlyphTTF(gid, px, samples)
	}
	if f.Outline == CFF1Outline {
		return f.RasterGlyphCFF1(gid, px, samples)
	}
	return nil, image.Point{}, fontErr("unknown outline format")
}
func rasterContours(contours GlyphOutline, px, units float64, samples int) (*image.Alpha, image.Point, error) {
	if px <= 0 || px > 2048 || units <= 0 || samples != 2 && samples != 4 && samples != 8 {
		return nil, image.Point{}, fontErr("raster settings")
	}
	if len(contours) == 0 {
		return image.NewAlpha(image.Rect(0, 0, 1, 1)), image.Point{}, nil
	}
	scale := px / units
	minx, miny, maxx, maxy := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range contours {
		for _, p := range c {
			minx = math.Min(minx, p.X*scale)
			maxx = math.Max(maxx, p.X*scale)
			miny = math.Min(miny, -p.Y*scale)
			maxy = math.Max(maxy, -p.Y*scale)
		}
	}
	x0, y0 := int(math.Floor(minx))-1, int(math.Floor(miny))-1
	x1, y1 := int(math.Ceil(maxx))+1, int(math.Ceil(maxy))+1
	if x1-x0 < 1 || y1-y0 < 1 || x1-x0 > 4096 || y1-y0 > 4096 {
		return nil, image.Point{}, fontErr("raster bounds")
	}
	edges, e := flattenOutline(contours, scale, -float64(x0), -float64(y0))
	if e != nil {
		return nil, image.Point{}, e
	}
	if uint64(x1-x0)*uint64(y1-y0) > 16<<20 || uint64(x1-x0)*uint64(y1-y0)*uint64(samples*samples) > 64<<20 {
		return nil, image.Point{}, fontErr("raster pixel budget")
	}
	if len(edges) > 100000 {
		return nil, image.Point{}, fontErr("outline edge budget")
	}
	mask := image.NewAlpha(image.Rect(0, 0, x1-x0, y1-y0))
	// Scan-line nonzero fill. Unlike testing every edge for each individual
	// subpixel, intersections are computed once per supersampled scanline.
	// This keeps 4x rasterization practical for full high-res text masks.
	type crossing struct {
		x     float64
		delta int
	}
	w, h := mask.Rect.Dx(), mask.Rect.Dy()
	hits := make([]uint16, w*h)
	for sy := 0; sy < h*samples; sy++ {
		y := (float64(sy) + 0.5) / float64(samples)
		intersections := make([]crossing, 0, len(edges))
		for _, e := range edges {
			a, b := e.a, e.b
			if a.y <= y && y < b.y {
				intersections = append(intersections, crossing{a.x + (y-a.y)*(b.x-a.x)/(b.y-a.y), 1})
			} else if b.y <= y && y < a.y {
				intersections = append(intersections, crossing{a.x + (y-a.y)*(b.x-a.x)/(b.y-a.y), -1})
			}
		}
		sort.Slice(intersections, func(i, j int) bool { return intersections[i].x < intersections[j].x })
		winding, cursor := 0, 0
		for sx := 0; sx < w*samples; sx++ {
			x := (float64(sx) + 0.5) / float64(samples)
			for cursor < len(intersections) && intersections[cursor].x <= x {
				winding += intersections[cursor].delta
				cursor++
			}
			if winding != 0 {
				hits[(sy/samples)*w+sx/samples]++
			}
		}
	}
	q := samples * samples
	for i, n := range hits {
		mask.Pix[i] = uint8((int(n)*255 + q/2) / q)
	}
	return mask, image.Pt(x0, y0), nil
}
