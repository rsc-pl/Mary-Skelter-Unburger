package mskit

// BC7 decode algorithms and interpolation constants adapted to Go from Richard
// Geldreich's bc7decomp.cpp (2020), available under the MIT license. See
// third_party/licenses/bc7decoder-MIT.txt for license/attribution.
// No C, cgo, third-party dependencies or runtime network access are required.

import (
	"fmt"
	"math/bits"
)

type bc7Bits struct {
	d   []byte
	pos int
}

func (r *bc7Bits) read(n int) int {
	if n <= 0 {
		return 0
	}
	x := 0
	for i := 0; i < n; i++ {
		x |= int((r.d[(r.pos+i)>>3]>>uint((r.pos+i)&7))&1) << uint(i)
	}
	r.pos += n
	return x
}

var bc7Weights = [3][]int{
	{0, 21, 43, 64},
	{0, 9, 18, 27, 37, 46, 55, 64},
	{0, 4, 9, 13, 17, 21, 26, 30, 34, 38, 43, 47, 51, 55, 60, 64},
}

func bc7Interpolate(lo, hi, index, bits int) byte {
	weight := bc7Weights[bits-2][index]
	return byte((lo*(64-weight) + hi*weight + 32) >> 6)
}
func bc7Expand(val, size int) int {
	expanded := val << (8 - size)
	return (expanded | (expanded >> size)) & 255
}
func bc7ExpandP(val, p, size int) int {
	return bc7Expand((val<<1)|p, size+1)
}

// DecodeBC7Block decodes exactly one 16-byte BC7 block to 16 top-down RGBA
// pixels. Invalid blocks return errors, never partial pixels.
func DecodeBC7Block(block []byte) ([64]byte, int, error) {
	var out [64]byte
	if len(block) != 16 {
		return out, -1, fmt.Errorf("BC7 block: expected 16 bytes, got %d", len(block))
	}
	if block[0] == 0 {
		return out, -1, fmt.Errorf("BC7 block: reserved mode encoding")
	}
	mode := bits.TrailingZeros8(block[0])
	r := bc7Bits{d: block}
	if r.read(mode+1) != (1 << mode) {
		return out, mode, fmt.Errorf("BC7 invalid mode prefix")
	}
	switch mode {
	case 0, 2:
		partBits, wb, eb, pn := 6, 2, 5, 0
		if mode == 0 {
			partBits, wb, eb, pn = 4, 3, 4, 6
		}
		part := r.read(partBits)
		var ep [6][4]int
		for c := 0; c < 3; c++ {
			for e := 0; e < 6; e++ {
				ep[e][c] = r.read(eb)
			}
		}
		var p [6]int
		for i := 0; i < pn; i++ {
			p[i] = r.read(1)
		}
		var indices [16]int
		for i := 0; i < 16; i++ {
			n := wb
			if i == 0 || i == bc7Anchor3a[part] || i == bc7Anchor3b[part] {
				n--
			}
			indices[i] = r.read(n)
		}
		if r.pos != 128 {
			return out, mode, fmt.Errorf("BC7 mode %d bits consumed %d", mode, r.pos)
		}
		for e := 0; e < 6; e++ {
			for c := 0; c < 3; c++ {
				if mode == 0 {
					ep[e][c] = bc7ExpandP(ep[e][c], p[e], eb)
				} else {
					ep[e][c] = bc7Expand(ep[e][c], eb)
				}
			}
		}
		for i := 0; i < 16; i++ {
			s := int(bc7Partitions3[part][i]) * 2
			for c := 0; c < 3; c++ {
				out[i*4+c] = bc7Interpolate(ep[s][c], ep[s+1][c], indices[i], wb)
			}
			out[i*4+3] = 255
		}
	case 1, 3, 7:
		part := r.read(6)
		comp, wb, eb, pn := 3, 2, 7, 4
		if mode == 1 {
			wb, eb, pn = 3, 6, 2
		}
		if mode == 7 {
			comp, eb = 4, 5
		}
		var ep [4][4]int
		for c := 0; c < comp; c++ {
			for e := 0; e < 4; e++ {
				ep[e][c] = r.read(eb)
			}
		}
		var p [4]int
		for i := 0; i < pn; i++ {
			p[i] = r.read(1)
		}
		var indices [16]int
		for i := 0; i < 16; i++ {
			n := wb
			if i == 0 || i == bc7Anchor2[part] {
				n--
			}
			indices[i] = r.read(n)
		}
		if r.pos != 128 {
			return out, mode, fmt.Errorf("BC7 mode %d bits consumed %d", mode, r.pos)
		}
		for e := 0; e < 4; e++ {
			for c := 0; c < comp; c++ {
				pi := e
				if mode == 1 {
					pi = e / 2
				}
				ep[e][c] = bc7ExpandP(ep[e][c], p[pi], eb)
			}
			if comp == 3 {
				ep[e][3] = 255
			}
		}
		for i := 0; i < 16; i++ {
			subset := int(bc7Partitions2[part][i])
			s := subset * 2
			for c := 0; c < comp; c++ {
				out[i*4+c] = bc7Interpolate(ep[s][c], ep[s+1][c], indices[i], wb)
			}
			if comp == 3 {
				out[i*4+3] = 255
			}
		}
	case 4, 5:
		rotation := r.read(2)
		idxMode := 0
		if mode == 4 {
			idxMode = r.read(1)
		}
		eb, ab, abw := 7, 8, 2
		if mode == 4 {
			eb, ab, abw = 5, 6, 3
		}
		var ep [2][4]int
		for c := 0; c < 4; c++ {
			for e := 0; e < 2; e++ {
				n := eb
				if c == 3 {
					n = ab
				}
				ep[e][c] = r.read(n)
			}
		}
		rgbBits, alphaBits := 2, abw
		if idxMode == 1 {
			rgbBits, alphaBits = abw, 2
		}
		var colorI, alphaI [16]int
		for i := 0; i < 16; i++ {
			n := 2
			if i == 0 {
				n--
			}
			if idxMode == 0 {
				colorI[i] = r.read(n)
			} else {
				alphaI[i] = r.read(n)
			}
		}
		for i := 0; i < 16; i++ {
			n := abw
			if i == 0 {
				n--
			}
			if idxMode == 0 {
				alphaI[i] = r.read(n)
			} else {
				colorI[i] = r.read(n)
			}
		}
		if r.pos != 128 {
			return out, mode, fmt.Errorf("BC7 mode %d bits consumed %d", mode, r.pos)
		}
		for e := 0; e < 2; e++ {
			for c := 0; c < 4; c++ {
				n := eb
				if c == 3 {
					n = ab
				}
				ep[e][c] = bc7Expand(ep[e][c], n)
			}
		}
		for i := 0; i < 16; i++ {
			for c := 0; c < 3; c++ {
				out[i*4+c] = bc7Interpolate(ep[0][c], ep[1][c], colorI[i], rgbBits)
			}
			out[i*4+3] = bc7Interpolate(ep[0][3], ep[1][3], alphaI[i], alphaBits)
			if rotation != 0 {
				c := i*4 + rotation - 1
				a := i*4 + 3
				out[c], out[a] = out[a], out[c]
			}
		}
	case 6:
		var ep [2][4]int
		for c := 0; c < 4; c++ {
			for e := 0; e < 2; e++ {
				ep[e][c] = r.read(7)
			}
		}
		p0, p1 := r.read(1), r.read(1)
		for c := 0; c < 4; c++ {
			ep[0][c] = ep[0][c]*2 + p0
			ep[1][c] = ep[1][c]*2 + p1
		}
		for i := 0; i < 16; i++ {
			n := 4
			if i == 0 {
				n--
			}
			idx := r.read(n)
			for c := 0; c < 4; c++ {
				out[i*4+c] = bc7Interpolate(ep[0][c], ep[1][c], idx, 4)
			}
		}
		if r.pos != 128 {
			return out, mode, fmt.Errorf("BC7 mode %d bits consumed %d", mode, r.pos)
		}
	default:
		return out, mode, fmt.Errorf("BC7 mode %d not yet enabled; fail closed", mode)
	}
	return out, mode, nil
}

// DecodeBC7RGBA decodes row-major BC7 blocks without changing any source bytes.
// It fails closed on malformed modes, including an exact block position.
func DecodeBC7RGBA(payload []byte, width, height int) ([]byte, error) {
	if width <= 0 || height <= 0 || height > (1<<26)/width {
		return nil, fmt.Errorf("BC7 invalid or oversized dimensions %dx%d", width, height)
	}
	bx, by := (width+3)/4, (height+3)/4
	if len(payload) != bx*by*16 {
		return nil, fmt.Errorf("BC7 payload size %d != %d", len(payload), bx*by*16)
	}
	rgba := make([]byte, width*height*4)
	for y := 0; y < by; y++ {
		for x := 0; x < bx; x++ {
			block, mode, err := DecodeBC7Block(payload[(y*bx+x)*16:][:16])
			if err != nil {
				return nil, fmt.Errorf("BC7 block (%d,%d) mode %d: %w", x, y, mode, err)
			}
			for py := 0; py < 4; py++ {
				if y*4+py >= height {
					break
				}
				for px := 0; px < 4; px++ {
					if x*4+px >= width {
						break
					}
					dst := ((y*4+py)*width + x*4 + px) * 4
					src := (py*4 + px) * 4
					copy(rgba[dst:dst+4], block[src:src+4])
				}
			}
		}
	}
	return rgba, nil
}

func DecodeTIDBC7RGBA(tid []byte) ([]byte, TIDMeta, error) {
	meta, err := ParseTIDMeta(tid)
	if err != nil {
		return nil, meta, err
	}
	if meta.Format != "BC7" {
		return nil, meta, fmt.Errorf("BC7 decoder cannot handle %s", meta.Format)
	}
	rgba, err := DecodeBC7RGBA(tid[128:], meta.Width, meta.Height)
	return rgba, meta, err
}
