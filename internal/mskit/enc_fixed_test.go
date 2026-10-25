package mskit

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

func TestFixedENCRealWesternG1(t *testing.T) {
	original, e := os.ReadFile(privateFixturePath(t, "switch-enc-ms2-original"))
	if e != nil {
		t.Fatal(e)
	}
	file, e := ParseENCEntry(original)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := file.Rows[0].DecodedPayload()
	if e != nil {
		t.Fatal(e)
	}
	off := 2055996
	edited := []ENCEdit{{Row: 0, Offset: off, Old: append([]byte(nil), raw[off:off+67]...), New: bytes.Repeat([]byte("N"), 139)}}
	out, e := RebuildENCEntryFixedWidthGo(original, edited)
	if e != nil {
		t.Fatal(e)
	}
	if len(out) != 331744 || !bytes.Equal(out[OrochiBILZDataOffset+int(binary.BigEndian.Uint32(original[0x88:0x8c])):], original[OrochiBILZDataOffset+int(binary.BigEndian.Uint32(original[0x88:0x8c])):]) {
		t.Fatal("GDAT entry footprint/suffix modified")
	}
	final, e := ParseENCEntry(out)
	if e != nil {
		t.Fatal(e)
	}
	got, e := final.Rows[0].DecodedPayload()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got[off:off+139], edited[0].New) {
		t.Fatal("translated text did not survive")
	}
	for i := 1; i < len(file.Rows); i++ {
		if !bytes.Equal(file.Rows[i].Payload, final.Rows[i].Payload) {
			t.Fatalf("unchanged compressed row %d drifted", i)
		}
	}
	t.Logf("real Western MS2 a073/g1: original and native Go revised length=%d, compressed zlib padded in-place; no unknown bytes moved", len(out))
}
func TestFixedENCRejectUnfittable(t *testing.T) {
	src, e := os.ReadFile(privateFixturePath(t, "switch-enc-ms2-original"))
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseENCEntry(src)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := parsed.Rows[0].DecodedPayload()
	if e != nil {
		t.Fatal(e)
	}
	_, err := RebuildENCEntryFixedWidthGo(src, []ENCEdit{{Row: 0, Offset: 2055996, Old: raw[2055996:2056063], New: bytes.Repeat([]byte("R"), 1000000)}})
	if err == nil || (!strings.Contains(err.Error(), "interior relocation") && !strings.Contains(err.Error(), "cannot fit")) {
		t.Fatalf("unsafe growth accepted / wrong reason: %v", err)
	}
}
