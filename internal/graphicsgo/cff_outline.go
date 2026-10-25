package graphicsgo

// CFF1 Type2 CharString interpreter for non-CID and CID-keyed OpenType fonts.
// All coordinates are expressed in font units with Y upward. Unsupported
// operators/features fail closed. This is an exploratory renderer, not an
// accepted Pillow/FreeType replacement.
import (
	"encoding/binary"
	"fmt"
	"math"
)

type cffData struct {
	charstrings, global, local [][]byte
	fdLocals                   [][][]byte
	fdSelect                   []uint16
	privateHints               [][]byte // read-only Private DICT bytes, per CID FD
}

func cffReadIndex(b []byte, at int) ([][]byte, int, error) {
	if at < 0 || at+2 > len(b) {
		return nil, 0, fontErr("CFF INDEX header")
	}
	count := int(u16sf(b, at))
	at += 2
	if count == 0 {
		return nil, at, nil
	}
	if at >= len(b) {
		return nil, 0, fontErr("CFF INDEX offsize")
	}
	os := int(b[at])
	at++
	if os < 1 || os > 4 || count > 65535 || count+1 > (len(b)-at)/os {
		return nil, 0, fontErr("CFF INDEX offsets")
	}
	offsets := make([]uint32, count+1)
	for i := range offsets {
		var n uint32
		for j := 0; j < os; j++ {
			n = n<<8 | uint32(b[at+i*os+j])
		}
		offsets[i] = n
	}
	at += (count + 1) * os
	if offsets[0] != 1 {
		return nil, 0, fontErr("CFF INDEX first offset")
	}
	total := uint64(offsets[count]) - 1
	if total > uint64(len(b)-at) {
		return nil, 0, fontErr("CFF INDEX data length")
	}
	result := make([][]byte, count)
	for i := 0; i < count; i++ {
		if offsets[i] == 0 || offsets[i] > offsets[i+1] {
			return nil, 0, fontErr("CFF INDEX order")
		}
		result[i] = b[at+int(offsets[i]-1) : at+int(offsets[i+1]-1)]
	}
	return result, at + int(total), nil
}
func cffNum(b []byte, pos *int) (float64, error) {
	if *pos >= len(b) {
		return 0, fontErr("CFF number position")
	}
	a := b[*pos]
	*pos++
	switch {
	case a >= 32 && a <= 246:
		return float64(int(a) - 139), nil
	case a >= 247 && a <= 250:
		if *pos >= len(b) {
			return 0, fontErr("CFF number")
		}
		v := (int(a)-247)*256 + int(b[*pos]) + 108
		*pos++
		return float64(v), nil
	case a >= 251 && a <= 254:
		if *pos >= len(b) {
			return 0, fontErr("CFF number")
		}
		v := -(int(a)-251)*256 - int(b[*pos]) - 108
		*pos++
		return float64(v), nil
	case a == 28:
		if *pos+2 > len(b) {
			return 0, fontErr("CFF short int")
		}
		v := int16(binary.BigEndian.Uint16(b[*pos:]))
		*pos += 2
		return float64(v), nil
	case a == 29:
		if *pos+4 > len(b) {
			return 0, fontErr("CFF long int")
		}
		v := int32(binary.BigEndian.Uint32(b[*pos:]))
		*pos += 4
		return float64(v), nil
	case a == 255:
		if *pos+4 > len(b) {
			return 0, fontErr("CFF 16.16")
		}
		v := int32(binary.BigEndian.Uint32(b[*pos:]))
		*pos += 4
		return float64(v) / 65536, nil
	case a == 30: // BCD real DICT value (font matrices etc.); required offsets are integers.
		for n := 0; n < 128; n++ {
			if *pos >= len(b) {
				return 0, fontErr("CFF real")
			}
			v := b[*pos]
			*pos++
			if v&15 == 15 || v>>4 == 15 {
				return 0, nil
			}
		}
		return 0, fontErr("CFF long real")
	}
	return 0, fontErr("CFF unsupported number")
}
func cffDict(b []byte) (map[int][]float64, error) {
	m := map[int][]float64{}
	var args []float64
	for at := 0; at < len(b); {
		a := int(b[at])
		if a <= 21 && a != 28 && a != 29 {
			at++
			if a == 12 {
				if at >= len(b) {
					return nil, fontErr("CFF escaped DICT op")
				}
				a = 1200 + int(b[at])
				at++
			}
			m[a] = append([]float64(nil), args...)
			args = nil
			continue
		}
		v, e := cffNum(b, &at)
		if e != nil {
			return nil, e
		}
		args = append(args, v)
		if len(args) > 96 {
			return nil, fontErr("CFF DICT stack")
		}
	}
	return m, nil
}
func cffInt(v float64) (int, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v < 0 || v > 1<<27 {
		return 0, fontErr("CFF invalid positive integer")
	}
	return int(v), nil
}
func cffIndexAt(b []byte, at int) ([][]byte, error) {
	out, _, err := cffReadIndex(b, at)
	return out, err
}

// cffPrivateSubrs resolves a private DICT and its Subrs INDEX. The Subrs
// offset is relative to the start of the Private DICT, not to the CFF table.
func cffPrivateSubrs(b []byte, dict map[int][]float64) ([][]byte, error) {
	priv := dict[18]
	if len(priv) == 0 {
		return nil, nil
	}
	if len(priv) != 2 {
		return nil, fontErr("CFF Private arguments")
	}
	size, e := cffInt(priv[0])
	if e != nil {
		return nil, e
	}
	start, e := cffInt(priv[1])
	if e != nil {
		return nil, e
	}
	if start > len(b) || size > len(b)-start {
		return nil, fontErr("CFF Private bounds")
	}
	pd, e := cffDict(b[start : start+size])
	if e != nil {
		return nil, e
	}
	s := pd[19]
	if len(s) == 0 {
		return nil, nil
	}
	if len(s) != 1 {
		return nil, fontErr("CFF Subrs arguments")
	}
	relative, e := cffInt(s[0])
	if e != nil {
		return nil, e
	}
	if relative > len(b)-start {
		return nil, fontErr("CFF Subrs range")
	}
	return cffIndexAt(b, start+relative)
}

// cffFDSelect resolves CFF1 CID formats 0 and 3. Ranges must partition
// [0,numGlyphs) exactly; out-of-range or missing FD references fail closed.
func cffFDSelect(b []byte, off, glyphs, fdCount int) ([]uint16, error) {
	if off >= len(b) {
		return nil, fontErr("CFF FDSelect offset")
	}
	selected := make([]uint16, glyphs)
	switch b[off] {
	case 0:
		if glyphs > len(b)-off-1 {
			return nil, fontErr("CFF FDSelect format0 length")
		}
		for i := 0; i < glyphs; i++ {
			fd := int(b[off+1+i])
			if fd >= fdCount {
				return nil, fontErr("CFF FDSelect FD index")
			}
			selected[i] = uint16(fd)
		}
	case 3:
		if len(b)-off < 3 {
			return nil, fontErr("CFF FDSelect format3 header")
		}
		n := int(u16sf(b, off+1))
		if n < 1 || n > glyphs || n > (len(b)-off-5)/3 {
			return nil, fontErr("CFF FDSelect format3 count")
		}
		at := off + 3
		prev := 0
		if int(u16sf(b, at)) != 0 {
			return nil, fontErr("CFF FDSelect first glyph")
		}
		for i := 0; i < n; i++ {
			first := int(u16sf(b, at))
			fd := int(b[at+2])
			at += 3
			if first != prev || fd >= fdCount {
				return nil, fontErr("CFF FDSelect range or FD index")
			}
			end := glyphs
			if i+1 < n {
				end = int(u16sf(b, at))
			} else {
				end = int(u16sf(b, at))
			}
			if end <= first || end > glyphs {
				return nil, fontErr("CFF FDSelect range end")
			}
			for gid := first; gid < end; gid++ {
				selected[gid] = uint16(fd)
			}
			prev = end
		}
		if prev != glyphs {
			return nil, fontErr("CFF FDSelect final sentinel")
		}
	default:
		return nil, fontErr("unsupported CFF1 FDSelect format")
	}
	return selected, nil
}

func (f *SFNT) readCFF() (*cffData, error) {
	if f == nil || f.Outline != CFF1Outline {
		return nil, fontErr("not a CFF1 font")
	}
	b, _ := f.RawTable("CFF ")
	if len(b) < 4 || b[0] != 1 || b[2] < 4 || int(b[2]) > len(b) || b[3] < 1 || b[3] > 4 {
		return nil, fontErr("invalid CFF1 header")
	}
	at := int(b[2])
	_, at, e := cffReadIndex(b, at)
	if e != nil {
		return nil, e
	}
	top, at, e := cffReadIndex(b, at)
	if e != nil {
		return nil, e
	}
	if len(top) != 1 {
		return nil, fontErr("CFF top dict font count")
	}
	_, at, e = cffReadIndex(b, at)
	if e != nil {
		return nil, e
	}
	global, _, e := cffReadIndex(b, at)
	if e != nil {
		return nil, e
	}
	dict, e := cffDict(top[0])
	if e != nil {
		return nil, e
	}
	val := dict[17]
	if len(val) != 1 {
		return nil, fontErr("CFF CharStrings offset")
	}
	off, e := cffInt(val[0])
	if e != nil {
		return nil, e
	}
	chars, e := cffIndexAt(b, off)
	if e != nil {
		return nil, e
	}
	if len(chars) != int(f.NumGlyphs) {
		return nil, fontErr("CFF charstrings/glyph mismatch")
	}
	result := &cffData{charstrings: chars, global: global}
	_, cid := dict[1230] // ROS, not FDArray itself, marks a CID-keyed CFF font
	if !cid {
		private, pe := cffPrivateBytes(b, dict)
		if pe != nil {
			return nil, pe
		}
		result.privateHints = [][]byte{private}
		result.local, e = cffPrivateSubrs(b, dict)
		return result, e
	}
	// CID-keyed fonts have font dictionaries indexed by FDSelect. Each FD
	// owns its own Private DICT and local subroutine INDEX. Global subroutines
	// still use the selected FD's local context when they call callsubr.
	if len(dict[1236]) != 1 || len(dict[1237]) != 1 {
		return nil, fontErr("CFF CID FDArray/FDSelect offsets")
	}
	fdOff, e := cffInt(dict[1236][0])
	if e != nil {
		return nil, e
	}
	selOff, e := cffInt(dict[1237][0])
	if e != nil {
		return nil, e
	}
	fdIndex, e := cffIndexAt(b, fdOff)
	if e != nil {
		return nil, e
	}
	if len(fdIndex) < 1 || len(fdIndex) > 256 {
		return nil, fontErr("CFF FDArray count")
	}
	result.fdLocals = make([][][]byte, len(fdIndex))
	result.privateHints = make([][]byte, len(fdIndex))
	for i, raw := range fdIndex {
		fd, e := cffDict(raw)
		if e != nil {
			return nil, e
		}
		result.privateHints[i], e = cffPrivateBytes(b, fd)
		if e != nil {
			return nil, e
		}
		result.fdLocals[i], e = cffPrivateSubrs(b, fd)
		if e != nil {
			return nil, e
		}
	}
	result.fdSelect, e = cffFDSelect(b, selOff, len(chars), len(fdIndex))
	if e != nil {
		return nil, e
	}
	return result, nil
}
func cffBias(n int) int {
	if n < 1240 {
		return 107
	}
	if n < 33900 {
		return 1131
	}
	return 32768
}

type cffPt struct{ x, y float64 }
type cffBuilder struct {
	paths        GlyphOutline
	current      GlyphContour
	p            cffPt
	stemCount    int
	hintRecorder *cffHintRecorder // nil for the previously accepted unhinted path
	stack        []float64
	commandCount int
}

func (c *cffBuilder) begin(dx, dy float64) {
	if len(c.current) > 0 {
		c.finish()
	}
	c.p.x += dx
	c.p.y += dy
	c.current = GlyphContour{{X: c.p.x, Y: c.p.y, On: true}}
	if c.hintRecorder != nil {
		c.hintRecorder.sourceEvent("move", len(c.paths), []cffPt{c.p}, -1, -1)
	}
}
func (c *cffBuilder) line(dx, dy float64) {
	start := c.p
	first := len(c.current) - 1
	c.p.x += dx
	c.p.y += dy
	c.current = append(c.current, GlyphPoint{X: c.p.x, Y: c.p.y, On: true})
	if c.hintRecorder != nil && first >= 0 {
		c.hintRecorder.sourceEvent("line", len(c.paths), []cffPt{start, c.p}, len(c.hintRecorder.geometry), -1)
		c.hintRecorder.geometryEvent(len(c.paths), first, len(c.current)-1, "line", false)
	}
}
func (c *cffBuilder) curve(dxa, dya, dxb, dyb, dxc, dyc float64) {
	first := len(c.current) - 1
	a := c.p
	u := cffPt{a.x + dxa, a.y + dya}
	v := cffPt{u.x + dxb, u.y + dyb}
	end := cffPt{v.x + dxc, v.y + dyc}
	// Flatten cubic Bezier deterministically to on-curve points in native units.
	// 16 segments is a bounded initial implementation; real-font pixel parity is
	// not asserted by this approximation.
	for i := 1; i <= 16; i++ {
		t := float64(i) / 16
		s := 1 - t
		x := s*s*s*a.x + 3*s*s*t*u.x + 3*s*t*t*v.x + t*t*t*end.x
		y := s*s*s*a.y + 3*s*s*t*u.y + 3*s*t*t*v.y + t*t*t*end.y
		c.current = append(c.current, GlyphPoint{x, y, true})
	}
	c.p = end
	if c.hintRecorder != nil && first >= 0 {
		c.hintRecorder.sourceEvent("curve", len(c.paths), []cffPt{a, u, v, end}, len(c.hintRecorder.geometry), -1)
		c.hintRecorder.geometryEvent(len(c.paths), first, len(c.current)-1, "curve", false)
	}
}
func (c *cffBuilder) finish() {
	if len(c.current) > 0 {
		if c.hintRecorder != nil {
			last := len(c.current) - 1
			firstPoint := c.current[0]
			lastPoint := c.current[last]
			start, end := cffPt{lastPoint.X, lastPoint.Y}, cffPt{firstPoint.X, firstPoint.Y}
			geometryIndex := -1
			if len(c.current) > 1 && start != end {
				geometryIndex = len(c.hintRecorder.geometry)
			}
			c.hintRecorder.sourceEvent("close", len(c.paths), []cffPt{start, end}, geometryIndex, -1)
			if geometryIndex >= 0 {
				c.hintRecorder.geometryEvent(len(c.paths), last, 0, "close", true)
			}
		}
		c.paths = append(c.paths, c.current)
		c.current = nil
	}
}
func (c *cffBuilder) popStem() error {
	if len(c.stack)%2 == 1 {
		c.stack = c.stack[1:]
	} // optional width
	c.stemCount += len(c.stack) / 2
	c.stack = nil
	if c.stemCount > 4096 {
		return fontErr("CFF hint count")
	}
	return nil
}
func (c *cffBuilder) execute(code []byte, refs *cffData, depth int) error {
	if depth > 12 {
		return fontErr("CFF subroutine depth")
	}
	for pos := 0; pos < len(code); {
		c.commandCount++
		if c.commandCount > 30000 {
			return fontErr("CFF instruction limit")
		}
		op := code[pos]
		if op == 28 || op == 255 || op >= 32 {
			v, e := cffNum(code, &pos)
			if e != nil {
				return e
			}
			c.stack = append(c.stack, v)
			if len(c.stack) > 96 {
				return fontErr("CFF stack")
			}
			continue
		}
		pos++
		s := c.stack
		switch op {
		case 1, 3, 18, 23:
			if c.hintRecorder != nil {
				if err := c.hintRecorder.stems(op, c.stack); err != nil {
					return err
				}
			}
			if err := c.popStem(); err != nil {
				return err
			}
		case 19, 20:
			if len(s) > 0 {
				if c.hintRecorder != nil {
					if err := c.hintRecorder.stems(3, c.stack); err != nil {
						return err
					}
				}
				if e := c.popStem(); e != nil {
					return e
				}
			}
			skip := (c.stemCount + 7) / 8
			if skip > len(code)-pos {
				return fontErr("CFF hintmask")
			}
			if c.hintRecorder != nil {
				if err := c.hintRecorder.mask(op, code[pos:pos+skip]); err != nil {
					return err
				}
				kind := "hintmask"
				if op == 20 {
					kind = "cntrmask"
				}
				c.hintRecorder.sourceEvent(kind, len(c.paths), nil, -1, len(c.hintRecorder.masks)-1)
			}
			pos += skip
		case 4:
			if len(s) == 2 {
				s = s[1:]
			} // optional width
			if len(s) != 1 {
				return fontErr("CFF vmoveto")
			}
			c.begin(0, s[0])
			c.stack = nil
		case 21:
			if len(s) == 3 {
				s = s[1:]
			}
			if len(s) != 2 {
				return fontErr("CFF rmoveto")
			}
			c.begin(s[0], s[1])
			c.stack = nil
		case 22:
			if len(s) == 2 {
				s = s[1:]
			}
			if len(s) != 1 {
				return fontErr("CFF hmoveto")
			}
			c.begin(s[0], 0)
			c.stack = nil
		case 5:
			if len(s) < 2 || len(s)%2 != 0 {
				return fontErr("CFF rlineto")
			}
			for i := 0; i < len(s); i += 2 {
				c.line(s[i], s[i+1])
			}
			c.stack = nil
		case 6, 7:
			if len(s) < 1 {
				return fontErr("CFF alternating line")
			}
			horizontal := op == 6
			for _, v := range s {
				if horizontal {
					c.line(v, 0)
				} else {
					c.line(0, v)
				}
				horizontal = !horizontal
			}
			c.stack = nil
		case 8:
			if len(s) < 6 || len(s)%6 != 0 {
				return fontErr("CFF rrcurveto")
			}
			for i := 0; i < len(s); i += 6 {
				c.curve(s[i], s[i+1], s[i+2], s[i+3], s[i+4], s[i+5])
			}
			c.stack = nil
		case 24:
			if len(s) < 8 || (len(s)-2)%6 != 0 {
				return fontErr("CFF rcurveline")
			}
			i := 0
			for i < len(s)-2 {
				c.curve(s[i], s[i+1], s[i+2], s[i+3], s[i+4], s[i+5])
				i += 6
			}
			c.line(s[i], s[i+1])
			c.stack = nil
		case 25:
			if len(s) < 8 || (len(s)-6)%2 != 0 {
				return fontErr("CFF rlinecurve")
			}
			i := 0
			for i+2 <= len(s)-6 {
				c.line(s[i], s[i+1])
				i += 2
			}
			c.curve(s[i], s[i+1], s[i+2], s[i+3], s[i+4], s[i+5])
			c.stack = nil
		case 26, 27:
			if len(s) < 4 {
				return fontErr("CFF vv/hh curve")
			}
			i := 0
			first := 0.0
			if len(s)%4 == 1 {
				first = s[0]
				i++
			}
			for i+4 <= len(s) {
				if op == 26 {
					c.curve(first, s[i], s[i+1], s[i+2], 0, s[i+3])
				} else {
					c.curve(s[i], first, s[i+1], s[i+2], s[i+3], 0)
				}
				first = 0
				i += 4
			}
			if i != len(s) {
				return fontErr("CFF vv/hh curve count")
			}
			c.stack = nil
		case 30, 31:
			if len(s) < 4 {
				return fontErr("CFF hv/vh curve")
			}
			i := 0
			horizontal := op == 31
			for i+4 <= len(s) {
				a, b, cc, d := s[i], s[i+1], s[i+2], s[i+3]
				i += 4
				last := 0.0
				if i == len(s)-1 {
					last = s[i]
					i++
				}
				if horizontal {
					c.curve(a, 0, b, cc, last, d)
				} else {
					c.curve(0, a, b, cc, d, last)
				}
				horizontal = !horizontal
			}
			if i != len(s) {
				return fontErr("CFF hv/vh count")
			}
			c.stack = nil
		case 10, 29:
			if len(s) < 1 {
				return fontErr("CFF callsubr")
			}
			index := int(s[len(s)-1])
			c.stack = c.stack[:len(s)-1]
			list := refs.local
			if op == 29 {
				list = refs.global
			}
			index += cffBias(len(list))
			if index < 0 || index >= len(list) {
				return fontErr("CFF subroutine index")
			}
			if e := c.execute(list[index], refs, depth+1); e != nil {
				return e
			}
		case 11:
			if depth == 0 {
				return fontErr("CFF return at top level")
			}
			return nil
		case 14:
			c.finish()
			c.stack = nil
			return nil
		case 12:
			if pos >= len(code) {
				return fontErr("CFF escape")
			}
			escaped := code[pos]
			pos++
			switch escaped {
			case 34:
				if len(s) != 7 {
					return fontErr("CFF hflex")
				}
				c.curve(s[0], 0, s[1], s[2], s[3], 0)
				c.curve(s[4], 0, s[5], -s[2], s[6], 0)
			case 35:
				if len(s) != 13 {
					return fontErr("CFF flex")
				}
				c.curve(s[0], s[1], s[2], s[3], s[4], s[5])
				c.curve(s[6], s[7], s[8], s[9], s[10], s[11])
			case 36:
				if len(s) != 9 {
					return fontErr("CFF hflex1")
				}
				c.curve(s[0], s[1], s[2], s[3], s[4], 0)
				c.curve(s[5], 0, s[6], s[7], s[8], -(s[1] + s[3] + s[7]))
			case 37:
				if len(s) != 11 {
					return fontErr("CFF flex1")
				}
				xsum, ysum := 0.0, 0.0
				for i := 0; i < 10; i += 2 {
					xsum += s[i]
					ysum += s[i+1]
				}
				dx, dy := s[10], -ysum
				if math.Abs(xsum) <= math.Abs(ysum) {
					dx = -xsum
					dy = s[10]
				}
				c.curve(s[0], s[1], s[2], s[3], s[4], s[5])
				c.curve(s[6], s[7], s[8], s[9], dx, dy)
			default:
				return fontErr(fmt.Sprintf("unsupported CFF escaped operator %d", escaped))
			}
			c.stack = nil
		default:
			return fontErr(fmt.Sprintf("unsupported CFF operator %d", op))
		}
		if math.Abs(c.p.x) > 1e6 || math.Abs(c.p.y) > 1e6 {
			return fontErr("CFF coordinate overflow")
		}
	}
	return nil
}

// GlyphOutlineCFF1 interprets a CFF1 Type2 charstring into bounded contours.
// CFF2 and unsupported exotic Type2 operators are rejected.
func (f *SFNT) GlyphOutlineCFF1(gid uint16) (GlyphOutline, error) {
	if f == nil || gid >= f.NumGlyphs {
		return nil, fontErr("CFF glyph index")
	}
	refs, e := f.readCFF()
	if e != nil {
		return nil, e
	}
	if len(refs.fdSelect) != 0 {
		refs.local = refs.fdLocals[refs.fdSelect[gid]]
	}
	c := &cffBuilder{}
	if e = c.execute(refs.charstrings[gid], refs, 0); e != nil {
		return nil, e
	}
	c.finish()
	return c.paths, nil
}
