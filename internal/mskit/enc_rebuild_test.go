package mskit

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func canonicalTestENC(data []byte, algo, level int) ([]byte, error) {
	return WrapBILZGo(data, algo, level)
}

func testENCEntry(t *testing.T, compressed bool, logical []byte) []byte {
	t.Helper()
	encoded := logical
	flag := uint32(0)
	aux := uint32(12345)
	if compressed {
		var err error
		encoded, err = CompressLZO1XLiterals(logical)
		if err != nil {
			t.Fatal(err)
		}
		flag = 0x80000000
		aux = uint32(len(logical))
	}
	payload := make([]byte, 0x40+len(encoded))
	binary.LittleEndian.PutUint32(payload[:4], 1)
	binary.LittleEndian.PutUint32(payload[4:8], flag|uint32(len(encoded)))
	binary.LittleEndian.PutUint32(payload[8:12], aux)
	binary.LittleEndian.PutUint32(payload[12:16], 0x40)
	copy(payload[0x40:], encoded)
	wrapped, err := canonicalTestENC(payload, 7, 7)
	if err != nil {
		t.Fatal(err)
	}
	entry := make([]byte, 0x80)
	copy(entry[:4], []byte{0, 'c', 'n', 'e'})
	return append(entry, wrapped...)
}

func TestLZOLiteralOnlyLengths(t *testing.T) {
	for _, n := range []int{4, 18, 19, 238, 239, 255, 272, 273, 274, 528, 1024, 65535} {
		src := bytes.Repeat([]byte("xy1"), (n+2)/3)[:n]
		raw, err := CompressLZO1XLiterals(src)
		if err != nil {
			t.Fatalf("size %d: %v", n, err)
		}
		out, err := DecompressLZO1X(raw, n)
		if err != nil || !bytes.Equal(src, out) {
			t.Fatalf("size %d: %v", n, err)
		}
	}
}

func TestENCNativeTerminalGrowthBeyondSlot(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		raw := append([]byte{1, 2, 3, 4, 0, 0, 0, 0}, bytes.Repeat([]byte("A"), 67)...)
		raw = append(raw, bytes.Repeat([]byte{0}, 20)...)
		entry := testENCEntry(t, compressed, raw)
		newText := bytes.Repeat([]byte("B"), 139)
		rebuilt, err := RebuildENCEntry(entry, []ENCEdit{{Row: 0, Offset: 8, Old: bytes.Repeat([]byte("A"), 67), New: newText, Capacity: 67, RecordKey: "MS2/a073/g00001/row0"}}, canonicalTestENC)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(rebuilt, entry) {
			t.Fatal("expected a changed entry")
		}
		parsed, err := ParseENCEntry(rebuilt)
		if err != nil {
			t.Fatal(err)
		}
		got, err := parsed.Rows[0].DecodedPayload()
		if err != nil {
			t.Fatal(err)
		}
		want := append([]byte{1, 2, 3, 4, 0, 0, 0, 0}, newText...)
		want = append(want, 0)
		want = append(want, bytes.Repeat([]byte{0}, 19)...)
		if !bytes.Equal(got, want) {
			t.Fatalf("decoded growth mismatch compressed=%v", compressed)
		}
		if parsed.Rows[0].LZOCompressed() != compressed {
			t.Fatal("lost row compression flag")
		}
		if !compressed && parsed.Rows[0].Aux != 12345 {
			t.Fatal("uncompressed aux changed")
		}
		if !bytes.Equal(entry[:0x80], rebuilt[:0x80]) {
			t.Fatal("wrapper header changed")
		}
		copyThrough, err := RebuildENCEntry(entry, nil, nil)
		if err != nil || !bytes.Equal(copyThrough, entry) {
			t.Fatal("no-op changed entry")
		}
	}
}

func TestENCNativeGrowthGuards(t *testing.T) {
	old := bytes.Repeat([]byte("A"), 67)
	base := append([]byte{1, 2, 3, 4, 0, 0, 0, 0}, old...)
	base = append(base, 0, 0, 'Z', 0, 0, 0)
	entry := testENCEntry(t, true, base)
	e := ENCEdit{Row: 0, Offset: 8, Old: old, New: bytes.Repeat([]byte("B"), 139), Capacity: 67, RecordKey: "interior"}
	if _, err := RebuildENCEntry(entry, []ENCEdit{e}, canonicalTestENC); err == nil || !strings.Contains(err.Error(), "interior relocation unproven") {
		t.Fatalf("interior unsafe edit accepted: %v", err)
	}
	e.New = []byte("New")
	e.Old = []byte("NOT_THE_SOURCE")
	if _, err := RebuildENCEntry(entry, []ENCEdit{e}, canonicalTestENC); err == nil {
		t.Fatal("wrong original byte accepted")
	}
	e.Old = old
	e.New = []byte("New")
	if _, err := RebuildENCEntry(entry, []ENCEdit{e}, nil); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("missing canonical writer accepted: %v", err)
	}
}
