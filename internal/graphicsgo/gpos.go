package graphicsgo

// Bounded OpenType GPOS 'kern' PairPos parser for unshaped Latin runs.
// This implements only the default language system's horizontal pair positioning
// (PairPos format 1/2, direct type 2 and extension type 9). Ligatures, mark
// positioning, script shaping and device/variation adjustments remain unsupported.
// The function fails closed when an active kern lookup is unsupported.
import (
	"fmt"
	"math"
)

type PairAdjustment struct {
	FirstXPlacement, FirstYPlacement, FirstXAdvance, FirstYAdvance     int
	SecondXPlacement, SecondYPlacement, SecondXAdvance, SecondYAdvance int
}

type gposPair struct {
	format         int
	covered        map[uint16]int
	sets           []map[uint16]PairAdjustment
	class1, class2 map[uint16]uint16
	matrix         [][]PairAdjustment
}

func gposWindow(b []byte, base, off, need int) ([]byte, error) {
	if base < 0 || off < 0 || need < 0 || base > len(b) || off > len(b)-base || need > len(b)-base-off {
		return nil, fontErr("GPOS offset out of range")
	}
	return b[base+off:], nil
}
func gposU16(b []byte, off int) (uint16, error) {
	if off < 0 || off+2 > len(b) {
		return 0, fontErr("GPOS uint16 range")
	}
	return u16sf(b, off), nil
}
func gposU32(b []byte, off int) (uint32, error) {
	if off < 0 || off+4 > len(b) {
		return 0, fontErr("GPOS uint32 range")
	}
	return u32sf(b, off), nil
}
func gposAt(b []byte, off int, need int) ([]byte, error) { return gposWindow(b, 0, off, need) }
func parseCoverage(b []byte, at int) (map[uint16]int, error) {
	x, e := gposAt(b, at, 4)
	if e != nil {
		return nil, e
	}
	n := int(u16sf(x, 2))
	if n > 65535 {
		return nil, fontErr("coverage count")
	}
	out := make(map[uint16]int)
	switch u16sf(x, 0) {
	case 1:
		if n > (len(x)-4)/2 {
			return nil, fontErr("coverage format1 count")
		}
		var last uint16
		for i := 0; i < n; i++ {
			id := u16sf(x, 4+2*i)
			if i > 0 && id <= last {
				return nil, fontErr("coverage sorting")
			}
			last = id
			out[id] = i
		}
	case 2:
		if n > (len(x)-4)/6 {
			return nil, fontErr("coverage format2 count")
		}
		prev := -1
		next := 0
		for i := 0; i < n; i++ {
			at := 4 + 6*i
			s, end, first := int(u16sf(x, at)), int(u16sf(x, at+2)), int(u16sf(x, at+4))
			if s <= prev || s > end || first != next {
				return nil, fontErr("coverage ranges")
			}
			for id := s; id <= end; id++ {
				out[uint16(id)] = next
				next++
			}
			prev = end
		}
	default:
		return nil, fontErr("unsupported coverage format")
	}
	return out, nil
}
func parseClassDef(b []byte, at int) (map[uint16]uint16, error) {
	x, e := gposAt(b, at, 4)
	if e != nil {
		return nil, e
	}
	out := make(map[uint16]uint16)
	switch u16sf(x, 0) {
	case 1:
		start, n := int(u16sf(x, 2)), 0
		n16, e := gposU16(x, 4)
		if e != nil {
			return nil, e
		}
		n = int(n16)
		if n > (len(x)-6)/2 || start+n > 65536 {
			return nil, fontErr("classdef format1 count")
		}
		for i := 0; i < n; i++ {
			out[uint16(start+i)] = u16sf(x, 6+2*i)
		}
	case 2:
		n := int(u16sf(x, 2))
		if n > (len(x)-4)/6 {
			return nil, fontErr("classdef format2 count")
		}
		prev := -1
		for i := 0; i < n; i++ {
			at := 4 + 6*i
			s, end, c := int(u16sf(x, at)), int(u16sf(x, at+2)), u16sf(x, at+4)
			if s <= prev || s > end {
				return nil, fontErr("classdef ranges")
			}
			for id := s; id <= end; id++ {
				out[uint16(id)] = c
			}
			prev = end
		}
	default:
		return nil, fontErr("unsupported classdef format")
	}
	return out, nil
}
func gposValueSize(format uint16) (int, error) {
	if format & ^uint16(0x000f) != 0 {
		return 0, fontErr("unsupported GPOS device/variation value format")
	}
	n := 0
	for i := uint(0); i < 4; i++ {
		if format&(1<<i) != 0 {
			n += 2
		}
	}
	return n, nil
}
func gposValue(b []byte, at int, format uint16) ([4]int, error) {
	var out [4]int
	size, e := gposValueSize(format)
	if e != nil {
		return out, e
	}
	if at < 0 || size > len(b)-at {
		return out, fontErr("GPOS value record")
	}
	off := at
	for i := uint(0); i < 4; i++ {
		if format&(1<<i) != 0 {
			out[i] = int(i16sf(b, off))
			off += 2
		}
	}
	return out, nil
}
func gposAdjustment(a, b [4]int) PairAdjustment {
	return PairAdjustment{a[0], a[1], a[2], a[3], b[0], b[1], b[2], b[3]}
}
func parsePairPos(b []byte, at int) (gposPair, error) {
	var result gposPair
	x, e := gposAt(b, at, 10)
	if e != nil {
		return result, e
	}
	format := int(u16sf(x, 0))
	if format != 1 && format != 2 {
		return result, fontErr("unsupported PairPos format")
	}
	coverageAt := int(u16sf(x, 2))
	vf1, vf2 := u16sf(x, 4), u16sf(x, 6)
	sz1, e := gposValueSize(vf1)
	if e != nil {
		return result, e
	}
	sz2, e := gposValueSize(vf2)
	if e != nil {
		return result, e
	}
	cov, e := parseCoverage(x, coverageAt)
	if e != nil {
		return result, e
	}
	result.covered = cov
	result.format = format
	if format == 1 {
		count := int(u16sf(x, 8))
		if count > (len(x)-10)/2 || count != len(cov) {
			return result, fontErr("PairPos pair set count")
		}
		result.sets = make([]map[uint16]PairAdjustment, count)
		for i := 0; i < count; i++ {
			set, e := gposAt(x, int(u16sf(x, 10+i*2)), 2)
			if e != nil {
				return result, e
			}
			n := int(u16sf(set, 0))
			record := 2 + sz1 + sz2
			if n > (len(set)-2)/record {
				return result, fontErr("PairPos pair records")
			}
			entries := make(map[uint16]PairAdjustment, n)
			last := -1
			for j := 0; j < n; j++ {
				off := 2 + j*record
				id := int(u16sf(set, off))
				if id <= last {
					return result, fontErr("PairPos pair records sorting")
				}
				last = id
				a, e := gposValue(set, off+2, vf1)
				if e != nil {
					return result, e
				}
				b, e := gposValue(set, off+2+sz1, vf2)
				if e != nil {
					return result, e
				}
				entries[uint16(id)] = gposAdjustment(a, b)
			}
			result.sets[i] = entries
		}
		return result, nil
	}
	if len(x) < 16 {
		return result, fontErr("short PairPos class format")
	}
	c1off, c2off := int(u16sf(x, 8)), int(u16sf(x, 10))
	c1n, c2n := int(u16sf(x, 12)), int(u16sf(x, 14))
	if c1n < 1 || c2n < 1 || c1n > 256 || c2n > 256 {
		return result, fontErr("PairPos class matrix too large")
	}
	c1, e := parseClassDef(x, c1off)
	if e != nil {
		return result, e
	}
	c2, e := parseClassDef(x, c2off)
	if e != nil {
		return result, e
	}
	for _, v := range c1 {
		if int(v) >= c1n {
			return result, fontErr("PairPos class1 overflow")
		}
	}
	for _, v := range c2 {
		if int(v) >= c2n {
			return result, fontErr("PairPos class2 overflow")
		}
	}
	record := sz1 + sz2
	if record != 0 && c1n*c2n > (len(x)-16)/record {
		return result, fontErr("PairPos matrix length")
	}
	result.class1, result.class2 = c1, c2
	result.matrix = make([][]PairAdjustment, c1n)
	atRec := 16
	for i := 0; i < c1n; i++ {
		result.matrix[i] = make([]PairAdjustment, c2n)
		for j := 0; j < c2n; j++ {
			a, e := gposValue(x, atRec, vf1)
			if e != nil {
				return result, e
			}
			b, e := gposValue(x, atRec+sz1, vf2)
			if e != nil {
				return result, e
			}
			result.matrix[i][j] = gposAdjustment(a, b)
			atRec += record
		}
	}
	return result, nil
}
func (p gposPair) eval(l, r uint16) PairAdjustment {
	idx, ok := p.covered[l]
	if !ok {
		return PairAdjustment{}
	}
	if p.format == 1 {
		return p.sets[idx][r]
	}
	return p.matrix[p.class1[l]][p.class2[r]]
}
func (a *PairAdjustment) add(b PairAdjustment) error {
	x := []*int{&a.FirstXPlacement, &a.FirstYPlacement, &a.FirstXAdvance, &a.FirstYAdvance, &a.SecondXPlacement, &a.SecondYPlacement, &a.SecondXAdvance, &a.SecondYAdvance}
	y := []int{b.FirstXPlacement, b.FirstYPlacement, b.FirstXAdvance, b.FirstYAdvance, b.SecondXPlacement, b.SecondYPlacement, b.SecondXAdvance, b.SecondYAdvance}
	for i := range x {
		if y[i] > 0 && *x[i] > math.MaxInt32-y[i] || y[i] < 0 && *x[i] < math.MinInt32-y[i] {
			return fontErr("GPOS position overflow")
		}
		*x[i] += y[i]
	}
	return nil
}

// GPOSPairLatin applies only kern feature lookups of the default language of
// the 'latn' script, falling back to DFLT. A nil adjustment is NOT proof that
// the full text layout is supported (ligatures/marks/shaping still open).
// If GPOS has no applicable 'kern', returns the legacy kern pair if available.
func (f *SFNT) GPOSPairLatin(left, right uint16) (PairAdjustment, error) {
	var result PairAdjustment
	if f == nil || left >= f.NumGlyphs || right >= f.NumGlyphs {
		return result, fontErr("GPOS glyph indices")
	}
	table, has := f.RawTable("GPOS")
	if !has {
		result.FirstXAdvance = int(f.KernUnits(left, right))
		return result, nil
	}
	if len(table) < 10 || u16sf(table, 0) != 1 {
		return result, fontErr("GPOS header major version")
	}
	minor := u16sf(table, 2)
	if minor > 1 {
		return result, fontErr("unsupported GPOS minor version")
	}
	if minor == 1 {
		if len(table) < 14 {
			return result, fontErr("GPOS 1.1 header truncated")
		}
		if u32sf(table, 10) != 0 {
			return result, fontErr("GPOS feature variations not yet supported")
		}
	}
	scripts, e := gposAt(table, int(u16sf(table, 4)), 2)
	if e != nil {
		return result, e
	}
	features, e := gposAt(table, int(u16sf(table, 6)), 2)
	if e != nil {
		return result, e
	}
	lookups, e := gposAt(table, int(u16sf(table, 8)), 2)
	if e != nil {
		return result, e
	}
	sc := int(u16sf(scripts, 0))
	if sc > (len(scripts)-2)/6 {
		return result, fontErr("GPOS script records")
	}
	scriptOff := -1
	dfltOff := -1
	for i := 0; i < sc; i++ {
		p := 2 + 6*i
		tag := string(scripts[p : p+4])
		off := int(u16sf(scripts, p+4))
		if tag == "latn" {
			scriptOff = off
		}
		if tag == "DFLT" {
			dfltOff = off
		}
	}
	if scriptOff < 0 {
		scriptOff = dfltOff
	}
	if scriptOff < 0 {
		result.FirstXAdvance = int(f.KernUnits(left, right))
		return result, nil
	}
	script, e := gposAt(scripts, scriptOff, 4)
	if e != nil {
		return result, e
	}
	langOff := int(u16sf(script, 0))
	if langOff == 0 {
		result.FirstXAdvance = int(f.KernUnits(left, right))
		return result, nil
	}
	lang, e := gposAt(script, langOff, 6)
	if e != nil {
		return result, e
	}
	featN := int(u16sf(lang, 4))
	if featN > (len(lang)-6)/2 {
		return result, fontErr("GPOS feature indices")
	}
	fc := int(u16sf(features, 0))
	if fc > (len(features)-2)/6 {
		return result, fontErr("GPOS feature list")
	}
	indices := make([]uint16, 0, featN+1)
	required := u16sf(lang, 2)
	if required != 0xffff {
		indices = append(indices, required)
	}
	for i := 0; i < featN; i++ {
		indices = append(indices, u16sf(lang, 6+2*i))
	}
	lookupIDs := make([]uint16, 0, 16)
	for _, id := range indices {
		if int(id) >= fc {
			return result, fontErr("GPOS feature index out of bounds")
		}
		at := 2 + 6*int(id)
		if string(features[at:at+4]) != "kern" {
			continue
		}
		feature, e := gposAt(features, int(u16sf(features, at+4)), 4)
		if e != nil {
			return result, e
		}
		n := int(u16sf(feature, 2))
		if n > (len(feature)-4)/2 {
			return result, fontErr("GPOS feature lookup count")
		}
		for j := 0; j < n; j++ {
			lookupIDs = append(lookupIDs, u16sf(feature, 4+2*j))
		}
	}
	if len(lookupIDs) == 0 {
		result.FirstXAdvance = int(f.KernUnits(left, right))
		return result, nil
	}
	lc := int(u16sf(lookups, 0))
	if lc > (len(lookups)-2)/2 {
		return result, fontErr("GPOS lookup list count")
	}
	for _, id := range lookupIDs {
		if int(id) >= lc {
			return result, fontErr("GPOS lookup index")
		}
		lookup, e := gposAt(lookups, int(u16sf(lookups, 2+2*int(id))), 6)
		if e != nil {
			return result, e
		}
		typ, flags, n := u16sf(lookup, 0), u16sf(lookup, 2), int(u16sf(lookup, 4))
		if flags&0x00e0 != 0 {
			return result, fontErr("GPOS unknown lookup flags")
		}
		// Pair positioning with mark filters is safe for verified base glyphs:
		// neither participant can be skipped by IgnoreMarks/MarkAttachmentType.
		// If glyph classes are unknown, fail closed rather than silently
		// applying or ignoring a positioning lookup.
		if flags&0xff1e != 0 {
			gdef, exists := f.RawTable("GDEF")
			if !exists || len(gdef) < 12 || u16sf(gdef, 0) != 1 || u16sf(gdef, 4) == 0 {
				return result, fontErr("GPOS lookup flags require GDEF glyph classes")
			}
			classes, err := parseClassDef(gdef, int(u16sf(gdef, 4)))
			if err != nil {
				return result, err
			}
			if classes[left] != 1 || classes[right] != 1 {
				return result, fontErr("GPOS lookup flags on non-base or unknown glyph")
			}
		}
		extra := 0
		if flags&0x0010 != 0 {
			extra = 2
		}
		if n > (len(lookup)-6-extra)/2 {
			return result, fontErr("GPOS subtable count/mark filtering")
		}
		for j := 0; j < n; j++ {
			sub, e := gposAt(lookup, int(u16sf(lookup, 6+2*j)), 2)
			if e != nil {
				return result, e
			}
			t := typ
			if t == 9 {
				if len(sub) < 8 || u16sf(sub, 0) != 1 || u16sf(sub, 2) != 2 {
					return result, fontErr("unsupported GPOS extension type")
				}
				off := u32sf(sub, 4)
				if off > uint32(len(sub)) {
					return result, fontErr("GPOS extension range")
				}
				sub, e = gposAt(sub, int(off), 2)
				if e != nil {
					return result, e
				}
				t = 2
			}
			if t != 2 {
				return result, fmt.Errorf("%w: GPOS kern lookup type %d", ErrFont, t)
			}
			pair, e := parsePairPos(sub, 0)
			if e != nil {
				return result, e
			}
			if e = result.add(pair.eval(left, right)); e != nil {
				return result, e
			}
		}
	}
	return result, nil
}
