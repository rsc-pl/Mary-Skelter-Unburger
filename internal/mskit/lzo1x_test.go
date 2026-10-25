package mskit

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestLZO1XSyntheticVectors(t *testing.T) {
	cases := []struct {
		raw    []byte
		hexstr string
	}{
		{bytes.Repeat([]byte("MarySkelter"), 20), "084d617279536b656c74657220a328000a65724d617279536b656c746572110000"},
		{bytes.Repeat(func() []byte {
			b := make([]byte, 64)
			for i := range b {
				b[i] = byte(i)
			}
			return b
		}(), 8), "002e000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f20008cfc0000022c2d2e2f303132333435363738393a3b3c3d3e3f110000"},
		{bytes.Repeat(append([]byte("AAAAABBBBBCCCCCDDDDDEEEEE"), 0), 30), "03414141414142610043610044610045610000200000c564000b4343434444444444454545454500110000"},
	}
	for i, tc := range cases {
		src, err := hex.DecodeString(tc.hexstr)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecompressLZO1X(src, len(tc.raw))
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !bytes.Equal(got, tc.raw) {
			t.Fatalf("case %d mismatch", i)
		}
	}
}
