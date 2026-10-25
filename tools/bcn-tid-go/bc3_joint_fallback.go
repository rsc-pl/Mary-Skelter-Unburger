package main

// BC3 joint endpoint fallback: strictly a candidate producer, never a bypass
// for the independent DXT5 decoder or per-pixel composited quality limits.
// Invoked only on blocks rejected by the existing BC3 encoders. No alterations
// to accepted typography, atlas source pixels, or unchanged compressed blocks.

import (
	"encoding/binary"
	"fmt"
	"sort"

	"maryskelter-unburger/internal/mskit"
)

type bc3JointResult struct {
	block          []byte
	alpha, visible int
	sum            int
}

func bc3JointAlphaPalette(a0, a1 int) [8]int {
	p := [8]int{a0, a1}
	if a0 > a1 {
		for k := 1; k <= 6; k++ {
			p[k+1] = ((7-k)*a0 + k*a1) / 7
		}
	} else {
		for k := 1; k <= 4; k++ {
			p[k+1] = ((5-k)*a0 + k*a1) / 5
		}
		p[6], p[7] = 0, 255
	}
	return p
}

func bc3JointSeedAlphas(target [64]byte, base []byte) [][2]int {
	lo, hi := 255, 0
	for i := 0; i < 16; i++ {
		a := int(target[4*i+3])
		if a < lo {
			lo = a
		}
		if a > hi {
			hi = a
		}
	}
	seeds := map[[2]int]bool{}
	add := func(a, b int) {
		if a >= 0 && a <= 255 && b >= 0 && b <= 255 {
			seeds[[2]int{a, b}] = true
		}
	}
	add(int(base[0]), int(base[1]))
	add(hi, lo)
	add(lo, hi)
	add(255, 0)
	add(0, 255)
	for _, n := range []int{-16, -8, 0, 8, 16} {
		add(hi+n, lo)
		add(hi, lo+n)
	}
	out := make([][2]int, 0, len(seeds))
	for k := range seeds {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func bc3JointVisiblePixel(target [64]byte, i int, rgb RGB, encodedAlpha int) (int, int) {
	off := i * 4
	wantA := int(target[off+3])
	maxErr := Abs(wantA - encodedAlpha)
	sum := maxErr * maxErr
	colors := [3]int{rgb.r, rgb.g, rgb.b}
	for c := 0; c < 3; c++ {
		want := (int(target[off+c])*wantA + 127) / 255
		got := (colors[c]*encodedAlpha + 127) / 255
		db := Abs(want - got)
		dw := Abs((want + 255 - wantA) - (got + 255 - encodedAlpha))
		if db > maxErr {
			maxErr = db
		}
		if dw > maxErr {
			maxErr = dw
		}
		sum += db*db + dw*dw
	}
	return maxErr, sum
}

func bc3JointBestIndices(target [64]byte, palette [4]RGB, alphas [8]int) (aidx [16]int, cidx [16]int, worst, sum int) {
	for i := 0; i < 16; i++ {
		wbest, sbest := 1<<30, 1<<30
		for a := 0; a < 8; a++ {
			for c := 0; c < 4; c++ {
				w, s := bc3JointVisiblePixel(target, i, palette[c], alphas[a])
				if w < wbest || w == wbest && s < sbest {
					wbest, sbest = w, s
					aidx[i], cidx[i] = a, c
				}
			}
		}
		if wbest > worst {
			worst = wbest
		}
		sum += sbest
	}
	return
}

func bc3JointPack(base []byte, a0, a1 int, alphaIndex, colorIndex [16]int, color Candidate) []byte {
	out := append([]byte(nil), base...)
	out[0], out[1] = byte(a0), byte(a1)
	var abits uint64
	for i := 0; i < 16; i++ {
		abits |= uint64(alphaIndex[i]) << (3 * i)
	}
	for i := 0; i < 6; i++ {
		out[2+i] = byte(abits >> (8 * i))
	}
	binary.LittleEndian.PutUint16(out[8:10], color.c0)
	binary.LittleEndian.PutUint16(out[10:12], color.c1)
	var cbits uint32
	for i := 0; i < 16; i++ {
		cbits |= uint32(colorIndex[i]) << (2 * i)
	}
	binary.LittleEndian.PutUint32(out[12:16], cbits)
	return out
}

// MinimaxJointBC3 searches a small deterministic set of BC3 alpha endpoint
// modes and RGB565 palettes. Every candidate is independently decoded and
// measured against the original, unchanged RGBA target. The calling encoder
// must still enforce the original strict maxAlpha/maxVisible thresholds.
func MinimaxJointBC3(target [64]byte, base []byte) ([]byte, int, int, error) {
	if len(base) != 16 {
		return nil, 0, 0, fmt.Errorf("BC3 joint fallback requires one 16-byte block")
	}
	decoded, err := mskit.DecodeDXT5RGBA(base, 4, 4)
	if err != nil {
		return nil, 0, 0, err
	}
	var original [64]byte
	copy(original[:], decoded)
	aa, vv := visibleDiff(target, original)
	best := bc3JointResult{block: append([]byte(nil), base...), alpha: aa, visible: vv, sum: 1 << 30}
	score := func(a, v int) int {
		if a > v {
			return a
		}
		return v
	}
	better := func(a, v, s int) bool {
		w, old := score(a, v), score(best.alpha, best.visible)
		return w < old || w == old && (v < best.visible || v == best.visible && (a < best.alpha || a == best.alpha && s < best.sum))
	}
	c0, c1 := binary.LittleEndian.Uint16(base[8:10]), binary.LittleEndian.Uint16(base[10:12])
	seeds := []Candidate{Try(target, [16]int{}, c0, c1)}
	// Base and canonical target colors cover both the initially chosen palette
	// and the actual colours that the encoder must represent. Deterministic.
	colors := map[uint16]bool{c0: true, c1: true}
	for i := 0; i < 16; i++ {
		p := 4 * i
		colors[Quant(int(target[p]), int(target[p+1]), int(target[p+2]))] = true
	}
	keys := make([]int, 0, len(colors))
	for v := range colors {
		keys = append(keys, int(v))
	}
	sort.Ints(keys)
	for i := 0; i < len(keys); i++ {
		for j := 0; j < i; j++ {
			seeds = append(seeds, Try(target, [16]int{}, uint16(keys[i]), uint16(keys[j])))
		}
	}
	for _, ends := range bc3JointSeedAlphas(target, base) {
		ap := bc3JointAlphaPalette(ends[0], ends[1])
		var alpha [16]int
		for i := 0; i < 16; i++ {
			d := 1 << 30
			for _, a := range ap {
				dist := Abs(a - int(target[4*i+3]))
				if dist < d {
					d = dist
					alpha[i] = a
				}
			}
		}
		// Fast endpoint ranking under the chosen alpha palette, then bounded
		// per-channel refinement; never mutates input or enables lossy output.
		ranked := make([]Candidate, 0, len(seeds))
		for _, seed := range seeds {
			ranked = append(ranked, Try(target, alpha, seed.c0, seed.c1))
		}
		sort.Slice(ranked, func(i, j int) bool { return Less(ranked[i], ranked[j]) })
		n := 2
		if len(ranked) < n {
			n = len(ranked)
		}
		for ix := 0; ix < n; ix++ {
			col := Refine(target, alpha, ranked[ix])
			for iteration := 0; iteration < 2; iteration++ {
				pal := Palette(col.c0, col.c1)
				aidx, cidx, _, sum := bc3JointBestIndices(target, pal, ap)
				trial := bc3JointPack(base, ends[0], ends[1], aidx, cidx, col)
				px, e := mskit.DecodeDXT5RGBA(trial, 4, 4)
				if e != nil {
					return nil, 0, 0, e
				}
				var pixels [64]byte
				copy(pixels[:], px)
				a, v := visibleDiff(target, pixels)
				if better(a, v, sum) {
					best = bc3JointResult{trial, a, v, sum}
				}
				for i := 0; i < 16; i++ {
					alpha[i] = ap[aidx[i]]
				}
				if iteration == 0 {
					col = Refine(target, alpha, Try(target, alpha, col.c0, col.c1))
				}
			}
		}
	}
	return best.block, best.alpha, best.visible, nil
}
