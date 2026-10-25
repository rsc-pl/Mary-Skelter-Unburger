package graphicsgo

import (
	"encoding/binary"
	"os"
	"testing"
)

// Add a synthetic independently-controlled table without writing a font to disk.
func addSFNTTable(orig []byte, tag string, data []byte) []byte {
	n := int(u16sf(orig, 4))
	delta := 16
	out := make([]byte, len(orig)+delta+len(data))
	copy(out[:12], orig[:12])
	put16(out, 4, uint16(n+1))
	copy(out[12:12+n*16], orig[12:12+n*16])
	copy(out[12+(n+1)*16:], orig[12+n*16:])
	for i := 0; i < n; i++ {
		at := 12 + 16*i
		put32(out, at+8, u32sf(out, at+8)+uint32(delta))
	}
	at := 12 + 16*n
	copy(out[at:at+4], tag)
	put32(out, at+8, uint32(len(orig)+delta))
	put32(out, at+12, uint32(len(data)))
	copy(out[len(orig)+delta:], data)
	return out
}
func pairGPOS(class bool) []byte {
	// ScriptList: latn default langsys, feature index 0.
	scripts := []byte{0, 1, 'l', 'a', 't', 'n', 0, 8, 0, 4, 0, 0, 0, 0, 0xff, 0xff, 0, 1, 0, 0}
	// FeatureList: kern feature -> lookup index 0.
	features := []byte{0, 1, 'k', 'e', 'r', 'n', 0, 8, 0, 0, 0, 1, 0, 0}
	// Lookup type 2 (PairPos) with a single subtable at offset 8.
	lookup := []byte{0, 2, 0, 0, 0, 1, 0, 8}
	var sub []byte
	if !class { // PairPos format1, first GID 1, second GID 2, XAdvance -73
		sub = make([]byte, 24)
		put16(sub, 0, 1)
		put16(sub, 2, 18)
		put16(sub, 4, 4)
		put16(sub, 6, 0)
		put16(sub, 8, 1)
		put16(sub, 10, 12)
		put16(sub, 12, 1)
		put16(sub, 14, 2)
		put16(sub, 16, 0xFFB7)
		put16(sub, 18, 1)
		put16(sub, 20, 1)
		put16(sub, 22, 1)
	} else { // Format2: 2x2 class matrix; value class 1,1 -> -91.
		sub = make([]byte, 16+2*2*2+6+8+8)
		put16(sub, 0, 2)
		put16(sub, 2, 24)
		put16(sub, 4, 4)
		put16(sub, 8, 30)
		put16(sub, 10, 38)
		put16(sub, 12, 2)
		put16(sub, 14, 2)
		put16(sub, 16+6, 0xFFA5)
		put16(sub, 24, 1)
		put16(sub, 26, 1)
		put16(sub, 28, 1) // coverage
		put16(sub, 30, 1)
		put16(sub, 32, 1)
		put16(sub, 34, 1)
		put16(sub, 36, 1) // class1
		put16(sub, 38, 1)
		put16(sub, 40, 2)
		put16(sub, 42, 1)
		put16(sub, 44, 1) // class2
	}
	lookup = append(lookup, sub...)
	ll := []byte{0, 1, 0, 4}
	ll = append(ll, lookup...)
	out := make([]byte, 10)
	put16(out, 0, 1)
	put16(out, 4, 10)
	put16(out, 6, uint16(10+len(scripts)))
	put16(out, 8, uint16(10+len(scripts)+len(features)))
	out = append(out, scripts...)
	out = append(out, features...)
	out = append(out, ll...)
	return out
}
func TestGPOSLatinPairFormatOneAndTwo(t *testing.T) {
	for _, class := range []bool{false, true} {
		f, e := ParseSFNT(addSFNTTable(syntheticFont(true), "GPOS", pairGPOS(class)))
		if e != nil {
			t.Fatalf("sfnt %v: %v", class, e)
		}
		p, e := f.GPOSPairLatin(1, 2)
		want := -73
		if class {
			want = -91
		}
		if e != nil || p.FirstXAdvance != want {
			t.Errorf("class %v: %v %+v expected %d", class, e, p, want)
		}
		p, e = f.GPOSPairLatin(2, 1)
		if e != nil || p != (PairAdjustment{}) {
			t.Errorf("unexpected pair %v %+v", e, p)
		}
	}
}
func TestGPOSMalformedAndKernFallback(t *testing.T) {
	f, e := ParseSFNT(syntheticFont(true))
	if e != nil {
		t.Fatal(e)
	}
	legacy := f.KernUnits(1, 2)
	p, e := f.GPOSPairLatin(1, 2)
	if e != nil || p.FirstXAdvance != int(legacy) {
		t.Fatalf("fallback %+v %v wanted %d", p, e, legacy)
	}
	b := pairGPOS(false)
	binary.BigEndian.PutUint16(b[8:10], 0xffff)
	f, e = ParseSFNT(addSFNTTable(syntheticFont(true), "GPOS", b))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.GPOSPairLatin(1, 2); e == nil {
		t.Fatal("GPOS broken offset accepted")
	}
	if _, e = f.GPOSPairLatin(65535, 0); e == nil {
		t.Fatal("invalid glyph accepted")
	}
}
func TestOptionalPublicGPOSExample(t *testing.T) {
	const path = "/usr/share/fonts/truetype/adf/AccanthisADFStd-Regular.otf"
	b, e := os.ReadFile(path)
	if e != nil {
		t.Skip("public test font unavailable")
	}
	f, e := ParseSFNT(b)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := f.GlyphIndex('A')
	v, _ := f.GlyphIndex('V')
	p, e := f.GPOSPairLatin(a, v)
	if e != nil || p.FirstXAdvance != -95 {
		t.Fatalf("public font AV: %+v %v", p, e)
	}
}

func TestOptionalPublicClassGPOSAndMalformedMutations(t *testing.T) {
	b, e := os.ReadFile("/usr/share/fonts/truetype/adf/BerenisADFPro-Bold.otf")
	if e == nil {
		// The public test font stores its class PairPos kern feature under DFLT;
		// its latn defaults to cpsp alone. Rename latn to an unused tag in a
		// throwaway in-memory copy, so the independent DFLT class path is tested.
		b = append([]byte(nil), b...)
		n := int(u16sf(b, 4))
		off, length := 0, 0
		for i := 0; i < n; i++ {
			at := 12 + 16*i
			if string(b[at:at+4]) == "GPOS" {
				off = int(u32sf(b, at+8))
				length = int(u32sf(b, at+12))
				break
			}
		}
		found := false
		for i := off; i+4 <= off+length; i++ {
			if string(b[i:i+4]) == "latn" {
				copy(b[i:i+4], "latx")
				found = true
				break
			}
		}
		if !found {
			t.Fatal("public test has no latn")
		}
		f, err := ParseSFNT(b)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := f.GlyphIndex('A')
		c, _ := f.GlyphIndex('C')
		p, err := f.GPOSPairLatin(a, c)
		if err != nil || p.FirstXAdvance != -40 {
			t.Errorf("public format2 A-C mismatch %+v %v", p, err)
		}
	}
	// Deterministic mutations of a valid table must return values/errors,
	// never escape a slice; no C/Python/private fonts are needed.
	b = pairGPOS(true)
	for at := 0; at < len(b); at++ {
		mod := append([]byte(nil), b...)
		mod[at] ^= 0xff
		f, err := ParseSFNT(addSFNTTable(syntheticFont(true), "GPOS", mod))
		if err == nil {
			_, _ = f.GPOSPairLatin(1, 2)
		}
	}
	for at := 0; at < len(b); at++ {
		mod := append([]byte(nil), b[:at]...)
		f, err := ParseSFNT(addSFNTTable(syntheticFont(true), "GPOS", mod))
		if err == nil {
			_, _ = f.GPOSPairLatin(1, 2)
		}
	}
}
