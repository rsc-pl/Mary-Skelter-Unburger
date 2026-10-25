package mskit

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func makeTestZlib(t *testing.T, p []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z, e := zlib.NewWriterLevel(&b, 9)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = z.Write(p); e != nil {
		t.Fatal(e)
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func TestDenseDeflateExactPadding(t *testing.T) {
	raw := bytes.Repeat([]byte("The same text repeated:abcdefghijklmnopqrstuvwxyz0123456789"), 200)
	dense := makeTestZlib(t, raw)
	for n := 0; n < 105; n++ {
		want := len(dense) + n
		p, e := PadDenseZlibWithoutChangingText(dense, want)
		ok := n == 0 || n%5 == 0 || n >= 16+(n%5-1)
		if n > 0 && n%5 != 0 {
			ok = n >= 15+n%5
		}
		if !ok {
			if e == nil {
				t.Fatalf("expected bounded failure at n=%d", n)
			}
			continue
		}
		if e != nil {
			t.Fatalf("n=%d: %v", n, e)
		}
		if len(p) != want {
			t.Fatalf("n=%d wrong size", n)
		}
		plain, e := strictUnzlib(p)
		if e != nil || !bytes.Equal(plain, raw) {
			t.Fatalf("n=%d incorrect decoded data: %v", n, e)
		}
	}
	if _, e := PadDenseZlibWithoutChangingText(dense, len(dense)-1); e == nil {
		t.Fatal("compression overflow accepted")
	}
	bad := append(append([]byte{}, dense...), 0, 0)
	if _, e := PadDenseZlibWithoutChangingText(bad, len(bad)+5); e == nil {
		t.Fatal("trailing garbage accepted")
	}
}
func TestDenseBINFixedSizePreservesSuffix(t *testing.T) {
	raw := bytes.Repeat([]byte("BIN payload test row | repeat "), 220)
	c := makeTestZlib(t, raw)
	entry := make([]byte, 0x90+len(c)+123)
	copy(entry[:4], []byte{0, 'n', 'i', 'b'})
	copy(entry[0x80:0x84], "BILZ")
	binary.BigEndian.PutUint32(entry[0x84:0x88], uint32(len(raw)))
	binary.BigEndian.PutUint32(entry[0x88:0x8c], uint32(len(c)))
	binary.BigEndian.PutUint32(entry[0x8c:0x90], 7)
	copy(entry[0x90:], c)
	for i := 0x90 + len(c); i < len(entry); i++ {
		entry[i] = byte(i%249 + 1)
	}
	dense := c
	got, e := RebuildBINEntryFromDenseZlibFixedWidth(entry, raw, dense)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != len(entry) || !bytes.Equal(got[0x90+len(c):], entry[0x90+len(c):]) {
		t.Fatal("suffix changed")
	}
	if _, e = RebuildBINEntryFromDenseZlibFixedWidth(entry, append([]byte{}, raw...), []byte{1, 2, 3}); e == nil {
		t.Fatal("bad encoder accepted")
	}
}
func TestWesternMS2073CompressedGolden(t *testing.T) {
	base := privateFixturePath(t, "switch-bin-fixed-samples")
	old, e := os.ReadFile(filepath.Join(base, "original-entry0.bin"))
	if os.IsNotExist(e) {
		t.Skip("private source sample not in public package")
	}
	if e != nil {
		t.Fatal(e)
	}
	plain, e := os.ReadFile(filepath.Join(base, "rebuilt-bin.bin"))
	if e != nil {
		t.Fatal(e)
	}
	dense, e := os.ReadFile(filepath.Join(base, "dense-zlib.bin"))
	if e != nil {
		t.Fatal(e)
	}
	got, e := RebuildBINEntryFromDenseZlibFixedWidth(old, plain, dense)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != 26457 || !bytes.Equal(got[0x90+21435:], old[0x90+21435:]) {
		t.Fatal("real source size/trailing bytes changed")
	}
	if !bytes.Equal(got[:0x80], old[:0x80]) {
		t.Fatal("BIN outer tag changed")
	}
	t.Log("Western MS2 BIN073 exact-length rebuild, unchanged 4878-byte opaque suffix PASS")
}
