package mskit

import (
	"encoding/binary"
	"fmt"
)

// DecodeDXT5RGBA decodes row-major BC3/DXT5 compressed blocks to top-down
// RGBA8 pixels. It does not modify source bytes. The shipped TID90/TID91
// payloads use exactly this layout, without a tiled/swizzled transform.
func DecodeDXT5RGBA(payload []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 || height > (1<<26)/width {
		return nil, fmt.Errorf("DXT5 RGBA image dimensions invalid or exceed 64 megapixels: %dx%d", width, height)
	}
	expected, err := TexturePayloadSize(width, height)
	if err != nil {
		return nil, err
	}
	if len(payload) != expected {
		return nil, fmt.Errorf("DXT5 payload length %d != %d", len(payload), expected)
	}

	out := make([]byte, width*height*4)
	blocksX := (width + 3) / 4
	blocksY := (height + 3) / 4
	for by := 0; by < blocksY; by++ {
		for bx := 0; bx < blocksX; bx++ {
			block := payload[(by*blocksX+bx)*16:][:16]
			a0, a1 := int(block[0]), int(block[1])
			ap := [8]int{a0, a1}
			if a0 > a1 {
				for k := 1; k <= 6; k++ {
					ap[k+1] = ((7-k)*a0 + k*a1) / 7
				}
			} else {
				for k := 1; k <= 4; k++ {
					ap[k+1] = ((5-k)*a0 + k*a1) / 5
				}
				ap[6] = 0
				ap[7] = 255
			}
			var alphaBits uint64
			for j := 0; j < 6; j++ {
				alphaBits |= uint64(block[2+j]) << (8 * j)
			}
			c0 := int(binary.LittleEndian.Uint16(block[8:10]))
			c1 := int(binary.LittleEndian.Uint16(block[10:12]))
			colors := bc3ColorPalette(c0, c1)
			colBits := binary.LittleEndian.Uint32(block[12:16])
			for py := 0; py < 4; py++ {
				y := by*4 + py
				if y >= height {
					continue
				}
				for px := 0; px < 4; px++ {
					x := bx*4 + px
					if x >= width {
						continue
					}
					idx := py*4 + px
					col := colors[(colBits>>(2*idx))&3]
					off := (y*width + x) * 4
					out[off] = byte(col.r)
					out[off+1] = byte(col.g)
					out[off+2] = byte(col.b)
					out[off+3] = byte(ap[(alphaBits>>(3*idx))&7])
				}
			}
		}
	}
	return out, nil
}

// DecodeTIDDXT5RGBA validates the original TID header and payload before decoding.
func DecodeTIDDXT5RGBA(tid []byte) ([]byte, TIDMeta, error) {
	meta, err := ParseTIDMeta(tid)
	if err != nil {
		return nil, meta, err
	}
	if meta.Format != "DXT5" {
		return nil, meta, fmt.Errorf("DXT5 decoder cannot handle %s", meta.Format)
	}
	rgba, err := DecodeDXT5RGBA(tid[0x80:], meta.Width, meta.Height)
	return rgba, meta, err
}
