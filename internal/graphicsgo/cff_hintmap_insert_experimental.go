package graphicsgo

// This research-only module implements the prepositioned (already Blue-captured
// or previously locked) edge INSERTION subcase of FreeType cf2_hintmap_build.
// Input device coordinates are authoritative and are NEVER independently
// rounded. It does NOT perform Blue capture, first-use edge adjustment,
// mask evaluation, counter groups, or source-point hinting. Production uses
// the accepted Pillow renderer, not this experiment.

import "math"

// CFFPrepositionedHintExperimental is one prepared horizontal hint. Either
// edge may be omitted (ghost hint), but not both. Bottom/Top refer to the
// source stem edges, not the order in the caller's slice. The points are
// signed 16.16. The caller must have already positioned/locked DeviceY.
type CFFPrepositionedHintExperimental struct {
	Bottom *CFFHintEdgeFixedExperimental
	Top    *CFFHintEdgeFixedExperimental
}

type cffPreparedEdge struct {
	CFFHintEdgeFixedExperimental
	pairTop bool
}

func validCFFPreparedEdge(e *CFFHintEdgeFixedExperimental) bool {
	return e != nil && e.CharacterY >= math.MinInt32 && e.CharacterY <= math.MaxInt32 &&
		e.DeviceY >= math.MinInt32 && e.DeviceY <= math.MaxInt32
}

// BuildCFFPrepositionedHintMapExperimental preserves the FreeType insertion
// ordering, touching/overlap rejection in both spaces, and optional initial
// map synthetic (0,0) anchor. It accepts only externally prepared hint
// coordinates. It fails closed on invalid input. It is NOT general cf2_hintmap_build.
func BuildCFFPrepositionedHintMapExperimental(scale int64, hints []CFFPrepositionedHintExperimental, initial bool) (*CFFHintMapFixedExperimental, error) {
	if scale <= 0 || scale > math.MaxInt32 || len(hints) > 96 {
		return nil, fontErr("CFF prepositioned map invalid scale or hint count")
	}
	edges := make([]cffPreparedEdge, 0, len(hints)*2+1)
	insert := func(h CFFPrepositionedHintExperimental) error {
		if h.Bottom == nil && h.Top == nil {
			return fontErr("CFF prepared hint missing both edges")
		}
		if h.Bottom != nil && !validCFFPreparedEdge(h.Bottom) {
			return fontErr("CFF prepared invalid bottom")
		}
		if h.Top != nil && !validCFFPreparedEdge(h.Top) {
			return fontErr("CFF prepared invalid top")
		}
		first := h.Bottom
		second := h.Top
		pair := first != nil && second != nil
		if first == nil {
			first = second
		}
		if pair && (second.CharacterY < first.CharacterY || second.DeviceY < first.DeviceY) {
			return fontErr("CFF prepared invalid edge pair")
		}
		if len(edges)+1+boolToIntCFFExperimental(pair) > 192 {
			return fontErr("CFF prepared edge capacity")
		}
		index := len(edges)
		for i, e := range edges {
			if e.CharacterY >= first.CharacterY {
				index = i
				break
			}
		}
		if index < len(edges) {
			next := edges[index]
			if next.CharacterY == first.CharacterY || (pair && next.CharacterY <= second.CharacterY) || next.pairTop {
				return nil // FreeType silently rejects touching or straddling hints.
			}
		}
		if index > 0 && first.DeviceY < edges[index-1].DeviceY {
			return nil
		}
		last := first
		if pair {
			last = second
		}
		if index < len(edges) && last.DeviceY > edges[index].DeviceY {
			return nil
		}
		replacements := []cffPreparedEdge{{CFFHintEdgeFixedExperimental: CFFHintEdgeFixedExperimental{CharacterY: first.CharacterY, DeviceY: first.DeviceY, Scale: scale}}}
		if pair {
			replacements = append(replacements, cffPreparedEdge{CFFHintEdgeFixedExperimental: CFFHintEdgeFixedExperimental{CharacterY: second.CharacterY, DeviceY: second.DeviceY, Scale: scale}, pairTop: true})
		}
		edges = append(edges, make([]cffPreparedEdge, len(replacements))...)
		copy(edges[index+len(replacements):], edges[index:len(edges)-len(replacements)])
		copy(edges[index:], replacements)
		return nil
	}
	for _, h := range hints {
		if err := insert(h); err != nil {
			return nil, err
		}
	}
	if initial && (len(edges) == 0 || edges[0].CharacterY > 0 || edges[len(edges)-1].CharacterY < 0) {
		zero := CFFHintEdgeFixedExperimental{CharacterY: 0, DeviceY: 0, Scale: scale}
		if err := insert(CFFPrepositionedHintExperimental{Bottom: &zero}); err != nil {
			return nil, err
		}
	}
	out := make([]CFFHintEdgeFixedExperimental, len(edges))
	for i, e := range edges {
		out[i] = e.CFFHintEdgeFixedExperimental
	}
	// FreeType adjustHints recomputes the local interpolation slope between
	// each two neighboring edges, even if both edges were already locked.
	// Duplicate character-space coordinates leave the prior scale intact.
	for i := 1; i < len(out); i++ {
		dc := out[i].CharacterY - out[i-1].CharacterY
		if dc == 0 {
			continue
		}
		dd := out[i].DeviceY - out[i-1].DeviceY
		slope, err := cffFixedDivExperimental(dd, dc)
		if err != nil {
			return nil, err
		}
		out[i-1].Scale = slope
	}
	return NewCFFHintMapFixedExperimental(scale, out)
}

func boolToIntCFFExperimental(b bool) int {
	if b {
		return 1
	}
	return 0
}

// FT_DivFix-style signed 16.16 division for already bounded edge deltas.
// Do not wrap or silently clamp an unrepresentable slope.
func cffFixedDivExperimental(numerator, denominator int64) (int64, error) {
	if denominator == 0 || numerator < math.MinInt32 || numerator > math.MaxInt32 || denominator < math.MinInt32 || denominator > math.MaxInt32 {
		return 0, fontErr("CFF hintmap division bounds")
	}
	a, b := numerator, denominator
	neg := (a < 0) != (b < 0)
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	q := (a*65536 + b/2) / b
	if neg {
		q = -q
	}
	if q < 0 || q > math.MaxInt32 {
		return 0, fontErr("CFF hintmap interpolation scale overflow")
	}
	return q, nil
}
