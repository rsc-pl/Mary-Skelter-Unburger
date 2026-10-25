package mskit

// RESEARCH ONLY. Mode/partition beam and alpha-composited farthest-pair
// endpoints are independent of reference-C output. They do not modify the
// production graphics pipeline. All ranking uses actually decoded BC7 pixels.

import (
	"fmt"
	"sort"
)

type bc7RestartCandidate struct {
	block [16]byte
	score uint64
	mode  int
}

// Pixel-pair selection uses ORIGINAL alpha/RGB even for BC7 rotation modes;
// endpoint quantization uses the rotated storage-domain channels.
func bc7PremultipliedSeed(original [64]byte, rotated [64]byte, s bc7SearchSpec, l BC7SearchLayout, reverse bool) BC7BlockFields {
	f := BC7BlockFields{Mode: l.Mode, Partition: l.Partition, Rotation: l.Rotation, IndexMode: l.IndexMode}
	for subset := 0; subset < s.subsets; subset++ {
		bestD := -1
		first, last := -1, -1
		for i := 0; i < 16; i++ {
			if bc7Subset(l, i) != subset {
				continue
			}
			for j := i + 1; j < 16; j++ {
				if bc7Subset(l, j) != subset {
					continue
				}
				ai, aj := int(original[4*i+3]), int(original[4*j+3])
				d := (ai - aj) * (ai - aj) * 2
				for c := 0; c < 3; c++ {
					pi := (int(original[4*i+c])*ai + 127) / 255
					pj := (int(original[4*j+c])*aj + 127) / 255
					x := pi - pj
					d += x * x
				}
				if d > bestD {
					bestD, first, last = d, i, j
				}
			}
		}
		if first < 0 { // A valid BC7 subset must be nonempty; fail closed at caller.
			return f
		}
		if last < 0 {
			last = first
		}
		if reverse {
			first, last = last, first
		}
		for end, pixel := range [2]int{first, last} {
			other := last
			if end == 1 {
				other = first
			}
			for c := 0; c < s.components; c++ {
				v := int(rotated[4*pixel+c])
				// Transparent texels carry irrelevant RGB. Do not let arbitrary
				// hidden color dominate the initial endpoints for visible glyphs.
				if c < 3 && s.components == 4 && original[4*pixel+3] == 0 && original[4*other+3] > 0 {
					v = int(rotated[4*other+c])
				}
				bits := bc7QuantBits(s, c)
				f.Endpoints[2*subset+end][c] = uint8(v >> uint(8-bits))
			}
		}
	}
	return f
}

func bc7OptimizeSeed(target [64]byte, layout BC7SearchLayout, s bc7SearchSpec, seed BC7BlockFields, iterations int) ([16]byte, uint64, error) {
	var empty [16]byte
	rotated := bc7TargetRotated(target, layout.Rotation)
	f := seed
	bestScore := ^uint64(0)
	best := f
	for it := 0; it < iterations; it++ {
		bc7ClosestIndices(&f, s, layout, rotated, 4)
		bc7QuantizeEndpoints(&f, s, layout, rotated, 4)
		bc7ClosestIndices(&f, s, layout, rotated, 4)
		score := bc7WeightedFieldsSSE(f, s, layout, rotated, 4)
		if score < bestScore {
			bestScore, best = score, f
		}
	}
	block, err := PackBC7Block(best)
	if err != nil {
		return empty, 0, err
	}
	score, err := bc7EdgeCompositeObjective(target, block)
	return block, score, err
}

// SearchBC7CompositeRestartExperimental evaluates both ordinary and
// premultiplied-alpha seeds with a small mode-diverse beam before greedy
// decoded-composite refinement. It strictly retains the starting result
// on ties and never accepts a worse decoded objective. Reference-C bytes and
// mode metadata are not inputs. Layouts must be explicitly supplied.
func SearchBC7CompositeRestartExperimental(target [64]byte, layouts []BC7SearchLayout, iterations, passes, perMode int) ([16]byte, uint64, error) {
	var empty [16]byte
	if len(layouts) < 1 || len(layouts) > 512 || iterations < 1 || iterations > 32 || passes < 1 || passes > 8 || perMode < 1 || perMode > 4 {
		return empty, 0, fmt.Errorf("BC7 restart experiment invalid bounds")
	}
	base, _, _, err := SearchBC7EdgePreservingExperimental(target, layouts, iterations)
	if err != nil {
		return empty, 0, err
	}
	winner, winnerQ, err := ImproveBC7CompositeExperimental(target, base, passes)
	if err != nil {
		return empty, 0, err
	}
	groups := map[int][]bc7RestartCandidate{}
	for _, layout := range layouts {
		_, e := bc7Spec(layout)
		if e != nil {
			return empty, 0, e
		}
		for _, weight := range []int{1, 4} {
			block, _, e := RefineBC7FixedLayoutAlphaWeightedExperimental(target, layout, iterations, weight)
			if e != nil {
				return empty, 0, e
			}
			score, e := bc7EdgeCompositeObjective(target, block)
			if e != nil {
				return empty, 0, e
			}
			groups[layout.Mode] = append(groups[layout.Mode], bc7RestartCandidate{block, score, layout.Mode})
		}
	}
	// Deterministic per-mode shortlist, stable on equal objective/bitstreams.
	modes := make([]int, 0, len(groups))
	for mode := range groups {
		modes = append(modes, mode)
	}
	sort.Ints(modes)
	for _, mode := range modes {
		if mode != 5 && mode != 7 {
			continue // bounded mode-5/7 experiment; preserve all other modes through baseline
		}
		candidates := groups[mode]
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score < candidates[j].score })
		unique := map[[16]byte]bool{}
		count := 0
		for _, candidate := range candidates {
			if unique[candidate.block] {
				continue
			}
			unique[candidate.block] = true
			fields, e := UnpackBC7Block(candidate.block[:])
			if e != nil {
				return empty, 0, e
			}
			layout := BC7SearchLayout{Mode: fields.Mode, Partition: fields.Partition, Rotation: fields.Rotation, IndexMode: fields.IndexMode}
			choices := [][16]byte{candidate.block}
			if layout.Mode == 5 || layout.Mode == 7 {
				s, e := bc7Spec(layout)
				if e != nil {
					return empty, 0, e
				}
				rotated := bc7TargetRotated(target, layout.Rotation)
				for _, reverse := range []bool{false, true} {
					seed := bc7PremultipliedSeed(target, rotated, s, layout, reverse)
					b, _, e := bc7OptimizeSeed(target, layout, s, seed, iterations)
					if e != nil {
						return empty, 0, e
					}
					choices = append(choices, b)
				}
			}
			for _, start := range choices {
				improved, q, e := ImproveBC7CompositeExperimental(target, start, passes)
				if e != nil {
					return empty, 0, e
				}
				if q < winnerQ {
					winner, winnerQ = improved, q
				}
			}
			count++
			if count == perMode {
				break
			}
		}
	}
	got, e := bc7EdgeCompositeObjective(target, winner)
	if e != nil || got != winnerQ {
		return empty, 0, fmt.Errorf("BC7 restart result verification mismatch: %v", e)
	}
	return winner, winnerQ, nil
}
