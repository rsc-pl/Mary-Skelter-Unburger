package main

import (
	"bytes"
	"github.com/woozymasta/bcn"
	"testing"
)

func TestBC7StrictFallbackDeterministicAndFailClosed(t *testing.T) {
	var src [64]byte
	for i := 0; i < 64; i += 4 {
		n := i / 4
		src[i] = byte(n * 17)
		src[i+1] = byte((n*71 + 13) % 256)
		src[i+2] = byte((n*43 + 211) % 256)
		src[i+3] = byte(n * 13)
	}
	encoded, err := bcn.EncodeBC7WithOptions(src[:], 4, 4, &bcn.EncodeOptions{QualityLevel: 6, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	a, attempts, err := MinimaxBC7(src, encoded, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.a == 0 && a.v == 0 {
		t.Fatal("contrasting gradient unexpectedly exactly encodable")
	}
	if attempts < 1 {
		t.Fatal("did not explore bounded fallback")
	}
	b, second, err := MinimaxBC7(src, encoded, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if a.a != b.a || a.v != b.v || attempts != second || !bytes.Equal(a.encoded, b.encoded) {
		t.Fatal("non-deterministic BC7 fallback")
	}
	t.Logf("strict 0/0 refuses impossible nonzero reconstruction: alpha=%d visible=%d trials=%d", a.a, a.v, attempts)
}
