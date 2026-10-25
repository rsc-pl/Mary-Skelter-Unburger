package mskit

// BCNTrialStats describes a deterministic, byte-level comparison of two
// decoders. It never authorizes switching the production graphics codec.
type BCNTrialStats struct {
	Pixels                    uint64 `json:"pixels"`
	ExactPixels               uint64 `json:"exact_rgba_pixels"`
	AlphaDifferentPixels      uint64 `json:"alpha_different_pixels"`
	VisibleDifferentPixels    uint64 `json:"visible_different_pixels"`
	HiddenRGBDifferentPixels  uint64 `json:"hidden_rgb_different_pixels"`
	MaxAlphaDifference        uint8  `json:"max_alpha_difference"`
	MaxCompositeDifference    uint8  `json:"max_black_or_white_composite_difference"`
	CompositeDifferentPixels  uint64 `json:"black_or_white_composite_different_pixels"`
	SumAlphaAbsDifference     uint64 `json:"sum_alpha_absolute_difference"`
	SumCompositeAbsDifference uint64 `json:"sum_black_and_white_composite_absolute_difference"`
}

func absBCNByte(a, b byte) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

// CompareBCNTrialRGBA compares straight-alpha RGBA, including invisible RGB,
// alpha, and color composited over both black and white backgrounds.
// The composite calculation uses integer rounding, not a lossy threshold.
func CompareBCNTrialRGBA(reference, candidate []byte) (BCNTrialStats, bool) {
	var s BCNTrialStats
	if len(reference) == 0 || len(reference)%4 != 0 || len(reference) != len(candidate) {
		return s, false
	}
	s.Pixels = uint64(len(reference) / 4)
	for p := 0; p < len(reference); p += 4 {
		allSame, rgbSame := true, true
		for c := 0; c < 4; c++ {
			if reference[p+c] != candidate[p+c] {
				allSame = false
				if c < 3 {
					rgbSame = false
				}
			}
		}
		if allSame {
			s.ExactPixels++
		}
		a, b := reference[p+3], candidate[p+3]
		if a != b {
			s.AlphaDifferentPixels++
		}
		s.SumAlphaAbsDifference += uint64(absBCNByte(a, b))
		if d := absBCNByte(a, b); d > s.MaxAlphaDifference {
			s.MaxAlphaDifference = d
		}
		if !rgbSame && a == 0 && b == 0 {
			s.HiddenRGBDifferentPixels++
		}
		if a != b || (!rgbSame && (a != 0 || b != 0)) {
			s.VisibleDifferentPixels++
		}
		compositeChanged := false
		for c := 0; c < 3; c++ {
			for _, bg := range [...]int{0, 255} {
				v1 := (int(reference[p+c])*int(a) + bg*(255-int(a)) + 127) / 255
				v2 := (int(candidate[p+c])*int(b) + bg*(255-int(b)) + 127) / 255
				d := absBCNByte(byte(v1), byte(v2))
				s.SumCompositeAbsDifference += uint64(d)
				if d > 0 {
					compositeChanged = true
				}
				if d > s.MaxCompositeDifference {
					s.MaxCompositeDifference = d
				}
			}
		}
		if compositeChanged {
			s.CompositeDifferentPixels++
		}
	}
	return s, true
}

// BCNTouchedBlocks reports the exact block count changed while replacing only
// original 4x4 blocks, never the TID header or unrelated compressed blocks.
func BCNTouchedBlocks(source, replacement []byte, width, height int) (changed int, preserved int, ok bool) {
	if width <= 0 || height <= 0 || width%4 != 0 || height%4 != 0 {
		return 0, 0, false
	}
	n := width / 4 * (height / 4)
	if n <= 0 || len(source) != n*16 || len(source) != len(replacement) {
		return 0, 0, false
	}
	for i := 0; i < len(source); i += 16 {
		equal := true
		for j := 0; j < 16; j++ {
			if source[i+j] != replacement[i+j] {
				equal = false
				break
			}
		}
		if equal {
			preserved++
		} else {
			changed++
		}
	}
	return changed, preserved, true
}
