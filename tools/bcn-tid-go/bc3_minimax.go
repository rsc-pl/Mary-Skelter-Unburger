package main

// legacy experimental, bounded BC3 RGB565 palette search for *failing* gold
// blocks. Independent BC3 decoder remains the only authority. No thresholds
// are relaxed. Alpha bits are copied bit-for-bit from the supplied candidate.
import (
	"encoding/binary"
	"errors"
	"fmt"
	"maryskelter-unburger/internal/mskit"
	"sort"
)

type RGB struct{ r, g, b int }

func Expand(v uint16) RGB {
	r, g, b := int((v>>11)&31), int((v>>5)&63), int(v&31)
	return RGB{(r << 3) | (r >> 2), (g << 2) | (g >> 4), (b << 3) | (b >> 2)}
}
func Quant(r, g, b int) uint16 {
	return uint16(((r*31+127)/255)<<11 | ((g*63+127)/255)<<5 | ((b*31 + 127) / 255))
}
func Palette(c0, c1 uint16) [4]RGB {
	a, b := Expand(c0), Expand(c1)
	return [4]RGB{a, b, {(2*a.r + b.r) / 3, (2*a.g + b.g) / 3, (2*a.b + b.b) / 3}, {(a.r + 2*b.r) / 3, (a.g + 2*b.g) / 3, (a.b + 2*b.b) / 3}}
}
func Abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// Measure exact composites against black/white for 16 pixels. Alpha comes
// from the original compressed block and cannot be altered by this search.
func BestRGB(target [64]byte, alpha [16]int, c0, c1 uint16) (int, int, uint32) {
	p := Palette(c0, c1)
	worst, sum := 0, 0
	var bits uint32
	for i := 0; i < 16; i++ {
		off := 4 * i
		wA := int(target[off+3])
		a := alpha[i]
		bestMax, bestSum, bestIndex := 1<<30, 1<<30, 0
		for j, q := range p {
			mx, sq := 0, 0
			vals := [3]int{q.r, q.g, q.b}
			for c := 0; c < 3; c++ {
				want := (int(target[off+c])*wA + 127) / 255
				got := (vals[c]*a + 127) / 255
				db := Abs(want - got)
				dw := Abs((want + 255 - wA) - (got + 255 - a))
				if db > mx {
					mx = db
				}
				if dw > mx {
					mx = dw
				}
				sq += db*db + dw*dw
			}
			if mx < bestMax || (mx == bestMax && sq < bestSum) {
				bestMax, bestSum, bestIndex = mx, sq, j
			}
		}
		if bestMax > worst {
			worst = bestMax
		}
		sum += bestSum
		bits |= uint32(bestIndex) << (2 * i)
	}
	return worst, sum, bits
}
func ColorNeighbors(v uint16) []uint16 {
	r, g, b := int((v>>11)&31), int((v>>5)&63), int(v&31)
	out := make([]uint16, 0, 13)
	for _, s := range [][3]int{{-1, 0, 0}, {1, 0, 0}, {0, -1, 0}, {0, 1, 0}, {0, 0, -1}, {0, 0, 1}, {-2, 0, 0}, {2, 0, 0}, {0, -2, 0}, {0, 2, 0}, {0, 0, -2}, {0, 0, 2}} {
		rr, gg, bb := r+s[0], g+s[1], b+s[2]
		if rr >= 0 && rr <= 31 && gg >= 0 && gg <= 63 && bb >= 0 && bb <= 31 {
			out = append(out, uint16(rr<<11|gg<<5|bb))
		}
	}
	return out
}

// MinimaxRGBBC3AlphaPreserving will return nil if it cannot strictly improve the
// independent-decoder-visible maximum and tie-breaking squared error.
// It never mutates the caller's source; max. palette endpoints is bounded.
func MinimaxRGBBC3AlphaPreserving(target [64]byte, base []byte) ([]byte, int, int, error) {
	if len(base) != 16 {
		return nil, 0, 0, errors.New("BC3 minimax expects 16-byte BC3 block")
	} // implemented below? no
	var alpha [16]int
	decoded, e := mskit.DecodeDXT5RGBA(base, 4, 4)
	if e != nil {
		return nil, 0, 0, e
	}
	for i := 0; i < 16; i++ {
		alpha[i] = int(decoded[i*4+3])
	}
	candidates := make(map[uint16]struct{}, 20)
	c0 := binary.LittleEndian.Uint16(base[8:10])
	c1 := binary.LittleEndian.Uint16(base[10:12])
	candidates[c0] = struct{}{}
	candidates[c1] = struct{}{}
	minR, minG, minB := 255, 255, 255
	maxR, maxG, maxB := 0, 0, 0
	for i := 0; i < 16; i++ {
		o := i * 4
		r, g, b := int(target[o]), int(target[o+1]), int(target[o+2])
		if r < minR {
			minR = r
		}
		if g < minG {
			minG = g
		}
		if b < minB {
			minB = b
		}
		if r > maxR {
			maxR = r
		}
		if g > maxG {
			maxG = g
		}
		if b > maxB {
			maxB = b
		}
		candidates[Quant(r, g, b)] = struct{}{}
	}
	candidates[Quant(minR, minG, minB)] = struct{}{}
	candidates[Quant(maxR, maxG, maxB)] = struct{}{}
	candidates[Quant(maxR, minG, maxB)] = struct{}{}
	candidates[Quant(maxR, maxG, minB)] = struct{}{}
	// Start from the existing candidate, find the best pair of quantized
	// colours sampled in the block, then refine with bounded RGB565 steps.
	best0, best1 := c0, c1
	if best0 < best1 {
		best0, best1 = best1, best0
	}
	if best0 == best1 {
		if best0 < 65535 {
			best0++
		} else {
			best1--
		}
	}
	bestMax, bestSum, bestBits := BestRGB(target, alpha, best0, best1)
	better := func(mx, sq int) bool { return mx < bestMax || (mx == bestMax && sq < bestSum) }
	keys := make([]int, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, int(key))
	}
	sort.Ints(keys)
	for _, xi := range keys {
		x := uint16(xi)
		for _, yi := range keys {
			y := uint16(yi)
			if x <= y {
				continue
			}
			mx, sq, bi := BestRGB(target, alpha, x, y)
			if better(mx, sq) {
				best0, best1, bestMax, bestSum, bestBits = x, y, mx, sq, bi
			}
		}
	}
	for step := 0; step < 8; step++ {
		old0, old1 := best0, best1
		for _, x := range ColorNeighbors(best0) {
			if x <= best1 {
				continue
			}
			mx, sq, bi := BestRGB(target, alpha, x, best1)
			if better(mx, sq) {
				best0, bestMax, bestSum, bestBits = x, mx, sq, bi
			}
		}
		for _, y := range ColorNeighbors(best1) {
			if best0 <= y {
				continue
			}
			mx, sq, bi := BestRGB(target, alpha, best0, y)
			if better(mx, sq) {
				best1, bestMax, bestSum, bestBits = y, mx, sq, bi
			}
		}
		if best0 == old0 && best1 == old1 {
			break
		}
	}
	out := append([]byte(nil), base...)
	binary.LittleEndian.PutUint16(out[8:10], best0)
	binary.LittleEndian.PutUint16(out[10:12], best1)
	binary.LittleEndian.PutUint32(out[12:16], bestBits)
	actual, err := mskit.DecodeDXT5RGBA(out, 4, 4)
	if err != nil {
		return nil, 0, 0, err
	}
	var tile [64]byte
	copy(tile[:], actual)
	a, v := visibleDiff(target, tile)
	if v != bestMax {
		return nil, 0, 0, fmt.Errorf("BC3 minimax internal parity: %d != %d", v, bestMax)
	}
	return out, a, v, nil
}
