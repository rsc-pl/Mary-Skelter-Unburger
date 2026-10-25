package main

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/woozymasta/bcn"
	"maryskelter-unburger/internal/mskit"
)

func TestBC3JointFallbackCandidateIsDecodedAndNonRegressing(t *testing.T) {
	rng := rand.New(rand.NewSource(136))
	var target [64]byte
	for i := 0; i < 16; i++ {
		j := 4 * i
		target[j] = byte(20 + rng.Intn(230))
		target[j+1] = byte(10 + rng.Intn(220))
		target[j+2] = byte(8 + rng.Intn(240))
		target[j+3] = byte(rng.Intn(256))
	}
	block, err := bcn.EncodeBC3WithOptions(target[:], 4, 4, &bcn.EncodeOptions{QualityLevel: 6, Workers: 1})
	if err != nil {
		t.Fatal(err)
	}
	original := bytes.Clone(block)
	basePix, e := mskit.DecodeDXT5RGBA(block, 4, 4)
	if e != nil {
		t.Fatal(e)
	}
	var origRGBA [64]byte
	copy(origRGBA[:], basePix)
	initialAlpha, initialVisible := visibleDiff(target, origRGBA)
	got, a, v, e := MinimaxJointBC3(target, block)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(original, block) {
		t.Fatal("joint search modified caller-owned compressed bytes")
	}
	if len(got) != 16 {
		t.Fatal("invalid BC3 block size")
	}
	pix, e := mskit.DecodeDXT5RGBA(got, 4, 4)
	if e != nil {
		t.Fatal(e)
	}
	var independent [64]byte
	copy(independent[:], pix)
	aa, vv := visibleDiff(target, independent)
	if a != aa || v != vv {
		t.Fatalf("reported %d/%d != independent %d/%d", a, v, aa, vv)
	}
	worst := func(x, y int) int {
		if x > y {
			return x
		}
		return y
	}
	if worst(a, v) > worst(initialAlpha, initialVisible) {
		t.Fatalf("joint search regressed minimax %d/%d -> %d/%d", initialAlpha, initialVisible, a, v)
	}
	again, aaa, vvv, e := MinimaxJointBC3(target, block)
	if e != nil || !bytes.Equal(again, got) || aaa != a || vvv != v {
		t.Fatal("non-deterministic fallback")
	}
	t.Logf("joint BC3 minimax initial %d/%d final %d/%d", initialAlpha, initialVisible, a, v)
}

func TestBC3JointFallbackRejectsBadInputAndKeepsStrictGuard(t *testing.T) {
	if _, _, _, e := MinimaxJointBC3([64]byte{}, []byte{0}); e == nil {
		t.Fatal("accepted invalid block")
	}
	source, target := tinyDXT5(t)
	// A 4x4 pattern is not exactly representable as BC3. Zero-tolerance must
	// remain fail-closed even after all fallback stages are attempted.
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := (y*8 + x) * 4
			n := byte(y*4 + x)
			target[i] = n * 17
			target[i+1] = n*11 + 9
			target[i+2] = n*13 + 17
			target[i+3] = n * 15
		}
	}
	_, rep, err := EncodeChangedTID(source, target, 6, 0, 0)
	if err == nil {
		t.Fatal("joint fallback bypassed exact-compression quality guard")
	}
	if rep.BC3JointAttempts == 0 {
		t.Fatal("joint fallback was not exercised by the strict rejection test")
	}
}

func TestBC3JointFallbackFullTIDQualityAndPreservation(t *testing.T) {
	source, target := tinyDXT5(t)
	rng := rand.New(rand.NewSource(136))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			j := (y*8 + x) * 4
			target[j] = byte(20 + rng.Intn(230))
			target[j+1] = byte(10 + rng.Intn(220))
			target[j+2] = byte(8 + rng.Intn(240))
			target[j+3] = byte(rng.Intn(256))
		}
	}
	encoded, rep, e := EncodeChangedTID(source, target, 6, 64, 64)
	if e != nil {
		t.Fatalf("strict encoded TID rejected: %v; partial %+v", e, rep)
	}
	if rep.ChangedBlocks != 1 || rep.UnchangedBlocks != 3 {
		t.Fatalf("unexpected changed blocks: %+v", rep)
	}
	if !bytes.Equal(source[:128], encoded[:128]) || !bytes.Equal(source[128+16:], encoded[128+16:]) {
		t.Fatal("original TID header or untouched blocks drifted")
	}
	rgba, meta, e := mskit.DecodeTIDDXT5RGBA(encoded)
	if e != nil {
		t.Fatal(e)
	}
	if meta.Format != "DXT5" {
		t.Fatal("wrong codec")
	}
	var original [64]byte
	var decoded [64]byte
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			p := 4 * (y*4 + x)
			q := 4 * (y*8 + x)
			copy(original[p:p+4], target[q:q+4])
			copy(decoded[p:p+4], rgba[q:q+4])
		}
	}
	a, v := visibleDiff(original, decoded)
	if a > 64 || v > 64 {
		t.Fatalf("failed strict guard after independent decode: %d/%d", a, v)
	}
	t.Logf("strict full DXT5 changed block alpha=%d visible=%d joint attempts=%d joint selected=%d", a, v, rep.BC3JointAttempts, rep.BC3JointSelected)
}

// Fixed synthetic case that cannot pass the standard/fixed-alpha alternatives
// but does pass strict 64/64 after the joint-alpha fallback. Not a substitute
// for the private Camp gold-source atlas test.
func TestBC3JointFallbackSelectedInStrictFullTID(t *testing.T) {
	source, target := tinyDXT5(t)
	rng := rand.New(rand.NewSource(41))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			off := (y*8 + x) * 4
			for c := 0; c < 4; c++ {
				target[off+c] = byte(rng.Intn(256))
			}
		}
	}
	result, report, err := EncodeChangedTID(source, target, 6, 64, 64)
	if err != nil {
		t.Fatalf("joint quality acceptance: %v", err)
	}
	if report.BC3JointAttempts != 1 || report.BC3JointSelected != 1 || report.MaxAlphaError > 64 || report.MaxVisibleError > 64 {
		t.Fatalf("expected single fallback under original unchanged guards: %+v", report)
	}
	if report.ChangedBlocks != 1 || !bytes.Equal(result[:128], source[:128]) || !bytes.Equal(result[144:], source[144:]) {
		t.Fatal("fallback changed source header, unrelated blocks, or unexpected count")
	}
	px, meta, e := mskit.DecodeTIDDXT5RGBA(result)
	if e != nil || meta.Format != "DXT5" {
		t.Fatalf("independent full TID decode: %v", e)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			at := (y*8 + x) * 4
			aDiff := absI(int(target[at+3]) - int(px[at+3]))
			if aDiff > 64 {
				t.Fatalf("alpha regression at %d,%d", x, y)
			}
		}
	}
}
