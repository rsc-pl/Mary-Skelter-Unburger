package mskit

// Research-only local decoded-composite improvement of a previously encoded BC7
// block. Unlike RGBA-only endpoint quantization, every index and endpoint trial
// is evaluated after BC7 integer expansion and black/white alpha compositing.
// This is not a production pixel encoder and does not read reference-C data.

import "fmt"

// ImproveBC7CompositeExperimental greedily improves a valid starting block
// without changing its mode, partition, rotation or index selector. The input
// block remains the chosen result on all ties; no step may worsen the black /
// white plus 2x alpha squared-error objective. passes bounds search cost.
func ImproveBC7CompositeExperimental(target [64]byte, start [16]byte, passes int) ([16]byte, uint64, error) {
	if passes < 1 || passes > 8 {
		return start, 0, fmt.Errorf("BC7 composited pass count outside 1..8")
	}
	fields, err := UnpackBC7Block(start[:])
	if err != nil {
		return start, 0, err
	}
	layout := BC7SearchLayout{Mode: fields.Mode, Partition: fields.Partition, Rotation: fields.Rotation, IndexMode: fields.IndexMode}
	spec, err := bc7Spec(layout)
	if err != nil {
		return start, 0, err
	}
	best, err := bc7EdgeCompositeObjective(target, start)
	if err != nil {
		return start, 0, err
	}
	current := start
	try := func(f BC7BlockFields) bool {
		b, e := PackBC7Block(f)
		if e != nil {
			return false
		}
		q, e := bc7EdgeCompositeObjective(target, b)
		if e != nil || q >= best {
			return false
		}
		fields, current, best = f, b, q
		return true
	}
	for pass := 0; pass < passes; pass++ {
		old := best
		// Optimize both index planes, where present, against *composited* RGBA.
		// Revisit after endpoint updates because their best indices may differ.
		for i := 0; i < 16; i++ {
			colorMax := (1 << uint(spec.colorBits)) - 1
			if bc7Anchor(layout, i) {
				colorMax = (1 << uint(spec.colorBits-1)) - 1
			}
			for index := 0; index <= colorMax; index++ {
				f := fields
				f.ColorIndices[i] = uint8(index)
				try(f)
			}
			if spec.alphaBits > 0 {
				alphaMax := (1 << uint(spec.alphaBits)) - 1
				if bc7Anchor(layout, i) {
					alphaMax = (1 << uint(spec.alphaBits-1)) - 1
				}
				for index := 0; index <= alphaMax; index++ {
					f := fields
					f.AlphaIndices[i] = uint8(index)
					try(f)
				}
			}
		}
		// Quantized endpoint steps permit escaping a poor RGB seed, especially
		// with alpha~0 where unpremultiplied RGB error is misleading.
		for _, step := range []int{16, 8, 4, 2, 1} {
			for endpoint := 0; endpoint < spec.subsets*2; endpoint++ {
				for channel := 0; channel < spec.components; channel++ {
					limit := (1 << uint(bc7QuantBits(spec, channel))) - 1
					for _, delta := range []int{-step, step} {
						q := int(fields.Endpoints[endpoint][channel]) + delta
						if q < 0 || q > limit {
							continue
						}
						f := fields
						f.Endpoints[endpoint][channel] = uint8(q)
						try(f)
					}
				}
			}
		}
		pCount := 0
		if spec.pbits == 1 {
			pCount = spec.subsets
		}
		if spec.pbits == 2 {
			pCount = spec.subsets * 2
		}
		for pi := 0; pi < pCount; pi++ {
			f := fields
			f.PBits[pi] ^= 1
			try(f)
		}
		if best == old {
			break
		}
	}
	if recomputed, err := bc7EdgeCompositeObjective(target, current); err != nil || recomputed != best {
		return start, 0, fmt.Errorf("BC7 composite local refinement verification failed: %v", err)
	}
	return current, best, nil
}
