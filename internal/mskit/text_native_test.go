package mskit

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func sharedGBNL(t *testing.T) []byte {
	t.Helper()
	raw, e := hex.DecodeString("4753544c01000000100000000400000001000000400000000200000004000000010000004800000000000000500000000800000000000000000000000000000000000000000000000500000000000000536e6f772057686974652e2e2e00")
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestGoGBNLSharedStringsIndependentEdits(t *testing.T) {
	raw := sharedGBNL(t)
	change := map[TextFieldID][]byte{{Message: 0, Field: 0}: []byte("Snow")}
	got, e := RebuildGBNL(raw, change)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseGBNL(got)
	if e != nil {
		t.Fatal(e)
	}
	one, _ := parsed.Field(0, 0)
	two, _ := parsed.Field(1, 0)
	if string(one.StrRaw) != "Snow" || string(two.StrRaw) != "Snow White..." {
		t.Fatalf("shared text corrupted: %q / %q", one.StrRaw, two.StrRaw)
	}
	both, e := RebuildGBNL(raw, map[TextFieldID][]byte{{0, 0}: []byte("Snow"), {1, 0}: []byte("Snow Yuki")})
	if e != nil {
		t.Fatal(e)
	}
	g, e := ParseGBNL(both)
	if e != nil {
		t.Fatal(e)
	}
	f0, _ := g.Field(0, 0)
	f1, _ := g.Field(1, 0)
	if string(f0.StrRaw) != "Snow" || string(f1.StrRaw) != "Snow Yuki" {
		t.Fatalf("different translations collided %q / %q", f0.StrRaw, f1.StrRaw)
	}
	if bytes.Equal(raw, both) {
		t.Fatal("no change")
	}
	if _, e = RebuildGBNL(raw, map[TextFieldID][]byte{{0, 0}: []byte("invalid\x00value")}); e == nil {
		t.Fatal("NUL accepted")
	}
}
func TestGoBINNativeEntryRoundtrip(t *testing.T) {
	p := make([]byte, 0x40)
	binary.LittleEndian.PutUint64(p[0x10:0x18], 0x30)
	binary.LittleEndian.PutUint64(p[0x20:0x28], 0x36)
	copy(p[0x30:], []byte("Alpha\x00Beta\x00"))
	w, e := WrapBILZGo(p, 7, 7)
	if e != nil {
		t.Fatal(e)
	}
	entry := make([]byte, 0x80)
	copy(entry, []byte{0, 'n', 'i', 'b'})
	entry = append(entry, w...)
	after, e := RebuildBINEntry(entry, map[int][]byte{0: []byte("Alpha Extended")}, WrapBILZGo)
	if e != nil {
		t.Fatal(e)
	}
	check, e := ParseBINEntry(after)
	if e != nil {
		t.Fatal(e)
	}
	got, e := check.Strings()
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 2 || string(got[0]) != "Alpha Extended" || string(got[1]) != "Beta" {
		t.Fatalf("BIN strings unexpected %q", got)
	}
	if _, e = RebuildBINEntry(entry, map[int][]byte{2: []byte("bad")}, WrapBILZGo); e == nil {
		t.Fatal("out of range accepted")
	}
}
