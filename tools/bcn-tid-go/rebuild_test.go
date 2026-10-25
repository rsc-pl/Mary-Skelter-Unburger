package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woozymasta/bcn"
	"maryskelter-unburger/internal/mskit"
)

func testTinyTID(t *testing.T) ([]byte, []byte) {
	t.Helper()
	original := make([]byte, 8*8*4)
	for i := 0; i < 64; i++ {
		original[i*4] = 80
		original[i*4+1] = 110
		original[i*4+2] = 210
		original[i*4+3] = 255
	}
	blocks, e := bcn.EncodeBC7WithOptions(original, 8, 8, &bcn.EncodeOptions{QualityLevel: 6, Workers: 1})
	if e != nil {
		t.Fatal(e)
	}
	header := make([]byte, 128)
	copy(header[:4], []byte{'T', 'I', 'D', 0x90})
	header[0x44] = 8
	header[0x48] = 8
	copy(header[0x64:], "BC7")
	tid := append(header, blocks...)
	before, meta, e := mskit.DecodeTIDBC7RGBA(tid)
	if e != nil || meta.Width != 8 {
		t.Fatalf("TID: %v", e)
	}
	return tid, before
}
func TestBC7ChangedBlocksNoClobber(t *testing.T) {
	tid, target := testTinyTID(t)
	output, rep, e := EncodeChangedBC7(tid, target, 6, 64, 64)
	if e != nil {
		t.Fatal(e)
	}
	if rep.ChangedBlocks != 0 || !bytes.Equal(output, tid) {
		t.Fatalf("no-op altered original: %+v", rep)
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			p := (y*8 + x) * 4
			target[p], target[p+1], target[p+2] = 180, 40, 95
		}
	}
	output, rep, e = EncodeChangedBC7(tid, target, 6, 64, 64)
	if e != nil {
		t.Fatal(e)
	}
	if rep.ChangedBlocks != 1 || rep.UnchangedBlocks != 3 {
		t.Fatalf("bad changed block count %+v", rep)
	}
	if !bytes.Equal(tid[:128], output[:128]) || !bytes.Equal(tid[128+16:], output[128+16:]) {
		t.Fatal("header or untouched compressed blocks changed")
	}
	if rep.OutputSHA256 != digest(output) {
		t.Fatal("wrong output digest")
	}
	if _, _, e = mskit.DecodeTIDBC7RGBA(output); e != nil {
		t.Fatal(e)
	}
}
func TestBC7GuardsAndCLI(t *testing.T) {
	tid, target := testTinyTID(t)
	// legacy: the bounded BC7 fallback can now encode the old one-pixel
	// example exactly. Use a truly nonrepresentable 4x4 RGBA pattern to
	// exercise the strict 0/0 fail-closed guard instead.
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			n := y*4 + x
			at := (y*8 + x) * 4
			target[at], target[at+1], target[at+2], target[at+3] = byte(n*17), byte((n*71+13)%256), byte((n*43+211)%256), byte(n*13)
		}
	}
	if _, _, e := EncodeChangedBC7(tid, target, 6, 0, 0); e == nil || !strings.Contains(e.Error(), "quality guard") {
		t.Fatalf("no reject: %v", e)
	}
	if _, _, e := EncodeChangedBC7(tid, target, 11, 64, 64); e == nil {
		t.Fatal("invalid quality accepted")
	}
	bad := append([]byte(nil), tid...)
	copy(bad[0x64:], "DXT")
	if _, _, e := EncodeChangedBC7(bad, target, 6, 64, 64); e == nil {
		t.Fatal("DXT5 accepted")
	}
	d := t.TempDir()
	src := filepath.Join(d, "source.tid")
	pngFile := filepath.Join(d, "target.png")
	out := filepath.Join(d, "output.tid")
	if e := os.WriteFile(src, tid, 0600); e != nil {
		t.Fatal(e)
	}
	originalRGBA, _, _ := mskit.DecodeTIDBC7RGBA(tid)
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			i := (y*8 + x) * 4
			img.SetNRGBA(x, y, color.NRGBA{R: originalRGBA[i], G: originalRGBA[i+1], B: originalRGBA[i+2], A: originalRGBA[i+3]})
		}
	}
	f, e := os.Create(pngFile)
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, img); e != nil {
		t.Fatal(e)
	}
	f.Close()
	pngRaw, _ := os.ReadFile(pngFile)
	pHash := sha256.Sum256(pngRaw)
	args := []string{"--source-tid", src, "--target-png", pngFile, "--out", out, "--source-sha256", digest(tid), "--png-sha256", hex.EncodeToString(pHash[:])}
	if _, e = execute(args); e != nil {
		t.Fatal(e)
	}
	if got, e := os.ReadFile(out); e != nil || !bytes.Equal(got, tid) {
		t.Fatal("no-op CLI changed original", e)
	}
	if _, e = execute(args); e == nil {
		t.Fatal("clobbered existing output")
	}
	args[len(args)-1] = strings.Repeat("0", 64)
	args[5] = filepath.Join(d, "other.tid")
	if _, e = execute(args); e == nil {
		t.Fatal("source hash failure not rejected")
	}
}
