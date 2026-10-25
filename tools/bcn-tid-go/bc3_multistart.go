package main

// legacy opt-in bounded wide-step RGB565 minmax search for BC3 blocks that fail
// the pinned alpha/visible guard after legacy. Alpha bytes remain unmodified.
// This is not a fallback to lowering visual thresholds.
import (
	"encoding/binary"
	"fmt"
	"maryskelter-unburger/internal/mskit"
	"sort"
)

type Candidate struct {
	c0, c1     uint16
	worst, sum int
	bits       uint32
}

func Try(target [64]byte, alpha [16]int, c0, c1 uint16) Candidate {
	if c0 < c1 {
		c0, c1 = c1, c0
	}
	if c0 == c1 {
		return Candidate{worst: 1 << 30, sum: 1 << 30}
	}
	worst, sum, bits := BestRGB(target, alpha, c0, c1)
	return Candidate{c0, c1, worst, sum, bits}
}
func Less(a, b Candidate) bool {
	if a.worst != b.worst {
		return a.worst < b.worst
	}
	if a.sum != b.sum {
		return a.sum < b.sum
	}
	if a.c0 != b.c0 {
		return a.c0 < b.c0
	}
	return a.c1 < b.c1
}
func Shift(v uint16, channel, change int) (uint16, bool) {
	shifts := [3]uint{11, 5, 0}
	masks := [3]int{31, 63, 31}
	shift := shifts[channel]
	orig := int((v >> shift) & uint16(masks[channel]))
	next := orig + change
	if next < 0 || next > masks[channel] {
		return 0, false
	}
	return (v &^ (uint16(masks[channel]) << shift)) | uint16(next)<<shift, true
}
func Refine(target [64]byte, alpha [16]int, start Candidate) Candidate {
	cur := start
	for _, step := range []int{16, 8, 4, 2, 1} {
		for rounds := 0; rounds < 18; rounds++ {
			next := cur
			for endpoint := 0; endpoint < 2; endpoint++ {
				for channel := 0; channel < 3; channel++ {
					for _, dir := range []int{-1, 1} {
						c0, c1 := cur.c0, cur.c1
						if endpoint == 0 {
							v, ok := Shift(c0, channel, dir*step)
							if !ok {
								continue
							}
							c0 = v
						} else {
							v, ok := Shift(c1, channel, dir*step)
							if !ok {
								continue
							}
							c1 = v
						}
						cand := Try(target, alpha, c0, c1)
						if Less(cand, next) {
							next = cand
						}
					}
				}
			}
			if !Less(next, cur) {
				break
			}
			cur = next
		}
	}
	return cur
}
func MinimaxRGBBC3ExpandedSearch(target [64]byte, base []byte) ([]byte, int, int, error) {
	if len(base) != 16 {
		return nil, 0, 0, fmt.Errorf("legacy expects 16-byte BC3 block")
	}
	decoded, err := mskit.DecodeDXT5RGBA(base, 4, 4)
	if err != nil {
		return nil, 0, 0, err
	}
	var alpha [16]int
	for i := 0; i < 16; i++ {
		alpha[i] = int(decoded[i*4+3])
	}
	c0 := binary.LittleEndian.Uint16(base[8:10])
	c1 := binary.LittleEndian.Uint16(base[10:12])
	seeds := map[uint16]struct{}{c0: {}, c1: {}, 0: {}, 65535: {}}
	for i := 0; i < 16; i++ {
		j := 4 * i
		seeds[Quant(int(target[j]), int(target[j+1]), int(target[j+2]))] = struct{}{}
	}
	keys := make([]int, 0, len(seeds))
	for k := range seeds {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	starts := []Candidate{Try(target, alpha, c0, c1)}
	for _, u := range keys {
		for _, v := range keys {
			if u <= v {
				continue
			}
			starts = append(starts, Try(target, alpha, uint16(u), uint16(v)))
		}
	}
	sort.Slice(starts, func(i, j int) bool { return Less(starts[i], starts[j]) })
	keep := 12
	if len(starts) < keep {
		keep = len(starts)
	}
	best := starts[0]
	for i := 0; i < keep; i++ {
		refined := Refine(target, alpha, starts[i])
		if Less(refined, best) {
			best = refined
		}
	}
	out := append([]byte(nil), base...)
	binary.LittleEndian.PutUint16(out[8:10], best.c0)
	binary.LittleEndian.PutUint16(out[10:12], best.c1)
	binary.LittleEndian.PutUint32(out[12:16], best.bits)
	if string(out[:8]) != string(base[:8]) {
		return nil, 0, 0, fmt.Errorf("legacy alpha bytes mutated")
	}
	rgba, err := mskit.DecodeDXT5RGBA(out, 4, 4)
	if err != nil {
		return nil, 0, 0, err
	}
	var pix [64]byte
	copy(pix[:], rgba)
	a, v := visibleDiff(target, pix)
	if v != best.worst {
		return nil, 0, 0, fmt.Errorf("legacy independent visible mismatch %d != %d", v, best.worst)
	}
	return out, a, v, nil
}
