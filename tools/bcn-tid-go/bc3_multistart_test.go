package main

import (
	"bytes"
	"maryskelter-unburger/internal/mskit"
	"strings"
	"testing"
)

func TestWideMinimaxDeterministicAndAlphaFrozen(t *testing.T) {
	source, target := tinyDXT5(t)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := (y*8 + x) * 4
			switch (x + y) % 3 {
			case 0:
				copy(target[i:i+4], []byte{255, 205, 37, 255})
			case 1:
				copy(target[i:i+4], []byte{212, 0, 84, 255})
			case 2:
				copy(target[i:i+4], []byte{10, 0, 13, 255})
			}
		}
	}
	block := tile4(target, 8, 8, 0, 0)
	original := append([]byte(nil), source[128:144]...)
	a, alpha1, vis1, e := MinimaxRGBBC3ExpandedSearch(block, original)
	if e != nil {
		t.Fatal(e)
	}
	b, alpha2, vis2, e := MinimaxRGBBC3ExpandedSearch(block, original)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(a, b) || alpha1 != alpha2 || vis1 != vis2 {
		t.Fatal("legacy nondeterministic")
	}
	if !bytes.Equal(a[:8], original[:8]) {
		t.Fatal("legacy mutated compressed alpha")
	}
	decoded, e := mskit.DecodeDXT5RGBA(a, 4, 4)
	if e != nil {
		t.Fatal(e)
	}
	var pix [64]byte
	copy(pix[:], decoded)
	aa, vv := visibleDiff(block, pix)
	if aa != alpha1 || vv != vis1 {
		t.Fatalf("independent mismatch %d/%d != %d/%d", aa, vv, alpha1, vis1)
	}
	result, _, e := EncodeChangedTID(source, target, 6, 64, 64)
	if e == nil || result != nil || !strings.Contains(e.Error(), "quality guard rejected") {
		t.Fatalf("strict guard bypassed: %v", e)
	}
}
