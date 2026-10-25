package mskit

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestGBNLOneStringFixture(t *testing.T) {
	raw, err := hex.DecodeString("4753544c01000000100000000400000001000000400000000200000004000000010000004800000000000000500000000800000000000000000000000000000000000000000000000500000000000000536e6f772057686974652e2e2e00")
	if err != nil {
		t.Fatal(err)
	}
	g, err := ParseGBNL(raw)
	if err != nil {
		t.Fatal(err)
	}
	if g.CountMsgs != 2 || g.Field30 != 8 || len(g.Items) != 1 || g.Items[0].Kind != "str" {
		t.Fatalf("unexpected GBNL shape: %+v items=%+v", g, g.Items)
	}
	for i := 0; i < 2; i++ {
		f, err := g.Field(i, 0)
		if err != nil {
			t.Fatal(err)
		}
		if string(f.StrRaw) != "Snow White..." || f.StrOffset == nil || *f.StrOffset != 0 {
			t.Fatalf("field %d = %+v", i, f)
		}
	}
}

func synthSTCMString() []byte {
	// One DATA export at 0x30, export record at 0x50. The data payload is a
	// NUL-terminated 8-byte string slot (offset_unit=2, field_8=1).
	b := make([]byte, 0x78)
	copy(b[:], []byte("STCM2L TEST"))
	binary.LittleEndian.PutUint32(b[0x20:0x24], 0x50)
	binary.LittleEndian.PutUint32(b[0x24:0x28], 1)
	binary.LittleEndian.PutUint32(b[0x50:0x54], 1)
	copy(b[0x54:0x74], []byte("msg"))
	binary.LittleEndian.PutUint32(b[0x74:0x78], 0x30)
	binary.LittleEndian.PutUint32(b[0x30:0x34], 0)
	binary.LittleEndian.PutUint32(b[0x34:0x38], 2)
	binary.LittleEndian.PutUint32(b[0x38:0x3c], 1)
	binary.LittleEndian.PutUint32(b[0x3c:0x40], 8)
	copy(b[0x40:0x48], []byte{'H', 'e', 'l', 'l', 'o', 0, 0, 0})
	return b
}

func TestSTCMSemanticStringTraversal(t *testing.T) {
	s, err := ParseSTCM(synthSTCMString())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.DataOrder) != 1 || s.DataOrder[0] != 0x30 {
		t.Fatalf("data order=%v", s.DataOrder)
	}
	strings := s.StringItems()
	if len(strings) != 1 || string(strings[0].StringBytes()) != "Hello" {
		t.Fatalf("strings=%+v", strings)
	}
}

func TestENCConservativeScanner(t *testing.T) {
	payload := append([]byte{1, 2}, []byte("Hello world")...)
	payload = append(payload, 0)
	payload = append(payload, []byte{3, 4}...)
	payload = append(payload, []byte("AB")...)
	payload = append(payload, 0)
	got := ScanENCStrings(payload, 2, 3)
	if len(got) != 1 || got[0].Offset != 2 || string(got[0].Raw) != "Hello world" || !got[0].Terminated {
		t.Fatalf("scan=%+v", got)
	}
}

func TestBINStringTable(t *testing.T) {
	payload := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(payload[:4], 0x1234)
	binary.LittleEndian.PutUint64(payload[0x10:0x18], 0x30)
	binary.LittleEndian.PutUint64(payload[0x20:0x28], 0x36)
	copy(payload[0x30:], []byte("Alpha\x00Beta\x00"))
	b, err := ParseBINPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	ss, err := b.Strings()
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 2 || string(ss[0]) != "Alpha" || string(ss[1]) != "Beta" {
		t.Fatalf("strings=%q", ss)
	}
}

func TestENCConservativeScannerMatchesPythonIsAlnumForUnicodeNumbers(t *testing.T) {
	payload := []byte("謎の記憶①－①\x00誰かの夢④-②\x00plain text\x00")
	got := ScanENCStrings(payload, 2, 3)
	if len(got) != 3 {
		t.Fatalf("ScanENCStrings unicode-number parity: got %d strings, want 3", len(got))
	}
	if string(got[0].Raw) != "謎の記憶①－①" || string(got[1].Raw) != "誰かの夢④-②" {
		t.Fatalf("unexpected unicode-number scanner output: %q / %q", got[0].Raw, got[1].Raw)
	}
}
