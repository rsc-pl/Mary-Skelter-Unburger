package mskit

import (
	"encoding/binary"
	"fmt"
)

// DecodeDXT1RGBA and DecodeDXT3RGBA decode the last two supported legacy
// source formats without DDS/Pillow. Block order is row-major, with clipped
// writes for logical dimensions not divisible by four.
func DecodeDXT1RGBA(payload []byte, width, height int) ([]byte, error) {
	return decodeDXT1DXT3RGBA(payload, width, height, false)
}
func DecodeDXT3RGBA(payload []byte, width, height int) ([]byte, error) {
	return decodeDXT1DXT3RGBA(payload, width, height, true)
}
func decodeDXT1DXT3RGBA(payload []byte, width, height int, dxt3 bool) ([]byte, error) {
	codec, blockSize := "DXT1", 8
	if dxt3 {
		codec, blockSize = "DXT3", 16
	}
	const maxPixels = 1 << 26
	if width <= 0 || height <= 0 || width > maxPixels || height > maxPixels/width {
		return nil, fmt.Errorf("%s dimensions invalid or exceed 64 megapixels: %dx%d", codec, width, height)
	}
	bx, by := (width+3)/4, (height+3)/4
	expected := bx * by * blockSize
	if len(payload) != expected {
		return nil, fmt.Errorf("%s payload length %d != %d", codec, len(payload), expected)
	}
	rgba := make([]byte, width*height*4)
	for yb := 0; yb < by; yb++ {
		for xb := 0; xb < bx; xb++ {
			block := payload[(yb*bx+xb)*blockSize:][:blockSize]
			offset := 0
			if dxt3 {
				offset = 8
			}
			c0 := int(binary.LittleEndian.Uint16(block[offset : offset+2]))
			c1 := int(binary.LittleEndian.Uint16(block[offset+2 : offset+4]))
			pal := bc3ColorPalette(c0, c1)
			transparent := !dxt3 && c0 <= c1
			if transparent {
				p0, p1 := pal[0], pal[1]
				pal[2] = rgb8{(p0.r + p1.r) / 2, (p0.g + p1.g) / 2, (p0.b + p1.b) / 2}
			}
			indices := binary.LittleEndian.Uint32(block[offset+4 : offset+8])
			for yy := 0; yy < 4; yy++ {
				for xx := 0; xx < 4; xx++ {
					x, y := xb*4+xx, yb*4+yy
					if x >= width || y >= height {
						continue
					}
					idx := yy*4 + xx
					ci := (indices >> uint(2*idx)) & 3
					col := pal[ci]
					dst := (y*width + x) * 4
					rgba[dst], rgba[dst+1], rgba[dst+2] = byte(col.r), byte(col.g), byte(col.b)
					alpha := byte(255)
					if dxt3 {
						alpha = byte((block[idx/2]>>uint(4*(idx%2)))&15) * 17
					} else if transparent && ci == 3 {
						rgba[dst], rgba[dst+1], rgba[dst+2] = 0, 0, 0
						alpha = 0
					}
					rgba[dst+3] = alpha
				}
			}
		}
	}
	return rgba, nil
}
func DecodeTIDDXT1RGBA(tid []byte) ([]byte, TIDMeta, error) { return decodeTIDLegacyRGBA(tid, "DXT1") }
func DecodeTIDDXT3RGBA(tid []byte) ([]byte, TIDMeta, error) { return decodeTIDLegacyRGBA(tid, "DXT3") }
func decodeTIDLegacyRGBA(tid []byte, format string) ([]byte, TIDMeta, error) {
	meta, err := ParseTIDMeta(tid)
	if err != nil {
		return nil, meta, err
	}
	if meta.Format != format {
		return nil, meta, fmt.Errorf("%s decoder cannot handle %s", format, meta.Format)
	}
	if format == "DXT1" {
		out, e := DecodeDXT1RGBA(tid[0x80:], meta.Width, meta.Height)
		return out, meta, e
	}
	out, e := DecodeDXT3RGBA(tid[0x80:], meta.Width, meta.Height)
	return out, meta, e
}
