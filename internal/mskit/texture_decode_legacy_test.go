package mskit

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func makeLegacyTID(format string, magic byte, width, height int, blocks []byte) []byte {
	tid := make([]byte, 128+len(blocks))
	copy(tid[:4], []byte{'T', 'I', 'D', magic})
	if magic == 0x90 {
		binary.LittleEndian.PutUint32(tid[0x44:], uint32(width))
		binary.LittleEndian.PutUint32(tid[0x48:], uint32(height))
		copy(tid[0x64:], format)
	} else {
		binary.BigEndian.PutUint32(tid[0x44:], uint32(width))
		binary.BigEndian.PutUint32(tid[0x48:], uint32(height))
		for i := 0; i < 4; i++ {
			tid[0x64+i] = format[3-i]
		}
	}
	copy(tid[128:], blocks)
	return tid
}
func TestLegacyDXT1TransparentVsOpaqueAndTID91(t *testing.T) {
	block := make([]byte, 8)
	// c0 < c1 activates BC1 three-color mode, index=3 is transparent.
	binary.LittleEndian.PutUint16(block[0:], 0)
	binary.LittleEndian.PutUint16(block[2:], 0xffff)
	binary.LittleEndian.PutUint32(block[4:], 0xffffffff)
	rgba, m, err := DecodeTIDDXT1RGBA(makeLegacyTID("DXT1", 0x91, 4, 4, block))
	if err != nil || m.Magic != "TID91" || len(rgba) != 64 {
		t.Fatalf("%v %+v", err, m)
	}
	if !bytes.Equal(rgba, make([]byte, 64)) {
		t.Fatalf("transparent index 3 should be RGBA=0, got %v", rgba[:4])
	}
	// Four-color mode must never mark index 3 transparent.
	binary.LittleEndian.PutUint16(block[0:], 0xffff)
	binary.LittleEndian.PutUint16(block[2:], 0)
	opaque, err := DecodeDXT1RGBA(block, 4, 4)
	if err != nil || opaque[3] != 255 || opaque[0] == 0 {
		t.Fatalf("DXT1 four-color mode: %v, %v", err, opaque[:4])
	}
}
func TestLegacyDXT3AlphaExplicitFourColorAndTID90(t *testing.T) {
	block := make([]byte, 16)
	block[0] = 0x10 // alpha pixel 0=0, pixel 1=17
	binary.LittleEndian.PutUint16(block[8:], 0)
	binary.LittleEndian.PutUint16(block[10:], 0xffff)
	binary.LittleEndian.PutUint32(block[12:], 0xffffffff) // index 3 uses 4 colors, never BC1 transparency
	rgba, m, err := DecodeTIDDXT3RGBA(makeLegacyTID("DXT3", 0x90, 4, 4, block))
	if err != nil || m.Magic != "TID90" {
		t.Fatal(err, m)
	}
	if rgba[3] != 0 || rgba[7] != 17 || rgba[11] != 0 || rgba[0] == 0 {
		t.Fatalf("incorrect explicit DXT3 alpha or color: %v", rgba[:12])
	}
}
func TestLegacyDXT1DXT3PartialDimensionsAndRejection(t *testing.T) {
	for _, format := range []string{"DXT1", "DXT3"} {
		bs := 8
		if format == "DXT3" {
			bs = 16
		}
		src := make([]byte, bs*2)
		var out []byte
		var err error
		if format == "DXT1" {
			out, err = DecodeDXT1RGBA(src, 5, 3)
		} else {
			out, err = DecodeDXT3RGBA(src, 5, 3)
		}
		if err != nil || len(out) != 60 {
			t.Fatalf("%s partial: %v", format, err)
		}
		for _, invalid := range [][]byte{src[:len(src)-1], append(append([]byte{}, src...), 0)} {
			if format == "DXT1" {
				_, err = DecodeDXT1RGBA(invalid, 5, 3)
			} else {
				_, err = DecodeDXT3RGBA(invalid, 5, 3)
			}
			if err == nil {
				t.Fatalf("%s malformed payload accepted", format)
			}
		}
		if format == "DXT1" {
			_, err = DecodeDXT1RGBA(src, 100000, 100000)
		} else {
			_, err = DecodeDXT3RGBA(src, 100000, 100000)
		}
		if err == nil {
			t.Fatalf("%s overlarge dimensions accepted", format)
		}
	}
	if _, _, err := DecodeTIDDXT1RGBA(makeLegacyTID("DXT3", 0x90, 4, 4, make([]byte, 16))); err == nil {
		t.Fatal("accepted wrong format")
	}
	if _, _, err := DecodeTIDDXT3RGBA(makeLegacyTID("DXT1", 0x91, 4, 4, make([]byte, 8))); err == nil {
		t.Fatal("accepted wrong format")
	}
}
