package graphicsgo

// Read-only SFNT table/cmap/metric reader. This is deliberately independent of
// C, cgo, Python, and external Go modules. CFF outlines and complex shaping
// are NOT implemented by this reader. Caller-supplied font bytes stay private.
import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var ErrFont = errors.New("graphicsgo: invalid or unsupported SFNT font")

type sfntTable struct{ start, length int }
type FontOutlineFormat string

const (
	TrueTypeOutline FontOutlineFormat = "TrueType-glyf"
	CFF1Outline     FontOutlineFormat = "OpenType-CFF1"
)

type SFNT struct {
	data                     []byte
	tables                   map[string]sfntTable
	Outline                  FontOutlineFormat
	UnitsPerEm               uint16
	NumGlyphs                uint16
	Ascent, Descent, LineGap int16
	numHMetrics              uint16
	indexToLocFormat         int16
	cmap                     []byte
	cmapFormat               uint16
	kern                     map[uint32]int16
	HasGPOS                  bool
}

func u16sf(b []byte, n int) uint16 { return binary.BigEndian.Uint16(b[n : n+2]) }
func i16sf(b []byte, n int) int16  { return int16(u16sf(b, n)) }
func u32sf(b []byte, n int) uint32 { return binary.BigEndian.Uint32(b[n : n+4]) }
func fontErr(s string) error       { return fmt.Errorf("%w: %s", ErrFont, s) }
func ParseSFNT(data []byte) (*SFNT, error) {
	if len(data) < 12 || len(data) > 128<<20 {
		return nil, fontErr("size")
	}
	magic := u32sf(data, 0)
	if magic != 0x00010000 && magic != 0x4f54544f {
		return nil, fontErr("signature (collections/WOFF unsupported)")
	}
	n := int(u16sf(data, 4))
	if n == 0 || n > 256 || n > (len(data)-12)/16 {
		return nil, fontErr("table count")
	}
	f := &SFNT{data: data, tables: make(map[string]sfntTable, n)}
	ranges := make([]sfntTable, 0, n)
	for i := 0; i < n; i++ {
		at := 12 + i*16
		tag := string(data[at : at+4])
		off := uint64(u32sf(data, at+8))
		length := uint64(u32sf(data, at+12))
		if _, ok := f.tables[tag]; ok {
			return nil, fontErr("duplicate " + tag)
		}
		if off > uint64(len(data)) || length > uint64(len(data))-off {
			return nil, fontErr("table range " + tag)
		}
		if length > 0 && off < uint64(12+n*16) {
			return nil, fontErr("table overlaps directory")
		}
		f.tables[tag] = sfntTable{int(off), int(length)}
		if length > 0 {
			ranges = append(ranges, sfntTable{int(off), int(length)})
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	for i := 1; i < len(ranges); i++ {
		if ranges[i-1].start+ranges[i-1].length > ranges[i].start {
			return nil, fontErr("table overlap")
		}
	}
	get := func(tag string, min int) ([]byte, error) {
		t, ok := f.tables[tag]
		if !ok || t.length < min {
			return nil, fontErr("required table " + tag)
		}
		return data[t.start : t.start+t.length], nil
	}
	head, e := get("head", 54)
	if e != nil {
		return nil, e
	}
	if u32sf(head, 12) != 0x5f0f3cf5 {
		return nil, fontErr("head magic")
	}
	f.UnitsPerEm = u16sf(head, 18)
	if f.UnitsPerEm < 16 || f.UnitsPerEm > 16384 {
		return nil, fontErr("units per em")
	}
	f.indexToLocFormat = i16sf(head, 50)
	if f.indexToLocFormat != 0 && f.indexToLocFormat != 1 {
		return nil, fontErr("loca format")
	}
	hhea, e := get("hhea", 36)
	if e != nil {
		return nil, e
	}
	f.Ascent, f.Descent, f.LineGap = i16sf(hhea, 4), i16sf(hhea, 6), i16sf(hhea, 8)
	f.numHMetrics = u16sf(hhea, 34)
	maxp, e := get("maxp", 6)
	if e != nil {
		return nil, e
	}
	f.NumGlyphs = u16sf(maxp, 4)
	if f.NumGlyphs == 0 || f.numHMetrics == 0 || f.numHMetrics > f.NumGlyphs {
		return nil, fontErr("glyph count/metrics")
	}
	_, e = get("hmtx", 4*int(f.numHMetrics)+2*(int(f.NumGlyphs)-int(f.numHMetrics)))
	if e != nil {
		return nil, e
	}
	_, glyf := f.tables["glyf"]
	_, loca := f.tables["loca"]
	_, cff := f.tables["CFF "]
	_, cff2 := f.tables["CFF2"]
	if cff2 {
		return nil, fontErr("CFF2 variable outlines not supported")
	}
	if glyf && loca && !cff {
		f.Outline = TrueTypeOutline
		size := int(f.NumGlyphs) + 1
		if f.indexToLocFormat == 0 {
			size *= 2
		} else {
			size *= 4
		}
		if _, e = get("loca", size); e != nil {
			return nil, e
		}
	} else if cff && !glyf && !loca {
		f.Outline = CFF1Outline
		if _, e = get("CFF ", 4); e != nil {
			return nil, e
		}
	} else {
		return nil, fontErr("missing/ambiguous glyf/loca or CFF outlines")
	}
	f.HasGPOS = f.tables["GPOS"].length > 0
	cmap, e := get("cmap", 4)
	if e != nil {
		return nil, e
	}
	count := int(u16sf(cmap, 2))
	if count <= 0 || count > 4096 || count > (len(cmap)-4)/8 {
		return nil, fontErr("cmap table count")
	}
	bestScore := -1
	for i := 0; i < count; i++ {
		at := 4 + i*8
		plat, enc := u16sf(cmap, at), u16sf(cmap, at+2)
		off := uint64(u32sf(cmap, at+4))
		if off+4 > uint64(len(cmap)) {
			continue
		}
		v := cmap[off:]
		format := u16sf(v, 0)
		score := 0
		if format == 12 && len(v) >= 16 {
			length := uint64(u32sf(v, 4))
			if length < 16 || length > uint64(len(v)) {
				continue
			}
			if plat == 3 && enc == 10 {
				score = 10
			} else if plat == 0 {
				score = 9
			} else {
				score = 3
			}
			v = v[:length]
		} else if format == 4 && len(v) >= 16 {
			length := uint64(u16sf(v, 2))
			if length < 16 || length > uint64(len(v)) {
				continue
			}
			if plat == 3 && enc == 1 {
				score = 8
			} else if plat == 0 {
				score = 7
			} else {
				score = 2
			}
			v = v[:length]
		} else {
			continue
		}
		if score > bestScore {
			bestScore = score
			f.cmap = v
			f.cmapFormat = format
		}
	}
	if bestScore < 0 {
		return nil, fontErr("unsupported cmap")
	}
	if f.cmapFormat == 4 {
		if e = f.validateCmap4(); e != nil {
			return nil, e
		}
	} else if e = f.validateCmap12(); e != nil {
		return nil, e
	}
	// Legacy kern version 0, horizontal format 0 only; no assumptions about GPOS.
	if t, ok := f.tables["kern"]; ok && t.length >= 4 {
		b := data[t.start : t.start+t.length]
		if u16sf(b, 0) == 0 {
			pairs := make(map[uint32]int16)
			nt := int(u16sf(b, 2))
			at := 4
			for i := 0; i < nt; i++ {
				if len(b)-at < 6 {
					return nil, fontErr("truncated kern")
				}
				length := int(u16sf(b, at+2))
				if length < 6 || length > len(b)-at {
					return nil, fontErr("kern length")
				}
				cover := u16sf(b, at+4)
				if cover == 0x0001 {
					sub := b[at : at+length]
					if len(sub) < 14 {
						return nil, fontErr("kern pair header")
					}
					num := int(u16sf(sub, 6))
					if 14+num*6 > len(sub) {
						return nil, fontErr("kern pair count")
					}
					for j := 0; j < num; j++ {
						p := 14 + j*6
						k := u32sf(sub, p)
						pairs[k] = i16sf(sub, p+4)
					}
				}
				at += length
			}
			f.kern = pairs
		}
	}
	return f, nil
}
func (f *SFNT) validateCmap12() error {
	b := f.cmap
	n := int(u32sf(b, 12))
	if n == 0 || n > 100000 || uint64(n)*12 > uint64(len(b)-16) {
		return fontErr("format12 groups")
	}
	var prev uint32
	for i := 0; i < n; i++ {
		o := 16 + i*12
		s, e := u32sf(b, o), u32sf(b, o+4)
		if s > e || e > 0x10ffff || (i > 0 && s <= prev) {
			return fontErr("format12 unsorted ranges")
		}
		prev = e
	}
	return nil
}
func (f *SFNT) validateCmap4() error {
	b := f.cmap
	segx2 := int(u16sf(b, 6))
	if segx2 == 0 || segx2&1 != 0 || segx2 > 32768 {
		return fontErr("format4 segment count")
	}
	n := segx2 / 2
	if 16+n*8 > len(b) {
		return fontErr("format4 boundaries")
	}
	prev := -1
	for i := 0; i < n; i++ {
		end := int(u16sf(b, 14+i*2))
		start := int(u16sf(b, 16+n*2+i*2))
		if start > end || end <= prev {
			return fontErr("format4 unordered ranges")
		}
		prev = end
	}
	return nil
}

// GlyphIndex resolves a Unicode scalar from the selected cmap; unmapped -> error,
// not silent .notdef substitution in game text.
func (f *SFNT) GlyphIndex(r rune) (uint16, error) {
	if f == nil || r < 0 || r > 0x10ffff || r >= 0xd800 && r <= 0xdfff {
		return 0, fontErr("rune")
	}
	b := f.cmap
	var index uint32
	if f.cmapFormat == 12 {
		n := int(u32sf(b, 12))
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			o := 16 + 12*m
			if uint32(r) > u32sf(b, o+4) {
				lo = m + 1
			} else {
				hi = m
			}
		}
		if lo < n {
			o := 16 + 12*lo
			if uint32(r) >= u32sf(b, o) {
				index = u32sf(b, o+8) + uint32(r) - u32sf(b, o)
			}
		}
	} else if r <= 0xffff {
		n := int(u16sf(b, 6)) / 2
		lo, hi := 0, n
		for lo < hi {
			m := (lo + hi) / 2
			if uint32(r) > uint32(u16sf(b, 14+m*2)) {
				lo = m + 1
			} else {
				hi = m
			}
		}
		if lo < n {
			start := u16sf(b, 16+2*n+lo*2)
			if uint16(r) >= start {
				delta := u16sf(b, 16+4*n+lo*2)
				rangeOffsetAt := 16 + 6*n + 2*lo
				off := int(u16sf(b, rangeOffsetAt))
				if off == 0 {
					index = uint32(uint16(r) + delta)
				} else {
					where := rangeOffsetAt + off + 2*(int(r)-int(start))
					if where < 0 || where+2 > len(b) {
						return 0, fontErr("cmap4 glyph array range")
					}
					v := u16sf(b, where)
					if v != 0 {
						index = uint32(v + delta)
					}
				}
			}
		}
	}
	if index == 0 || index >= uint32(f.NumGlyphs) {
		return 0, fontErr(fmt.Sprintf("unmapped glyph U+%04X", r))
	}
	return uint16(index), nil
}

// HMetric returns unscaled glyph advance and left side bearing in font units.
func (f *SFNT) HMetric(gid uint16) (uint16, int16, error) {
	if f == nil || gid >= f.NumGlyphs {
		return 0, 0, fontErr("glyph index")
	}
	t := f.tables["hmtx"]
	b := f.data[t.start : t.start+t.length]
	n := int(f.numHMetrics)
	idx := int(gid)
	if idx < n {
		return u16sf(b, idx*4), i16sf(b, idx*4+2), nil
	}
	return u16sf(b, (n-1)*4), i16sf(b, n*4+2*(idx-n)), nil
}

// KernUnits returns classic 'kern' horizontal pair correction. Fonts using
// GPOS are flagged; client must not claim complete kerning/shaping parity.
func (f *SFNT) KernUnits(left, right uint16) int16 {
	if f == nil {
		return 0
	}
	return f.kern[uint32(left)<<16|uint32(right)]
}
func (f *SFNT) RawTable(tag string) ([]byte, bool) {
	if f == nil {
		return nil, false
	}
	t, ok := f.tables[tag]
	if !ok {
		return nil, false
	}
	return f.data[t.start : t.start+t.length], true
}
