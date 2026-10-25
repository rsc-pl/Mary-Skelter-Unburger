package graphicsgo

import "testing"

func TestGPOSMinor11DefaultStatic(t *testing.T) {
	orig := pairGPOS(false)
	x := append(append(append([]byte{}, orig[:10]...), 0, 0, 0, 0), orig[10:]...)
	for _, at := range []int{4, 6, 8} {
		put16(x, at, u16sf(x, at)+4)
	}
	put16(x, 2, 1)
	f, e := ParseSFNT(addSFNTTable(syntheticFont(true), "GPOS", x))
	if e != nil {
		t.Fatal(e)
	}
	got, e := f.GPOSPairLatin(1, 2)
	if e != nil || got.FirstXAdvance != -73 {
		t.Fatalf("static 1.1: %+v %v", got, e)
	}
	put32(x, 10, 15)
	f, e = ParseSFNT(addSFNTTable(syntheticFont(true), "GPOS", x))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.GPOSPairLatin(1, 2); e == nil {
		t.Fatal("accepted unimplemented feature variation")
	}
}
func TestGPOSLookupMarkFlagsOnlyWithGDEFBaseProof(t *testing.T) {
	gpos := pairGPOS(false)
	// The lookup sits at GPOS offset 10 + ScriptList(20) + FeatureList(14) + LookupList(4).
	lookupAt := 10 + 20 + 14 + 4
	put16(gpos, lookupAt+2, 8) // IgnoreMarks
	f, e := ParseSFNT(addSFNTTable(syntheticFont(true), "GPOS", gpos))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.GPOSPairLatin(1, 2); e == nil {
		t.Fatal("GDEF-less mark lookup not rejected")
	}
	// Glyph class definition format1, glyphs 1 and 2 are both base glyphs.
	gdef := make([]byte, 12+10)
	put16(gdef, 0, 1)
	put16(gdef, 4, 12)
	put16(gdef, 12, 1)
	put16(gdef, 14, 1)
	put16(gdef, 16, 2)
	put16(gdef, 18, 1)
	put16(gdef, 20, 1)
	f, e = ParseSFNT(addSFNTTable(addSFNTTable(syntheticFont(true), "GPOS", gpos), "GDEF", gdef))
	if e != nil {
		t.Fatal(e)
	}
	p, e := f.GPOSPairLatin(1, 2)
	if e != nil || p.FirstXAdvance != -73 {
		t.Fatalf("GDEF proven bases %+v %v", p, e)
	}
	put16(gdef, 20, 3) // Mark right glyph: must fail rather than misinterpret
	f, e = ParseSFNT(addSFNTTable(addSFNTTable(syntheticFont(true), "GPOS", gpos), "GDEF", gdef))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.GPOSPairLatin(1, 2); e == nil {
		t.Fatal("mark glyph wrongly treated as base")
	}
}
