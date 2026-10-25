package main

import (
	"bytes"
	"testing"

	"github.com/woozymasta/bcn"
	"maryskelter-unburger/internal/mskit"
)

// A source-derived 4x4 tile with opaque dark/magenta/gold gradients that
// defeats baseline BC3. These 64 bytes are fixture data, not a game file.
var HardGold = [64]byte{
	208, 0, 83, 255, 128, 0, 53, 255, 17, 3, 11, 255, 17, 0, 14, 255,
	163, 0, 66, 255, 26, 2, 15, 255, 20, 3, 13, 255, 23, 5, 14, 255,
	45, 1, 22, 255, 20, 4, 12, 255, 11, 0, 13, 255, 158, 116, 27, 255,
	15, 4, 10, 255, 10, 0, 13, 255, 114, 79, 23, 255, 255, 204, 37, 255,
}

func TestMinimaxBC3OriginalDifficultBlockDeterministicAlphaPreserving(t *testing.T) {
	src, e := bcn.EncodeBC3WithOptions(HardGold[:], 4, 4, &bcn.EncodeOptions{QualityLevel: 6, Workers: 1})
	if e != nil {
		t.Fatal(e)
	}
	dec, e := mskit.DecodeDXT5RGBA(src, 4, 4)
	if e != nil {
		t.Fatal(e)
	}
	var before [64]byte
	copy(before[:], dec)
	alpha0, vis0 := visibleDiff(HardGold, before)
	if vis0 < 100 {
		t.Fatalf("wrong baseline: visible=%d", vis0)
	}
	var first []byte
	for i := 0; i < 8; i++ {
		out, a, v, err := MinimaxRGBBC3AlphaPreserving(HardGold, src)
		if err != nil {
			t.Fatal(err)
		}
		if a != alpha0 || v >= vis0 || v > 72 {
			t.Fatalf("minimax did not improve opaque hard tile: alpha=%d visible=%d baseline=%d", a, v, vis0)
		}
		if !bytes.Equal(out[:8], src[:8]) {
			t.Fatal("minimax altered compressed alpha")
		}
		got, err := mskit.DecodeDXT5RGBA(out, 4, 4)
		if err != nil {
			t.Fatal(err)
		}
		var px [64]byte
		copy(px[:], got)
		aa, vv := visibleDiff(HardGold, px)
		if aa != a || vv != v {
			t.Fatal("independent decoder disagrees with optimizer report")
		}
		if i > 0 && !bytes.Equal(out, first) {
			t.Fatal("palette selection nondeterministic")
		}
		if i == 0 {
			first = append([]byte(nil), out...)
		}
	}
}

func TestMinimaxRejectsBadLength(t *testing.T) {
	_, _, _, e := MinimaxRGBBC3AlphaPreserving(HardGold, []byte{1, 2, 3})
	if e == nil {
		t.Fatal("accepted truncated BC3 block")
	}
}

func TestHardBC3DoesNotEscapeQualityGuard(t *testing.T) {
	source, target := tinyDXT5(t)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			off := (y*8 + x) * 4
			copy(target[off:off+4], HardGold[(y*4+x)*4:(y*4+x+1)*4])
		}
	}
	result, report, err := EncodeChangedTID(source, target, 6, 64, 64)
	if err == nil || result != nil {
		t.Fatalf("quality guard bypassed: err=%v report=%+v", err, report)
	}
	if report.BC3PaletteAttempts < 1 || report.BC3PaletteSelected < 1 {
		t.Fatalf("minimax fallback was not integrated: %+v", report)
	}
}
