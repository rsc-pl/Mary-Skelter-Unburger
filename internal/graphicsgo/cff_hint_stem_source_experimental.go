package graphicsgo

// Source-stem expansion for future CFF HintMap construction. This isolated
// research function DOES NOT capture Blue zones, fit widths, position unlocked
// edges or render pixels. It materializes original active Type2 horizontal
// stems (including -20/-21 ghost hints), respecting an operation's drawing
// mask, and optionally reuses coordinates locked by a preceding map.
import "math"

type CFFSourceStemHistoryExperimental struct {
	Used            bool
	BottomDS, TopDS int64 // signed 16.16, position fixed by previous use
}
type CFFSourceHorizontalHintExperimental struct {
	StemIndex int
	Hint      CFFPrepositionedHintExperimental
	Locked    bool
	Ghost     string // "", "bottom", "top"
}

// ExpandCFFSourceHorizontalHintsExperimental emits active hstems in the
// declaration order; the returned hint edges are *not* assumed to be already
// positioned for insertion. Unlocked device coordinates are only nominal.
// Initial-map callers must perform Blue capture and keep *only* captured
// edges; noninitial callers must perform capture then adjust remaining stems.
func ExpandCFFSourceHorizontalHintsExperimental(plan CFFHintPlan, op CFFHintSourceOperation, scale, hintOrigin, darkenY int64, history map[int]CFFSourceStemHistoryExperimental) ([]CFFSourceHorizontalHintExperimental, error) {
	if scale <= 0 || scale > math.MaxInt32 || hintOrigin < math.MinInt32 || hintOrigin > math.MaxInt32 || darkenY < 0 || darkenY > math.MaxInt32/2 || len(plan.Stems) > 96 {
		return nil, fontErr("CFF source-stem invalid bounds")
	}
	out := make([]CFFSourceHorizontalHintExperimental, 0, len(op.Active))
	seen := make(map[int]bool, len(op.Active))
	for _, idx := range op.Active {
		if idx < 0 || idx >= len(plan.Stems) || seen[idx] {
			return nil, fontErr("CFF source-stem invalid active index")
		}
		seen[idx] = true
		stem := plan.Stems[idx]
		if stem.Axis != "y" {
			if stem.Axis != "x" {
				return nil, fontErr("CFF source-stem invalid axis")
			}
			continue // vertical stems do not create Y-map edges
		}
		toFixed := func(v float64) (int64, error) {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < -32768 || v > 32767 {
				return 0, fontErr("CFF source-stem out of range")
			}
			vv := v * 65536
			q := math.Round(vv)
			if math.Abs(q-vv) > 1e-7 {
				return 0, fontErr("CFF source-stem not 16.16 representable")
			}
			return int64(q), nil
		}
		a, err := toFixed(stem.Start)
		if err != nil {
			return nil, err
		}
		b, err := toFixed(stem.End)
		if err != nil {
			return nil, err
		}
		width := b - a
		var bottom, top *CFFHintEdgeFixedExperimental
		edge := func(v int64, isTop bool) (*CFFHintEdgeFixedExperimental, error) {
			if isTop {
				v += darkenY * 2
			}
			v += hintOrigin
			if v < math.MinInt32 || v > math.MaxInt32 {
				return nil, fontErr("CFF source-stem adjusted edge overflows")
			}
			ds, e := cffFixedMulExperimental(v, scale)
			if e != nil {
				return nil, e
			}
			return &CFFHintEdgeFixedExperimental{CharacterY: v, DeviceY: ds, Scale: scale}, nil
		}
		ghost := ""
		switch width {
		case -21 * 65536:
			ghost = "bottom"
			bottom, err = edge(b, false)
		case -20 * 65536:
			ghost = "top"
			top, err = edge(a, true)
		default:
			if width < 0 {
				a, b = b, a
			} // FreeType permissive inverted-pair case
			bottom, err = edge(a, false)
			if err == nil {
				top, err = edge(b, true)
			}
		}
		if err != nil {
			return nil, err
		}
		entry := CFFSourceHorizontalHintExperimental{StemIndex: idx, Hint: CFFPrepositionedHintExperimental{Bottom: bottom, Top: top}, Ghost: ghost}
		if old, exists := history[idx]; exists && old.Used {
			entry.Locked = true
			if bottom != nil {
				if old.BottomDS < math.MinInt32 || old.BottomDS > math.MaxInt32 {
					return nil, fontErr("CFF source-stem locked bottom range")
				}
				bottom.DeviceY = old.BottomDS
			}
			if top != nil {
				if old.TopDS < math.MinInt32 || old.TopDS > math.MaxInt32 {
					return nil, fontErr("CFF source-stem locked top range")
				}
				top.DeviceY = old.TopDS
			}
		}
		out = append(out, entry)
	}
	return out, nil
}
