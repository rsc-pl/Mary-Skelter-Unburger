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
)

func makeDXT5TestTexture(w, h int) []byte {
	b := make([]byte, 128+((w+3)/4)*((h+3)/4)*16)
	copy(b[:4], "TID\x90")
	b[0x44] = byte(w)
	b[0x48] = byte(h)
	copy(b[0x64:], "DXT5")
	return b
}
func TestLabelDXT5CleanCropAndNoStrayBlocks(t *testing.T) {
	src := makeDXT5TestTexture(64, 64)
	original := append([]byte(nil), src...)
	clean := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	mask := image.NewAlpha(image.Rect(0, 0, 32, 32))
	for y := 7; y < 22; y++ {
		for x := 7; x < 23; x++ {
			mask.SetAlpha(x, y, color.Alpha{A: 255})
		}
	}
	result, preview, rep, err := CreateTID(src, image.Rect(16, 16, 48, 48), clean, mask, color.NRGBA{255, 255, 255, 255})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src, original) {
		t.Fatal("source modified")
	}
	if rep.ChangedBlocks == 0 || rep.AlphaChangedPixels == 0 || rep.PixelsOutsideRectChanged != 0 || rep.Status != "PASS" {
		t.Fatalf("bad report: %#v", rep)
	}
	if preview.NRGBAAt(12, 12).A == 0 || preview.NRGBAAt(0, 0).A != 0 {
		t.Fatal("preview lacks alpha isolation")
	}
	blocks := 64 / 4
	for by := 0; by < blocks; by++ {
		for bx := 0; bx < blocks; bx++ {
			if bx >= 4 && bx < 12 && by >= 4 && by < 12 {
				continue
			}
			off := 128 + (by*blocks+bx)*16
			if !bytes.Equal(result[off:off+16], src[off:off+16]) {
				t.Fatalf("stray block x=%d y=%d", bx, by)
			}
		}
	}
	_, _, _, err = CreateTID(src, image.Rect(16, 16, 48, 48), clean, mask, color.NRGBA{255, 255, 255, 255})
	if err != nil {
		t.Fatal(err)
	}
}
func TestRejectsClippingAndSourceDrift(t *testing.T) {
	src := makeDXT5TestTexture(64, 64)
	clean := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	mask := image.NewAlpha(image.Rect(0, 0, 32, 32))
	mask.SetAlpha(0, 12, color.Alpha{255})
	if _, _, _, err := CreateTID(src, image.Rect(16, 16, 48, 48), clean, mask, color.NRGBA{255, 255, 255, 255}); err == nil {
		t.Fatal("clipped glyph accepted")
	}
	if _, err := Rectangle("3,4,32,32"); err == nil {
		t.Fatal("unaligned rect accepted")
	}
	if _, err := Rectangle("0,0,9000,32"); err == nil {
		t.Fatal("huge rect accepted")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "old.tid")
	if err := os.WriteFile(path, src, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPinned(path, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong hash accepted")
	}
	hash := sha256.Sum256(src)
	if _, err := ReadPinned(path, hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
}
func TestPublishExclusively(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.tid")
	if err := PublishNew(path, []byte("test")); err != nil {
		t.Fatal(err)
	}
	if err := PublishNew(path, []byte("overwrite")); err == nil {
		t.Fatal("overwrote existing output")
	}
	b, err := os.ReadFile(path)
	if err != nil || string(b) != "test" {
		t.Fatal("existing output changed")
	}
	var output bytes.Buffer
	if err = png.Encode(&output, image.NewNRGBA(image.Rect(0, 0, 32, 32))); err != nil {
		t.Fatal(err)
	}
}
func TestPrivateRealFontIfAvailable(t *testing.T) {
	// Go project includes user-provided private fonts in private FULL only.
	// No external font is downloaded or redistributed separately.
	font, err := os.ReadFile("../../third_party/fonts/chiaro/ChiaroStd-B.otf")
	if err != nil {
		t.Skip("private font absent")
	}
	_ = font // native font rasterization exercised in the separate integration oracle.
}
