package mskit

import (
	"bytes"
	"os"
	"testing"
)

func TestLZO1XMatchingRealWesternENC(t *testing.T) {
	original, e := os.ReadFile(privateFixturePath(t, "switch-enc-ms2-original"))
	if e != nil {
		t.Fatal(e)
	}
	enc, e := ParseENCEntry(original)
	if e != nil {
		t.Fatal(e)
	}
	for i, row := range enc.Rows {
		if !row.LZOCompressed() {
			continue
		}
		decoded, e := row.DecodedPayload()
		if e != nil {
			t.Fatal(e)
		}
		lit, e := CompressLZO1XLiterals(decoded)
		if e != nil {
			t.Fatal(e)
		}
		packed, e := CompressLZO1XMatches(decoded)
		if e != nil {
			t.Fatal(e)
		}
		if len(packed) > len(lit) {
			t.Fatalf("row %d compressor increased bytes: %d > %d", i, len(packed), len(lit))
		}
		check, e := DecompressLZO1X(packed, len(decoded))
		if e != nil || !bytes.Equal(check, decoded) {
			t.Fatal(e)
		}
		t.Logf("Western MS2 g1 row %d: decoded=%d, source LZO=%d, literals=%d, matching=%d", i, len(decoded), len(row.Payload), len(lit), len(packed))
	}
}
