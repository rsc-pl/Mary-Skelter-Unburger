package mskit

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"testing"
)

func synthENCEntry(t *testing.T, compressedRow []byte, decodedLen int) []byte {
	t.Helper()
	payload := make([]byte, 4+12+len(compressedRow))
	binary.LittleEndian.PutUint32(payload[:4], 1)
	binary.LittleEndian.PutUint32(payload[4:8], 0x80000000|uint32(len(compressedRow)))
	binary.LittleEndian.PutUint32(payload[8:12], uint32(decodedLen))
	binary.LittleEndian.PutUint32(payload[12:16], 16)
	copy(payload[16:], compressedRow)
	var z bytes.Buffer
	zw, err := zlib.NewWriterLevel(&z, 9)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	body := z.Bytes()
	entry := make([]byte, 0x90+len(body))
	copy(entry[:4], []byte{0, 'c', 'n', 'e'})
	copy(entry[0x80:0x84], []byte("BILZ"))
	binary.BigEndian.PutUint32(entry[0x84:0x88], uint32(len(payload)))
	binary.BigEndian.PutUint32(entry[0x88:0x8c], uint32(len(body)))
	binary.BigEndian.PutUint32(entry[0x8c:0x90], 7)
	copy(entry[0x90:], body)
	return entry
}

func TestParseENCEntryAndDecodeLZO(t *testing.T) {
	compressed := []byte{0x08, 0x4d, 0x61, 0x72, 0x79, 0x53, 0x6b, 0x65, 0x6c, 0x74, 0x65, 0x72, 0x20, 0xa3, 0x28, 0x00, 0x0a, 0x65, 0x72, 0x4d, 0x61, 0x72, 0x79, 0x53, 0x6b, 0x65, 0x6c, 0x74, 0x65, 0x72, 0x11, 0x00, 0x00}
	raw := bytes.Repeat([]byte("MarySkelter"), 20)
	entry := synthENCEntry(t, compressed, len(raw))
	f, err := ParseENCEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	if f.Count != 1 || !f.Rows[0].LZOCompressed() {
		t.Fatalf("parsed %+v", f)
	}
	got, err := f.Rows[0].DecodedPayload()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("decoded row mismatch")
	}
}
