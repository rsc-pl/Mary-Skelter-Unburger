package main

// legacy: deterministic, bounded, fail-closed BC7 outlier fallback.
// This never changes the desired target atlas pixels or the strict alpha/visible guard.
// A candidate is accepted only after independent BC7 decode against original target.
import (
	"fmt"
	"github.com/woozymasta/bcn"
	"maryskelter-unburger/internal/mskit"
)

type bc7Candidate struct {
	encoded []byte
	a, v    int
	label   string
}

func measure(want [64]byte, enc []byte, label string) (bc7Candidate, error) {
	var cand bc7Candidate
	px, _, err := mskit.DecodeBC7Block(enc)
	if err != nil {
		return cand, err
	}
	a, v := visibleDiff(want, px)
	return bc7Candidate{append([]byte(nil), enc...), a, v, label}, nil
}
func tryEncode(want, input [64]byte, quality int, label string) (bc7Candidate, error) {
	enc, err := bcn.EncodeBC7WithOptions(input[:], 4, 4, &bcn.EncodeOptions{QualityLevel: quality, Workers: 1})
	if err != nil {
		return bc7Candidate{}, err
	}
	return measure(want, enc, fmt.Sprintf("%s/q%d", label, quality))
}
func clamp127(n int) byte {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return byte(n)
}
func bc7FallbackTrial127(block [64]byte, maxA, maxV int) (bc7Candidate, int, error) {
	q := [...]int{6, 8}
	best, err := tryEncode(block, block, q[0], "original")
	if err != nil {
		return best, 0, err
	}
	tried := 1
	if best.a <= maxA && best.v <= maxV {
		return best, tried, nil
	}
	// On fails, test other native BC7 modes/seed refinement as candidate only.
	for _, quality := range []int{1, 4, 7, 9, 10} {
		cand, e := tryEncode(block, block, quality, "original")
		if e != nil {
			return best, tried, e
		}
		tried++
		if better127(cand, best, maxA, maxV) {
			best = cand
		}
		if best.a <= maxA && best.v <= maxV {
			return best, tried, nil
		}
	}
	maxAlpha := -1
	var maxRGB [3]byte
	for i := 0; i < 64; i += 4 {
		if int(block[i+3]) > maxAlpha {
			maxAlpha = int(block[i+3])
			copy(maxRGB[:], block[i:i+3])
		}
	}
	for _, kind := range []string{"zero", "white", "nearest", "blend", "a+16", "a-16", "a+32", "a-32", "a*0.75", "a*1.25", "opaqueRGB+0", "opaqueRGB+64", "alphaedge0", "alphaedge255"} {
		candidate := block
		for i := 0; i < 64; i += 4 {
			a := int(block[i+3])
			switch kind {
			case "zero":
				if a < 128 {
					for c := 0; c < 3; c++ {
						candidate[i+c] = 0
					}
				}
			case "white":
				if a < 128 {
					for c := 0; c < 3; c++ {
						candidate[i+c] = 255
					}
				}
			case "nearest":
				if a < 128 {
					copy(candidate[i:i+3], maxRGB[:])
				}
			case "blend":
				if a < 192 {
					for c := 0; c < 3; c++ {
						candidate[i+c] = byte((int(candidate[i+c]) + int(maxRGB[c])) / 2)
					}
				}
			case "a+16":
				candidate[i+3] = clamp127(a + 16)
			case "a-16":
				candidate[i+3] = clamp127(a - 16)
			case "a+32":
				candidate[i+3] = clamp127(a + 32)
			case "a-32":
				candidate[i+3] = clamp127(a - 32)
			case "a*0.75":
				candidate[i+3] = clamp127(a * 3 / 4)
			case "a*1.25":
				candidate[i+3] = clamp127(a * 5 / 4)
			case "opaqueRGB+0":
				if a < 64 {
					copy(candidate[i:i+3], maxRGB[:])
				}
			case "opaqueRGB+64":
				if a < 64 {
					copy(candidate[i:i+3], maxRGB[:])
					candidate[i+3] = clamp127(a + 64)
				}
			case "alphaedge0":
				if a < 64 {
					candidate[i+3] = 0
				}
			case "alphaedge255":
				if a > 192 {
					candidate[i+3] = 255
				}
			}
		}
		for _, quality := range q {
			cand, e := tryEncode(block, candidate, quality, kind)
			if e != nil {
				return best, tried, e
			}
			tried++
			if better127(cand, best, maxA, maxV) {
				best = cand
			}
			if best.a <= maxA && best.v <= maxV {
				return best, tried, nil
			}
		}
	}
	return best, tried, nil
}
func better127(a, b bc7Candidate, limitA, limitV int) bool {
	// Prioritize guard distance; only candidate whose independent decode honors both limits is acceptable.
	da := a.a - limitA
	if da < 0 {
		da = 0
	}
	dv := a.v - limitV
	if dv < 0 {
		dv = 0
	}
	ea := b.a - limitA
	if ea < 0 {
		ea = 0
	}
	ev := b.v - limitV
	if ev < 0 {
		ev = 0
	}
	v1 := da
	if dv > v1 {
		v1 = dv
	}
	v2 := ea
	if ev > v2 {
		v2 = ev
	}
	if v1 != v2 {
		return v1 < v2
	}
	return a.a+a.v < b.a+b.v
}

// Deterministic error-feedback search: never accept a change to target RGBA;
// seed perturbations only steer the encoder and are evaluated against the original target.
func bc7StrongTrial127(want [64]byte, best bc7Candidate, limitA, limitV int) (bc7Candidate, int, error) {
	tries := 0
	state := uint32(0x12a7fd91)
	randNext := func() uint32 { state ^= state << 13; state ^= state >> 17; state ^= state << 5; return state }
	var dec [64]byte
	for step := 0; step < 640; step++ {
		input := want
		if step%7 == 0 { // refresh feedback against the latest winning independently decoded block
			newpx, _, err := mskit.DecodeBC7Block(best.encoded)
			if err != nil {
				return best, tries, err
			}
			dec = newpx
		}
		gain := []int{1, 2, 3, 4, -1, -2, 6, 8}[step%8]
		amplitude := []int{4, 8, 12, 16, 24, 32, 48, 64}[step%8]
		for i := 0; i < 64; i += 4 {
			a := int(want[i+3])
			for c := 0; c < 4; c++ {
				if step < 160 { // residual-error feedback from true target to steer endpoint LSQ
					if step%4 == 0 && c != 3 {
						continue
					}
					if step%4 == 1 && c == 3 {
						continue
					}
					delta := int(want[i+c]) - int(dec[i+c])
					input[i+c] = clamp127(int(want[i+c]) + delta*gain/2)
				} else if step < 320 { // coherent per-block channel shift
					if c == (step/8)%4 {
						input[i+c] = clamp127(int(want[i+c]) + gain*amplitude/4)
					}
				} else if step < 480 { // alpha-sensitive smooth deterministic jitter
					r := int(randNext()%uint32(amplitude*2+1)) - amplitude
					if c == 3 || a < 192 {
						input[i+c] = clamp127(int(want[i+c]) + r)
					}
				} else { // single-texel / single-channel targeted kick
					k := (step * 37 / 3) % 64
					k -= k % 4
					if i == k && c == (step/7)%4 {
						input[i+c] = clamp127(int(want[i+c]) + gain*amplitude/2)
					}
				}
			}
		}
		quality := 6
		if step%9 == 0 {
			quality = 8
		} else if step%11 == 0 {
			quality = 1
		}
		cand, err := tryEncode(want, input, quality, "strong")
		if err != nil {
			return best, tries, err
		}
		tries++
		if better127(cand, best, limitA, limitV) {
			best = cand
		}
		if best.a <= limitA && best.v <= limitV {
			return best, tries, nil
		}
	}
	return best, tries, nil
}

// BC7-legal local bit neighborhood; every candidate independently decoded and
// judged ONLY against the unchanged original RGBA pixels.
func bc7EncodedNeighborhood127(want [64]byte, best bc7Candidate, limA, limV int) (bc7Candidate, int, error) {
	trials := 0
	for pass := 0; pass < 3; pass++ {
		improved := false
		scan := func(i, j int) bool {
			enc := append([]byte(nil), best.encoded...)
			enc[i/8] ^= 1 << uint(i%8)
			if j >= 0 {
				enc[j/8] ^= 1 << uint(j%8)
			}
			cand, e := measure(want, enc, "bit-neighborhood")
			trials++
			if e != nil {
				return false
			}
			if better127(cand, best, limA, limV) {
				best = cand
				improved = true
			}
			return best.a <= limA && best.v <= limV
		}
		for i := 8; i < 128; i++ {
			if scan(i, -1) {
				return best, trials, nil
			}
		}
		if improved {
			continue
		}
		for i := 8; i < 128; i++ {
			for j := i + 1; j < 128; j++ {
				if scan(i, j) {
					return best, trials, nil
				}
			}
		}
		if !improved {
			break
		}
	}
	return best, trials, nil
}

func MinimaxBC7(target [64]byte, original []byte, maxA, maxV int) (bc7Candidate, int, error) {
	best, err := measure(target, original, "production-Q6")
	if err != nil {
		return best, 0, err
	}
	if best.a <= maxA && best.v <= maxV {
		return best, 0, nil
	}
	alt, n, err := bc7FallbackTrial127(target, maxA, maxV)
	if err != nil {
		return best, n, err
	}
	if better127(alt, best, maxA, maxV) {
		best = alt
	}
	if best.a <= maxA && best.v <= maxV {
		return best, n, nil
	}
	alt, n2, err := bc7StrongTrial127(target, best, maxA, maxV)
	n += n2
	if err != nil {
		return best, n, err
	}
	if better127(alt, best, maxA, maxV) {
		best = alt
	}
	if best.a <= maxA && best.v <= maxV {
		return best, n, nil
	}
	alt, n2, err = bc7EncodedNeighborhood127(target, best, maxA, maxV)
	n += n2
	if err != nil {
		return best, n, err
	}
	if better127(alt, best, maxA, maxV) {
		best = alt
	}
	return best, n, nil
}
