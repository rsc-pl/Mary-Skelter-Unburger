package graphicsgo

// Standalone research-only 16.16 coordinate evaluator for an ALREADY BUILT
// Adobe/FreeType-style CFF vertical HintMap. This is NOT the builder: stem
// insertion, Blue zones, edge adjustment/history and queued contour flushes
// must be implemented/validated separately before real glyph rendering.
// No production caller uses this path.
import (
	"math"
	"sort"
)

// CFFHintEdgeFixedExperimental contains source (character space) and destination
// (device space) positions, both in signed 16.16 units. Scale is the 16.16
// slope for the interval extending from this edge to the next. Duplicate
// source coordinates are intentional: select the FINAL matching edge.
type CFFHintEdgeFixedExperimental struct{ CharacterY, DeviceY, Scale int64 }

// CFFHintMapFixedExperimental mirrors the actual mapping step of cf2_hintmap_map,
// not its edge-building policy. Immutable after construction.
type CFFHintMapFixedExperimental struct {
	scale int64
	edges []CFFHintEdgeFixedExperimental
}

func cffFixedMulExperimental(a, b int64) (int64, error) {
	// FT_MulFix semantics: multiply in signed 64-bit then round magnitude
	// half-up symmetrically, including on negative products.
	if a < math.MinInt32 || a > math.MaxInt32 || b < math.MinInt32 || b > math.MaxInt32 {
		return 0, fontErr("CFF hintmap fixed operand bounds")
	}
	p := a * b
	neg := p < 0
	if neg {
		p = -p
	}
	out := (p + 0x8000) >> 16
	if neg {
		out = -out
	}
	if out < math.MinInt32 || out > math.MaxInt32 {
		return 0, fontErr("CFF hintmap fixed result overflow")
	}
	return out, nil
}

func NewCFFHintMapFixedExperimental(scale int64, edges []CFFHintEdgeFixedExperimental) (*CFFHintMapFixedExperimental, error) {
	if scale <= 0 || scale > math.MaxInt32 || len(edges) > 192 {
		return nil, fontErr("CFF hintmap invalid scale or edge count")
	}
	m := &CFFHintMapFixedExperimental{scale: scale}
	m.edges = append([]CFFHintEdgeFixedExperimental(nil), edges...)
	for i, e := range m.edges {
		if e.CharacterY < math.MinInt32 || e.CharacterY > math.MaxInt32 || e.DeviceY < math.MinInt32 || e.DeviceY > math.MaxInt32 || e.Scale < 0 || e.Scale > math.MaxInt32 {
			return nil, fontErr("CFF hintmap invalid fixed edge")
		}
		if i > 0 && (e.CharacterY < m.edges[i-1].CharacterY || e.DeviceY < m.edges[i-1].DeviceY) {
			return nil, fontErr("CFF hintmap unsorted edge")
		}
	}
	return m, nil
}

func (m *CFFHintMapFixedExperimental) MapY(y int64) (int64, error) {
	if m == nil || y < math.MinInt32 || y > math.MaxInt32 {
		return 0, fontErr("CFF hintmap invalid input")
	}
	if len(m.edges) == 0 {
		return cffFixedMulExperimental(y, m.scale)
	}
	// Highest edge whose source Y is <= y. Source-coordinate duplicates are
	// allowed; selecting the last equality matches cf2_hintmap_map.
	i := sort.Search(len(m.edges), func(i int) bool { return m.edges[i].CharacterY > y }) - 1
	edge := m.edges[0]
	s := m.scale
	if i >= 0 {
		edge = m.edges[i]
		s = edge.Scale
	}
	delta := y - edge.CharacterY
	v, err := cffFixedMulExperimental(delta, s)
	if err != nil {
		return 0, err
	}
	v += edge.DeviceY
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, fontErr("CFF hintmap mapped overflow")
	}
	return v, nil
}
