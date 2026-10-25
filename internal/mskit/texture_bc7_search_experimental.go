package mskit

// EXPERIMENTAL, NOT USED BY THE PRODUCTION GRAPHICS PIPELINE.
//
// This is a deterministic integer-error, fixed-layout BC7 endpoint/index
// coordinate optimizer. It deliberately separates three problems:
//  (1) valid bitstream packing (), (2) endpoint/index optimization (here),
//  (3) matching the pinned C bc7enc's partition/mode decisions and exact bytes
//      (NOT YET PROVEN). No caller may substitute this for bc7enc on the strength
//      of an SSE comparison: BC7 has many different bitstreams per visual result.
//
// The function accepts a mode/partition/rotation/index-selector layout, not a
// C-derived answer. No cgo, files, global state or third-party dependencies.

import (
	"fmt"
	"math"
)

// BC7SearchLayout defines the fixed bitstream choices for a BC7 candidate.
// It does not choose a partition or a mode for the caller.
type BC7SearchLayout struct {
	Mode, Partition, Rotation, IndexMode int
}

type bc7SearchSpec struct {
	subsets, components, colorBits, alphaBits int
	endpointColorBits, endpointAlphaBits      int
	pbits                                     int // 0 none, 1 shared per subset, 2 per endpoint
}

func bc7Spec(l BC7SearchLayout) (bc7SearchSpec, error) {
	s := bc7SearchSpec{}
	switch l.Mode {
	case 0:
		s = bc7SearchSpec{3, 3, 3, 0, 4, 0, 2}
	case 1:
		s = bc7SearchSpec{2, 3, 3, 0, 6, 0, 1}
	case 2:
		s = bc7SearchSpec{3, 3, 2, 0, 5, 0, 0}
	case 3:
		s = bc7SearchSpec{2, 3, 2, 0, 7, 0, 2}
	case 4:
		s = bc7SearchSpec{1, 4, 2, 3, 5, 6, 0}
		if l.IndexMode == 1 {
			s.colorBits, s.alphaBits = 3, 2
		}
	case 5:
		s = bc7SearchSpec{1, 4, 2, 2, 7, 8, 0}
	case 6:
		s = bc7SearchSpec{1, 4, 4, 0, 7, 7, 2}
	case 7:
		s = bc7SearchSpec{2, 4, 2, 0, 5, 5, 2}
	default:
		return s, fmt.Errorf("BC7 experimental search invalid mode %d", l.Mode)
	}
	maxPart := 1
	if l.Mode == 0 {
		maxPart = 16
	}
	if l.Mode == 2 || l.Mode == 1 || l.Mode == 3 || l.Mode == 7 {
		maxPart = 64
	}
	if l.Partition < 0 || l.Partition >= maxPart {
		return s, fmt.Errorf("BC7 experimental invalid partition %d for mode %d", l.Partition, l.Mode)
	}
	if l.Rotation < 0 || l.Rotation > 3 || (l.Mode != 4 && l.Mode != 5 && l.Rotation != 0) {
		return s, fmt.Errorf("BC7 experimental invalid rotation %d", l.Rotation)
	}
	if l.IndexMode < 0 || l.IndexMode > 1 || (l.Mode != 4 && l.IndexMode != 0) {
		return s, fmt.Errorf("BC7 experimental invalid index selector %d", l.IndexMode)
	}
	return s, nil
}

func bc7Subset(l BC7SearchLayout, pixel int) int {
	switch l.Mode {
	case 0, 2:
		return int(bc7Partitions3[l.Partition][pixel])
	case 1, 3, 7:
		return int(bc7Partitions2[l.Partition][pixel])
	default:
		return 0
	}
}

func bc7Anchor(l BC7SearchLayout, i int) bool {
	if i == 0 {
		return true
	}
	switch l.Mode {
	case 0, 2:
		return i == bc7Anchor3a[l.Partition] || i == bc7Anchor3b[l.Partition]
	case 1, 3, 7:
		return i == bc7Anchor2[l.Partition]
	default:
		return false
	}
}

func bc7TargetRotated(target [64]byte, rotation int) [64]byte {
	if rotation > 0 {
		for i := 0; i < 16; i++ {
			j := 4 * i
			target[j+rotation-1], target[j+3] = target[j+3], target[j+rotation-1]
		}
	}
	return target
}

func bc7PbitIndex(s bc7SearchSpec, e int) int {
	if s.pbits == 1 {
		return e / 2
	}
	return e
}
func bc7QuantBits(s bc7SearchSpec, c int) int {
	if c == 3 {
		return s.endpointAlphaBits
	}
	return s.endpointColorBits
}
func bc7ExpandedEndpoint(f BC7BlockFields, s bc7SearchSpec, e, c int) int {
	bits := bc7QuantBits(s, c)
	if s.pbits > 0 {
		return bc7ExpandP(int(f.Endpoints[e][c]), int(f.PBits[bc7PbitIndex(s, e)]), bits)
	}
	return bc7Expand(int(f.Endpoints[e][c]), bits)
}
func bc7ChannelIndex(f BC7BlockFields, s bc7SearchSpec, i, c int) (int, int) {
	if c == 3 && s.alphaBits > 0 {
		return int(f.AlphaIndices[i]), s.alphaBits
	}
	return int(f.ColorIndices[i]), s.colorBits
}
func bc7ComponentSSE(f BC7BlockFields, s bc7SearchSpec, l BC7SearchLayout, target [64]byte, subset, c int) uint64 {
	var e uint64
	lo := bc7ExpandedEndpoint(f, s, 2*subset, c)
	hi := bc7ExpandedEndpoint(f, s, 2*subset+1, c)
	for i := 0; i < 16; i++ {
		if bc7Subset(l, i) != subset {
			continue
		}
		idx, bits := bc7ChannelIndex(f, s, i, c)
		d := int(target[4*i+c]) - int(bc7Interpolate(lo, hi, idx, bits))
		e += uint64(d * d)
	}
	return e
}
func bc7FieldsSSE(f BC7BlockFields, s bc7SearchSpec, l BC7SearchLayout, target [64]byte) uint64 {
	var e uint64
	for subset := 0; subset < s.subsets; subset++ {
		for c := 0; c < s.components; c++ {
			e += bc7ComponentSSE(f, s, l, target, subset, c)
		}
	}
	if s.components == 3 {
		for i := 0; i < 16; i++ {
			d := int(target[4*i+3]) - 255
			e += uint64(d * d)
		}
	}
	return e
}

// bc7WeightedFieldsSSE is a research-only objective. It measures decoded
// channels exactly, but gives alpha errors a configurable penalty for glyph
// edge fidelity. This is NOT a proxy for the pinned C bc7enc's private search.
func bc7WeightedFieldsSSE(f BC7BlockFields, s bc7SearchSpec, l BC7SearchLayout, target [64]byte, alphaWeight uint64) uint64 {
	var score uint64
	for subset := 0; subset < s.subsets; subset++ {
		for c := 0; c < s.components; c++ {
			w := uint64(1)
			if c == 3 {
				w = alphaWeight
			}
			score += w * bc7ComponentSSE(f, s, l, target, subset, c)
		}
	}
	if s.components == 3 {
		for i := 0; i < 16; i++ {
			d := int(target[4*i+3]) - 255
			score += alphaWeight * uint64(d*d)
		}
	}
	return score
}

func bc7ClosestIndices(f *BC7BlockFields, s bc7SearchSpec, l BC7SearchLayout, target [64]byte, alphaWeight uint64) {
	for i := 0; i < 16; i++ {
		subset := bc7Subset(l, i)
		lo, hi := 2*subset, 2*subset+1
		// Independent RGB and A index planes are possible in modes 4/5.
		planes := 1
		if s.alphaBits > 0 {
			planes = 2
		}
		for plane := 0; plane < planes; plane++ {
			bits := s.colorBits
			if plane == 1 {
				bits = s.alphaBits
			}
			maxIdx := (1 << uint(bits)) - 1
			if bc7Anchor(l, i) {
				maxIdx = (1 << uint(bits-1)) - 1
			}
			bestIdx, bestErr := 0, uint64(math.MaxUint64)
			for idx := 0; idx <= maxIdx; idx++ {
				var e uint64
				for c := 0; c < s.components; c++ {
					if plane == 0 && s.alphaBits > 0 && c == 3 {
						continue
					}
					if plane == 1 && c != 3 {
						continue
					}
					dec := int(bc7Interpolate(bc7ExpandedEndpoint(*f, s, lo, c), bc7ExpandedEndpoint(*f, s, hi, c), idx, bits))
					d := int(target[4*i+c]) - dec
					if c == 3 {
						e += alphaWeight * uint64(d*d)
					} else {
						e += uint64(d * d)
					}
				}
				if e < bestErr {
					bestIdx, bestErr = idx, e
				}
			}
			if plane == 0 {
				f.ColorIndices[i] = uint8(bestIdx)
			} else {
				f.AlphaIndices[i] = uint8(bestIdx)
			}
		}
	}
}

// bc7QuantizeEndpoints optimizes every raw quantized endpoint against the
// *decoded, integer-rounded* interpolation, not against an ideal float color.
// Shared pbits are optimized jointly per subset, preserving mode-1 semantics.
func bc7QuantizeEndpoints(f *BC7BlockFields, s bc7SearchSpec, l BC7SearchLayout, target [64]byte, alphaWeight uint64) {
	for subset := 0; subset < s.subsets; subset++ {
		pbitTrials := 1
		if s.pbits == 1 {
			pbitTrials = 2 // mode 1: one shared pbit for both endpoints
		} else if s.pbits == 2 {
			pbitTrials = 4 // other pbit modes: independent endpoint pbits
		}
		var best BC7BlockFields
		bestScore := uint64(math.MaxUint64)
		for p := 0; p < pbitTrials; p++ {
			trial := *f
			if s.pbits == 1 {
				trial.PBits[subset] = uint8(p)
			}
			for end := 2 * subset; end < 2*subset+2; end++ {
				if s.pbits == 2 {
					trial.PBits[end] = uint8((p >> uint(end-2*subset)) & 1)
				}
				for c := 0; c < s.components; c++ {
					bits := bc7QuantBits(s, c)
					bestQ := int(trial.Endpoints[end][c])
					minErr := uint64(math.MaxUint64)
					for q := 0; q < 1<<uint(bits); q++ {
						trial.Endpoints[end][c] = uint8(q)
						e := bc7ComponentSSE(trial, s, l, target, subset, c)
						if e < minErr {
							minErr, bestQ = e, q
						}
					}
					trial.Endpoints[end][c] = uint8(bestQ)
				}
			}
			var score uint64
			for c := 0; c < s.components; c++ {
				w := uint64(1)
				if c == 3 {
					w = alphaWeight
				}
				score += w * bc7ComponentSSE(trial, s, l, target, subset, c)
			}
			if score < bestScore {
				bestScore, best = score, trial
			}
		}
		for e := 2 * subset; e < 2*subset+2; e++ {
			for c := 0; c < s.components; c++ {
				f.Endpoints[e][c] = best.Endpoints[e][c]
			}
			if s.pbits == 2 {
				f.PBits[e] = best.PBits[e]
			}
		}
		if s.pbits == 1 {
			f.PBits[subset] = best.PBits[subset]
		}
	}
}

func bc7SeedFields(target [64]byte, s bc7SearchSpec, l BC7SearchLayout, reverse bool) BC7BlockFields {
	f := BC7BlockFields{Mode: l.Mode, Partition: l.Partition, Rotation: l.Rotation, IndexMode: l.IndexMode}
	for subset := 0; subset < s.subsets; subset++ {
		for c := 0; c < s.components; c++ {
			mn, mx := 255, 0
			for i := 0; i < 16; i++ {
				if bc7Subset(l, i) != subset {
					continue
				}
				v := int(target[4*i+c])
				if v < mn {
					mn = v
				}
				if v > mx {
					mx = v
				}
			}
			if reverse {
				mn, mx = mx, mn
			}
			for end, v := range []int{mn, mx} {
				e := 2*subset + end
				bits := bc7QuantBits(s, c)
				// Quantized endpoint bits always exclude the separately stored pbit.
				// E.g. BC7 mode 6 endpoint=7 bits + 1 pbit: v/2, not v/4.
				f.Endpoints[e][c] = uint8(v >> uint(8-bits))
			}
		}
	}
	return f
}

// RefineBC7FixedLayout optimizes indices and endpoints for a *fixed* mode,
// partition, rotation and index selector. Every result is a valid 128-bit BC7
// block; the SSE is computed on the exact integer decoder output. A zero SSE
// means pixel equivalence, NOT C byte parity.
func RefineBC7FixedLayout(target [64]byte, layout BC7SearchLayout, iterations int) ([16]byte, uint64, error) {
	return RefineBC7FixedLayoutAlphaWeightedExperimental(target, layout, iterations, 1)
}

// RefineBC7FixedLayoutAlphaWeightedExperimental varies the *optimization*
// objective without changing the BC7 bitstream format. It returns unweighted
// RGBA SSE for honest comparison with the preexisting API. Any alpha
// improvement must be proven across real edited textures before promotion.
// A weight of 1 preserves the exact search and tie-breaking.
func RefineBC7FixedLayoutAlphaWeightedExperimental(target [64]byte, layout BC7SearchLayout, iterations int, alphaWeight int) ([16]byte, uint64, error) {
	var empty [16]byte
	s, err := bc7Spec(layout)
	if err != nil {
		return empty, 0, err
	}
	if iterations < 1 || iterations > 32 {
		return empty, 0, fmt.Errorf("BC7 experimental invalid iterations %d", iterations)
	}
	if alphaWeight < 1 || alphaWeight > 32 {
		return empty, 0, fmt.Errorf("BC7 alpha weight outside 1..32: %d", alphaWeight)
	}
	rotated := bc7TargetRotated(target, layout.Rotation)
	var best BC7BlockFields
	bestObjective := uint64(math.MaxUint64)
	w := uint64(alphaWeight)
	for _, reverse := range []bool{false, true} {
		f := bc7SeedFields(rotated, s, layout, reverse)
		for iter := 0; iter < iterations; iter++ {
			bc7ClosestIndices(&f, s, layout, rotated, w)
			bc7QuantizeEndpoints(&f, s, layout, rotated, w)
			bc7ClosestIndices(&f, s, layout, rotated, w)
			objective := bc7WeightedFieldsSSE(f, s, layout, rotated, w)
			if objective < bestObjective {
				best, bestObjective = f, objective
			}
		}
	}
	out, err := PackBC7Block(best)
	if err != nil {
		return empty, 0, err
	}
	decoded, _, err := DecodeBC7Block(out[:])
	if err != nil {
		return empty, 0, err
	}
	var check uint64
	for i := 0; i < 64; i++ {
		d := int(target[i]) - int(decoded[i])
		check += uint64(d * d)
	}
	if check != bc7FieldsSSE(best, s, layout, rotated) {
		return empty, 0, fmt.Errorf("BC7 optimizer internal scoring mismatch with decoded block")
	}
	if bc7WeightedFieldsSSE(best, s, layout, rotated, w) != bestObjective {
		return empty, 0, fmt.Errorf("BC7 optimizer weighted score mismatch")
	}
	return out, check, nil
}

// SearchBC7AlphaWeightedExperimental searches supplied modes using an
// alpha-weighted objective (rather than raw RGBA SSE). The returned SSE remains
// raw RGBA SSE. A weight of 1 matches SearchBC7Experimental exactly.
func SearchBC7AlphaWeightedExperimental(target [64]byte, layouts []BC7SearchLayout, iterations int, alphaWeight int) ([16]byte, BC7SearchLayout, uint64, error) {
	var out [16]byte
	if len(layouts) < 1 || len(layouts) > 512 {
		return out, BC7SearchLayout{}, 0, fmt.Errorf("BC7 experimental layout count out of range")
	}
	if alphaWeight < 1 || alphaWeight > 32 {
		return out, BC7SearchLayout{}, 0, fmt.Errorf("BC7 alpha weight outside 1..32: %d", alphaWeight)
	}
	var winner BC7SearchLayout
	minObjective := uint64(math.MaxUint64)
	var rawSSE uint64
	for _, layout := range layouts {
		block, score, err := RefineBC7FixedLayoutAlphaWeightedExperimental(target, layout, iterations, alphaWeight)
		if err != nil {
			return out, winner, 0, err
		}
		// Independently score the decoded RGBA, so the mode choice is
		// governed by the *same* weighted objective as endpoint fitting.
		pixels, _, err := DecodeBC7Block(block[:])
		if err != nil {
			return out, winner, 0, err
		}
		var objective uint64
		for i := 0; i < 64; i++ {
			d := int(target[i]) - int(pixels[i])
			w := uint64(1)
			if i%4 == 3 {
				w = uint64(alphaWeight)
			}
			objective += w * uint64(d*d)
		}
		if objective < minObjective {
			out, winner, minObjective, rawSSE = block, layout, objective, score
		}
	}
	return out, winner, rawSSE, nil
}

// SearchBC7Experimental searches only explicitly supplied layouts and never
// influences production. Callers must separately demand source-pinned bc7enc
// byte parity before using its output in a game patch.
func SearchBC7Experimental(target [64]byte, layouts []BC7SearchLayout, iterations int) ([16]byte, BC7SearchLayout, uint64, error) {
	var out [16]byte
	if len(layouts) < 1 || len(layouts) > 512 {
		return out, BC7SearchLayout{}, 0, fmt.Errorf("BC7 experimental layout count out of range")
	}
	var winner BC7SearchLayout
	minErr := uint64(math.MaxUint64)
	for _, layout := range layouts {
		block, score, err := RefineBC7FixedLayout(target, layout, iterations)
		if err != nil {
			return out, winner, 0, err
		}
		if score < minErr {
			out, winner, minErr = block, layout, score
		}
	}
	return out, winner, minErr, nil
}

// bc7EdgeCompositeObjective measures the appearance of a block over black
// and white backgrounds, plus a separate alpha SSE penalty. It is used only
// to compare independently generated *valid* BC7 blocks, never to override
// or modify a pristine source block. Fixed 4x4 RGBA; integer-rounded compositing.
func bc7EdgeCompositeObjective(source [64]byte, block [16]byte) (uint64, error) {
	decoded, _, err := DecodeBC7Block(block[:])
	if err != nil {
		return 0, err
	}
	var objective uint64
	for i := 0; i < 16; i++ {
		j := i * 4
		sa, da := int(source[j+3]), int(decoded[j+3])
		alphaDiff := sa - da
		objective += 2 * uint64(alphaDiff*alphaDiff)
		for c := 0; c < 3; c++ {
			sb := (int(source[j+c])*sa + 127) / 255
			db := (int(decoded[j+c])*da + 127) / 255
			black := sb - db
			white := (sb + 255 - sa) - (db + 255 - da)
			objective += uint64(black*black + white*white)
		}
	}
	return objective, nil
}

// SearchBC7EdgePreservingExperimental is a pure Go *research* policy that
// compares the unweighted optimizer with an alpha-aware search (weight 4).
// The winner minimizes a separately decoded black/white/alpha edge objective;
// no C bytes, named fixture hashes, or production graphics paths enter the
// selection. It is deliberately NOT the shipped BC7 encoder.
func SearchBC7EdgePreservingExperimental(target [64]byte, layouts []BC7SearchLayout, iterations int) ([16]byte, BC7SearchLayout, uint64, error) {
	legacy, l0, s0, err := SearchBC7Experimental(target, layouts, iterations)
	if err != nil {
		return legacy, l0, s0, err
	}
	weighted, l1, s1, err := SearchBC7AlphaWeightedExperimental(target, layouts, iterations, 4)
	if err != nil {
		return legacy, l0, s0, err
	}
	q0, err := bc7EdgeCompositeObjective(target, legacy)
	if err != nil {
		return legacy, l0, s0, err
	}
	q1, err := bc7EdgeCompositeObjective(target, weighted)
	if err != nil {
		return legacy, l0, s0, err
	}
	if q1 < q0 {
		return weighted, l1, s1, nil
	}
	return legacy, l0, s0, nil // deterministic tie prefers established algorithm
}
