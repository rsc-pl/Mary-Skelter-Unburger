package mskit

import (
	"bytes"
	"testing"
)

func TestGoNestedGBNLSTCMCL3BILZTree(t *testing.T) {
	gnl := sharedGBNL(t)
	stcm, off, _, _ := stcmDeadCodeFixture()
	seeded, e := RebuildSTCMRelayout(stcm, map[int][]byte{off: gnl})
	if e != nil {
		t.Fatal(e)
	}
	c, e := ParseCL3(synthCL3(t, false))
	if e != nil {
		t.Fatal(e)
	}
	cl3, e := c.Rebuild(map[int][]byte{0: seeded})
	if e != nil {
		t.Fatal(e)
	}
	orig := make([]byte, 0x80)
	wrapped, e := WrapBILZGo(cl3, 7, 7)
	if e != nil {
		t.Fatal(e)
	}
	orig = append(orig, wrapped...)
	edits := []GBNLTextEdit{
		{CL3Index: 0, BlobOffset: off, Message: 0, Field: 0, Old: []byte("Snow White..."), New: []byte("Yuki no Shirayuki!")},
		{CL3Index: 0, BlobOffset: off, Message: 1, Field: 0, Old: []byte("Snow White..."), New: []byte("Shirayuki-chan!")},
	}
	result, e := RebuildGBNLTree(orig, edits, WrapBILZGo)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(result, orig) {
		t.Fatal("nested edit didn't update bytes")
	}
	plain, _, e := DecodeBILZWrapper(result)
	if e != nil {
		t.Fatal(e)
	}
	changedCL, e := ParseCL3(plain)
	if e != nil {
		t.Fatal(e)
	}
	member, e := changedCL.FileData(0)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseSTCM(member)
	if e != nil {
		t.Fatal(e)
	}
	item, ok := parsed.DataItems[off]
	if !ok {
		t.Fatal("STCM original Data item disappeared")
	}
	editedGBNL, e := ParseGBNL(item.Payload)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := editedGBNL.Field(0, 0)
	b, _ := editedGBNL.Field(1, 0)
	if string(a.StrRaw) != "Yuki no Shirayuki!" || string(b.StrRaw) != "Shirayuki-chan!" {
		t.Fatalf("nested GBNL texts corrupted: %q / %q", a.StrRaw, b.StrRaw)
	}
	second, e := changedCL.FileData(1)
	if e != nil {
		t.Fatal(e)
	}
	if string(second) != "BBBBB" {
		t.Fatal("untouched CL3 member mutated")
	}
	wrong := append([]GBNLTextEdit(nil), edits...)
	wrong[0].Old = []byte("wrong old")
	if _, e = RebuildGBNLTree(orig, wrong, WrapBILZGo); e == nil {
		t.Fatal("source text drift accepted")
	}
}
