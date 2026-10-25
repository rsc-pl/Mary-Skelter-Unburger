package graphicsgo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"os"
	"sort"
	"testing"
)

func put16(b []byte, n int, v uint16) { binary.BigEndian.PutUint16(b[n:n+2], v) }
func put32(b []byte, n int, v uint32) { binary.BigEndian.PutUint32(b[n:n+4], v) }
func sampleSimpleGlyph(coords [][2]int16) []byte {
	var bb bytes.Buffer
	p16 := func(v uint16) { _ = binary.Write(&bb, binary.BigEndian, v) }
	p16(1)
	p16(0)
	p16(0)
	p16(400)
	p16(650)
	p16(uint16(len(coords) - 1))
	p16(0)
	for range coords {
		bb.WriteByte(1)
	}
	x := int16(0)
	for _, p := range coords {
		p16(uint16(p[0] - x))
		x = p[0]
	}
	y := int16(0)
	for _, p := range coords {
		p16(uint16(p[1] - y))
		y = p[1]
	}
	return bb.Bytes()
}
func cffTestIndex(objects ...[]byte) []byte {
	if len(objects) == 0 {
		return []byte{0, 0}
	}
	total := 0
	for _, x := range objects {
		total += len(x)
	}
	b := make([]byte, 2+1+len(objects)+1, 2+1+len(objects)+1+total)
	put16(b, 0, uint16(len(objects)))
	b[2] = 1
	off := 1
	for i, x := range objects {
		b[3+i] = byte(off)
		off += len(x)
	}
	b[3+len(objects)] = byte(off)
	for _, x := range objects {
		b = append(b, x...)
	}
	return b
}
func cffTestNumber(n int) []byte {
	if n >= -107 && n <= 107 {
		return []byte{byte(n + 139)}
	}
	if n >= 108 && n <= 1131 {
		v := n - 108
		return []byte{byte(247 + v/256), byte(v % 256)}
	}
	if n <= -108 && n >= -1131 {
		v := -n - 108
		return []byte{byte(251 + v/256), byte(v % 256)}
	}
	b := []byte{28, 0, 0}
	put16(b, 1, uint16(int16(n)))
	return b
}
func cffTestGlyph(points [][2]int) []byte {
	b := []byte{}
	appendNum := func(n int) { b = append(b, cffTestNumber(n)...) }
	appendNum(0)
	appendNum(0)
	b = append(b, 21)
	x, y := 0, 0
	for _, p := range points {
		appendNum(p[0] - x)
		appendNum(p[1] - y)
		x, y = p[0], p[1]
	}
	b = append(b, 5, 14)
	return b
}
func syntheticCFF1() []byte {
	name := cffTestIndex([]byte{'T'})
	glyph0 := []byte{14}
	glyph1 := cffTestGlyph([][2]int{{350, 0}, {350, 650}, {0, 650}, {0, 0}})
	glyph2 := cffTestGlyph([][2]int{{350, 0}, {200, 650}, {0, 0}})
	chars := cffTestIndex(glyph0, glyph1, glyph2)
	// TopDict contains absolute CharStrings offset as a DICT long integer.
	top := []byte{29, 0, 0, 0, 0, 17}
	topIndex := cffTestIndex(top)
	csOff := 4 + len(name) + len(topIndex) + 2 + 2
	put32(top, 1, uint32(csOff))
	topIndex = cffTestIndex(top)
	b := []byte{1, 0, 4, 4}
	b = append(b, name...)
	b = append(b, topIndex...)
	b = append(b, 0, 0, 0, 0)
	b = append(b, chars...)
	return b
}
func syntheticFont(otf bool) []byte {
	tables := map[string][]byte{}
	head := make([]byte, 54)
	put32(head, 12, 0x5f0f3cf5)
	put16(head, 18, 1000)
	put16(head, 50, 1)
	tables["head"] = head
	hhea := make([]byte, 36)
	put16(hhea, 4, 800)
	put16(hhea, 6, 0xFF38)
	put16(hhea, 34, 2)
	tables["hhea"] = hhea
	maxp := make([]byte, 6)
	put32(maxp, 0, 0x10000)
	put16(maxp, 4, 3)
	tables["maxp"] = maxp
	hmtx := make([]byte, 10)
	put16(hmtx, 0, 500)
	put16(hmtx, 4, 600)
	put16(hmtx, 8, 20)
	tables["hmtx"] = hmtx
	cm4 := make([]byte, 40)
	put16(cm4, 0, 4)
	put16(cm4, 2, 40)
	put16(cm4, 6, 6)
	for i, v := range []uint16{65, 86, 65535} {
		put16(cm4, 14+i*2, v)
		put16(cm4, 22+i*2, v)
	}
	put16(cm4, 28, 0xFFC0)
	put16(cm4, 30, 0xFFAC)
	put16(cm4, 32, 1)
	cmap := make([]byte, 12+len(cm4))
	put16(cmap, 2, 1)
	put16(cmap, 4, 3)
	put16(cmap, 6, 1)
	put32(cmap, 8, 12)
	copy(cmap[12:], cm4)
	tables["cmap"] = cmap
	kern := make([]byte, 26)
	put16(kern, 2, 1)
	put16(kern, 6, 26-4)
	put16(kern, 8, 1)
	put16(kern, 10, 1)
	put32(kern, 18, 1<<16|2)
	put16(kern, 22, 0xFFE7)
	tables["kern"] = kern
	if otf {
		tables["CFF "] = syntheticCFF1()
	} else {
		glyph1 := sampleSimpleGlyph([][2]int16{{0, 0}, {350, 0}, {350, 650}, {0, 650}})
		glyph2 := sampleSimpleGlyph([][2]int16{{0, 0}, {350, 0}, {200, 650}})
		glyf := append(append([]byte{}, glyph1...), glyph2...)
		tables["glyf"] = glyf
		loca := make([]byte, 16)
		put32(loca, 0, 0)
		put32(loca, 4, 0)
		put32(loca, 8, uint32(len(glyph1)))
		put32(loca, 12, uint32(len(glyf)))
		tables["loca"] = loca
	}
	names := make([]string, 0, len(tables))
	for tag := range tables {
		names = append(names, tag)
	}
	sort.Strings(names)
	dir := make([]byte, 12+16*len(names))
	if otf {
		put32(dir, 0, 0x4f54544f)
	} else {
		put32(dir, 0, 0x00010000)
	}
	put16(dir, 4, uint16(len(names)))
	out := dir
	for i, tag := range names {
		data := tables[tag]
		offset := len(out)
		at := 12 + 16*i
		copy(out[at:], tag)
		put32(out, at+8, uint32(offset))
		put32(out, at+12, uint32(len(data)))
		out = append(out, data...)
		for len(out)%4 != 0 {
			out = append(out, 0)
		}
	}
	return out
}
func TestSFNTFormat4AndMetrics(t *testing.T) {
	for _, otf := range []bool{false, true} {
		f, e := ParseSFNT(syntheticFont(otf))
		if e != nil {
			t.Fatalf("parse otf=%t: %v", otf, e)
		}
		if otf && f.Outline != CFF1Outline || !otf && f.Outline != TrueTypeOutline {
			t.Fatalf("outline %s", f.Outline)
		}
		id, e := f.GlyphIndex('A')
		if e != nil || id != 1 {
			t.Fatalf("A %d %v", id, e)
		}
		id, e = f.GlyphIndex('V')
		if e != nil || id != 2 {
			t.Fatalf("V %d %v", id, e)
		}
		if _, e = f.GlyphIndex('x'); e == nil {
			t.Fatal("unmapped must fail")
		}
		advance, lsb, e := f.HMetric(2)
		if e != nil || advance != 600 || lsb != 20 {
			t.Fatalf("hmtx %d %d %v", advance, lsb, e)
		}
		// kern is optional and must not invent a nonzero pair.
		if f.KernUnits(1, 1) != 0 {
			t.Fatal("nonexistent kern pair")
		}
	}
}
func TestNativeTrueTypeRaster(t *testing.T) {
	f, e := ParseSFNT(syntheticFont(false))
	if e != nil {
		t.Fatal(e)
	}
	for _, gid := range []uint16{1, 2} {
		mask, offset, e := f.RasterGlyphTTF(gid, 40, 4)
		if e != nil {
			t.Fatal(e)
		}
		if mask.Bounds().Empty() || offset.Y >= 0 {
			t.Fatalf("glyph %d bad raster %v %v", gid, mask.Bounds(), offset)
		}
		var nonzero int
		for _, a := range mask.Pix {
			if a != 0 {
				nonzero++
			}
		}
		if nonzero < 40 {
			t.Fatalf("empty glyph %d", gid)
		}
	}
	if _, _, e := f.RasterGlyphTTF(1, 40, 3); e == nil {
		t.Fatal("invalid samples accepted")
	}
	f, e = ParseSFNT(syntheticFont(true))
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = f.RasterGlyphTTF(1, 40, 4); e == nil {
		t.Fatal("CFF wrongly passed to TTF raster")
	}
	if o, e := f.GlyphOutlineCFF1(1); e != nil || len(o) != 1 {
		t.Fatalf("CFF synthetic charstring %d %v", len(o), e)
	}
	if mask, _, e := f.RasterGlyphCFF1(1, 40, 4); e != nil || mask.Bounds().Empty() {
		t.Fatalf("CFF synthetic raster %v", e)
	}
}
func TestSFNTMalformedGuards(t *testing.T) {
	base := syntheticFont(false)
	cases := [][]byte{nil, []byte{0, 1, 2}, append([]byte{}, base[:25]...)}
	altered := append([]byte{}, base...)
	put32(altered, 12+8, 0xfffffff0)
	cases = append(cases, altered)
	altered = append([]byte{}, base...)
	copy(altered[12+16:], altered[12:12+4])
	cases = append(cases, altered)
	for i, b := range cases {
		if _, e := ParseSFNT(b); e == nil {
			t.Fatalf("invalid font case %d accepted", i)
		} else if !errors.Is(e, ErrFont) {
			t.Fatalf("invalid error wrapping %v", e)
		}
	}
}
func TestOptionalLocalPublicFontProbe(t *testing.T) {
	p := os.Getenv("MARY_SKELTER_TEST_PUBLIC_FONT")
	if p == "" {
		t.Skip("set MARY_SKELTER_TEST_PUBLIC_FONT to a locally licensed TTF or CFF OTF")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	f, e := ParseSFNT(b)
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("font outline=%s glyphs=%d units=%d gpos=%v", f.Outline, f.NumGlyphs, f.UnitsPerEm, f.HasGPOS)
	a, e := f.GlyphIndex('A')
	if e == nil {
		mask, _, e := f.RasterGlyph(a, 64, 4)
		if e != nil {
			t.Fatal(e)
		}
		if mask.Bounds() == image.ZR {
			t.Fatal("empty glyph")
		}
	}
}
