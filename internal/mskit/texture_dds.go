package mskit

import (
	"encoding/binary"
	"fmt"
)

// TIDToDDS wraps a validated compressed TID payload in the exact legacy DDS
// container consumed by the graphics renderer. No block/pixel transcoding
// takes place: this is solely the formerly Python-owned source conversion.
func TIDToDDS(tid []byte) ([]byte, TIDMeta, error) {
	meta, err := ParseTIDMeta(tid)
	if err != nil {
		return nil, meta, err
	}
	var blockSize int
	switch meta.Format {
	case "DXT1":
		blockSize = 8
	case "DXT3", "DXT5", "BC7":
		blockSize = 16
	default:
		return nil, meta, fmt.Errorf("unsupported TID-to-DDS codec %q", meta.Format)
	}
	const maxPixels = 1 << 26
	if meta.Width <= 0 || meta.Height <= 0 || meta.Width > maxPixels || meta.Height > maxPixels/meta.Width {
		return nil, meta, fmt.Errorf("TID dimensions exceed 64 megapixels: %dx%d", meta.Width, meta.Height)
	}
	cols, rows := (meta.Width+3)/4, (meta.Height+3)/4
	expected := cols * rows * blockSize
	if len(tid) != 0x80+expected {
		return nil, meta, fmt.Errorf("TID %s payload size mismatch: %d != %d", meta.Format, len(tid)-0x80, expected)
	}
	extra := 0
	if meta.Format == "BC7" {
		extra = 20
	}
	dds := make([]byte, 128+extra+expected)
	copy(dds[:4], "DDS ")
	put := func(off int, value uint32) { binary.LittleEndian.PutUint32(dds[off:off+4], value) }
	put(4, 124)                        // DDS_HEADER dwSize
	put(8, 0x1|0x2|0x4|0x1000|0x80000) // size, flags, height, width, pitch/linear
	put(12, uint32(meta.Height))
	put(16, uint32(meta.Width))
	put(20, uint32(cols*blockSize))
	put(28, 1)  // mip map count
	put(76, 32) // DDPIXELFORMAT dwSize
	put(80, 4)  // FOURCC
	if meta.Format == "BC7" {
		copy(dds[84:88], "DX10")
		put(128, 98) // DXGI_FORMAT_BC7_UNORM
		put(132, 3)  // D3D10_RESOURCE_DIMENSION_TEXTURE2D
		put(140, 1)  // array size
	} else {
		copy(dds[84:88], meta.Format)
	}
	put(108, 0x1000) // DDSCAPS_TEXTURE
	copy(dds[128+extra:], tid[0x80:])
	return dds, meta, nil
}
