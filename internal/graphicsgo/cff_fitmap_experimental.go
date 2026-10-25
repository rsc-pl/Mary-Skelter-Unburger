package graphicsgo

// Experimental complete edge-positioning research, detached from all
// production texture builders. This implements accepted-source edge provenance,
// the two directional FreeType CFF stem-position passes, and a transactional
// first-use history update. It does NOT hint Type2 drawing operations or
// establish glyph/RGBA parity with FreeType.

import "math"

type CFFFittedEdgeExperimental struct {
	CFFHintEdgeFixedExperimental
	StemIndex                              int
	PairTop, PairBottom, Locked, Synthetic bool
}

type CFFFittedMapExperimental struct {
	Map                *CFFHintMapFixedExperimental
	Edges              []CFFFittedEdgeExperimental
	Accepted, Rejected []int                                    // source stem IDs, after actual insertion checks
	History            map[int]CFFSourceStemHistoryExperimental // isolated output snapshot
	Initial            bool
}

func cffCopyHistoryExperimental(src map[int]CFFSourceStemHistoryExperimental) map[int]CFFSourceStemHistoryExperimental {
	dst := make(map[int]CFFSourceStemHistoryExperimental, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// BuildCFFFittedMapExperimental takes the original, already classified // candidates in source order. initialMap may be nil ONLY for an initial map;
// noninitial free edges are seeded from the initial mapping, preserving the
// nominal stem width around the mapped source midpoint. The original history
// and priority candidates are never mutated. LanguageGroup 1 stays rejected
// by the classifier; this function is not a public general-purpose hinter.
func BuildCFFFittedMapExperimental(priority CFFPriorityMapExperimental, scale int64, initialMap *CFFHintMapFixedExperimental, history map[int]CFFSourceStemHistoryExperimental) (CFFFittedMapExperimental, error) {
	res := CFFFittedMapExperimental{Initial: priority.Initial, History: cffCopyHistoryExperimental(history)}
	if scale <= 0 || scale > math.MaxInt32 || len(priority.Candidates) > 96 || (!priority.Initial && initialMap == nil) {
		return CFFFittedMapExperimental{}, fontErr("fitter scale/candidates/initial map invalid")
	}
	captured := make(map[int]bool)
	reused := make(map[int]bool)
	for _, i := range priority.CapturedIndices {
		captured[i] = true
	}
	for _, i := range priority.ReusedIndices {
		reused[i] = true
	}
	edges := make([]CFFFittedEdgeExperimental, 0, 2*len(priority.Candidates)+1)
	seen := make(map[int]bool)
	insert := func(h CFFSourceHorizontalHintExperimental, locked bool) (bool, error) {
		if seen[h.StemIndex] {
			return false, fontErr("duplicated source stem")
		}
		seen[h.StemIndex] = true
		a, b := h.Hint.Bottom, h.Hint.Top
		if a == nil && b == nil {
			return false, fontErr("no source edges")
		}
		if a != nil && !validCFFPreparedEdge(a) || b != nil && !validCFFPreparedEdge(b) {
			return false, fontErr("malformed source edge")
		}
		first := a
		second := b
		pair := a != nil && b != nil
		if first == nil {
			first = second
		}
		if pair && (second.CharacterY < first.CharacterY || second.DeviceY < first.DeviceY) {
			return false, fontErr("inverted pair")
		}
		// Make private copies: mapping through initialMap must not modify .
		lo := *first
		hi := CFFHintEdgeFixedExperimental{}
		if pair {
			hi = *second
		}
		if !priority.Initial && !locked {
			if pair {
				mid, e := initialMap.MapY((lo.CharacterY + hi.CharacterY) / 2)
				if e != nil {
					return false, e
				}
				half, e := cffFixedMulExperimental((hi.CharacterY-lo.CharacterY)/2, scale)
				if e != nil {
					return false, e
				}
				lo.DeviceY = mid - half
				hi.DeviceY = mid + half
			} else {
				var e error
				lo.DeviceY, e = initialMap.MapY(lo.CharacterY)
				if e != nil {
					return false, e
				}
			}
		}
		if lo.DeviceY < math.MinInt32 || lo.DeviceY > math.MaxInt32 || pair && (hi.DeviceY < math.MinInt32 || hi.DeviceY > math.MaxInt32) {
			return false, fontErr("mapped device bounds")
		}
		idx := len(edges)
		for i, v := range edges {
			if v.CharacterY >= lo.CharacterY {
				idx = i
				break
			}
		}
		if idx < len(edges) {
			next := edges[idx]
			if next.CharacterY == lo.CharacterY || pair && next.CharacterY <= hi.CharacterY || next.PairTop {
				return false, nil
			}
		}
		if idx > 0 && lo.DeviceY < edges[idx-1].DeviceY {
			return false, nil
		}
		last := lo
		if pair {
			last = hi
		}
		if idx < len(edges) && last.DeviceY > edges[idx].DeviceY {
			return false, nil
		}
		if len(edges)+1+boolToIntCFFExperimental(pair) > 192 {
			return false, fontErr("edge capacity")
		}
		added := []CFFFittedEdgeExperimental{{CFFHintEdgeFixedExperimental: lo, StemIndex: h.StemIndex, PairBottom: pair, Locked: locked}}
		if pair {
			added = append(added, CFFFittedEdgeExperimental{CFFHintEdgeFixedExperimental: hi, StemIndex: h.StemIndex, PairTop: true, Locked: locked})
		}
		edges = append(edges, make([]CFFFittedEdgeExperimental, len(added))...)
		copy(edges[idx+len(added):], edges[idx:len(edges)-len(added)])
		copy(edges[idx:], added)
		return true, nil
	}
	// Follow FreeType's priority ordering, not the position within a JSON map.
	for pass := 0; pass < 2; pass++ {
		for _, h := range priority.Candidates {
			locked := captured[h.StemIndex] || reused[h.StemIndex] || h.Locked
			if pass == 0 && !locked || pass == 1 && locked || priority.Initial && pass == 1 {
				continue
			}
			ok, e := insert(h, locked)
			if e != nil {
				return CFFFittedMapExperimental{}, e
			}
			if ok {
				res.Accepted = append(res.Accepted, h.StemIndex)
			} else {
				res.Rejected = append(res.Rejected, h.StemIndex)
			}
		}
	}
	if priority.Initial && (len(edges) == 0 || edges[0].CharacterY > 0 || edges[len(edges)-1].CharacterY < 0) {
		zero := CFFFittedEdgeExperimental{CFFHintEdgeFixedExperimental: CFFHintEdgeFixedExperimental{Scale: scale}, StemIndex: -1, Locked: true, Synthetic: true}
		at := len(edges)
		for i, v := range edges {
			if v.CharacterY >= 0 {
				at = i
				break
			}
		}
		edges = append(edges, zero)
		copy(edges[at+1:], edges[at:len(edges)-1])
		edges[at] = zero
	}
	if e := cffAdjustFittedEdgesExperimental(edges); e != nil {
		return CFFFittedMapExperimental{}, e
	}
	out := make([]CFFHintEdgeFixedExperimental, len(edges))
	for i, v := range edges {
		out[i] = v.CFFHintEdgeFixedExperimental
	}
	var err error
	res.Map, err = NewCFFHintMapFixedExperimental(scale, out)
	if err != nil {
		return CFFFittedMapExperimental{}, err
	}
	res.Edges = edges
	// Only edges that actually survived insertion become first-use history.
	// The initial map must not claim a stem was used during a drawing mask.
	if !priority.Initial {
		for _, e := range edges {
			if e.Synthetic {
				continue
			}
			hist := res.History[e.StemIndex]
			hist.Used = true
			if e.PairTop {
				hist.TopDS = e.DeviceY
			} else if e.PairBottom {
				hist.BottomDS = e.DeviceY
			} else {
				// For ghost hints, consult actual original edge presence.
				for _, h := range priority.Candidates {
					if h.StemIndex != e.StemIndex {
						continue
					}
					if h.Hint.Bottom == nil {
						hist.TopDS = e.DeviceY
					} else {
						hist.BottomDS = e.DeviceY
					}
					break
				}
			}
			res.History[e.StemIndex] = hist
		}
	}
	return res, nil
}

// CFF uses a half-pixel minimum gap between adjacent distinct hints.
const cffMinCounterExperimental int64 = 32768

func cffAdjustFittedEdgesExperimental(edges []CFFFittedEdgeExperimental) error {
	type deferred struct {
		j  int
		up int64
	}
	saved := make([]deferred, 0, len(edges))
	for i := 0; i < len(edges); i++ {
		j := i
		pair := edges[i].PairBottom
		if pair {
			j++
			if j >= len(edges) || !edges[j].PairTop {
				return fontErr("broken pair")
			}
		}
		if !edges[i].Locked {
			fracDown := edges[i].DeviceY & 0xffff
			fracUp := edges[j].DeviceY & 0xffff
			downMoveDown := -fracDown
			upMoveDown := -fracUp
			downMoveUp := int64(0)
			if fracDown != 0 {
				downMoveUp = 65536 - fracDown
			}
			upMoveUp := int64(0)
			if fracUp != 0 {
				upMoveUp = 65536 - fracUp
			}
			moveUp := min(downMoveUp, upMoveUp)
			moveDown := max(downMoveDown, upMoveDown)
			roomUp := j >= len(edges)-1 || edges[j+1].DeviceY >= edges[j].DeviceY+moveUp+cffMinCounterExperimental
			roomDown := i == 0 || edges[i-1].DeviceY <= edges[i].DeviceY+moveDown-cffMinCounterExperimental
			var move int64
			deferredMove := false
			if roomUp {
				if roomDown {
					if -moveDown < moveUp {
						move = moveDown
					} else {
						move = moveUp
					}
				} else {
					move = moveUp
				}
			} else if roomDown {
				move = moveDown
				deferredMove = moveUp < -moveDown
			} else {
				deferredMove = true
			}
			if deferredMove && j < len(edges)-1 && !edges[j+1].Locked {
				saved = append(saved, deferred{j: j, up: moveUp - move})
			}
			edges[i].DeviceY += move
			if pair {
				edges[j].DeviceY += move
			}
		}
		if i > 0 && edges[i-1].DeviceY > edges[i].DeviceY {
			return fontErr("nonmonotonic hinted edge")
		}
		if pair && edges[i].DeviceY > edges[j].DeviceY {
			return fontErr("inverted hinted pair")
		}
		if i > 0 && edges[i].CharacterY != edges[i-1].CharacterY {
			slope, e := cffFixedDivExperimental(edges[i].DeviceY-edges[i-1].DeviceY, edges[i].CharacterY-edges[i-1].CharacterY)
			if e != nil {
				return e
			}
			edges[i-1].Scale = slope
		}
		if pair {
			if edges[j].CharacterY != edges[j-1].CharacterY {
				slope, e := cffFixedDivExperimental(edges[j].DeviceY-edges[j-1].DeviceY, edges[j].CharacterY-edges[j-1].CharacterY)
				if e != nil {
					return e
				}
				edges[j-1].Scale = slope
			}
			i++
		}
	}
	for i := len(saved) - 1; i >= 0; i-- {
		d := saved[i]
		j := d.j
		if edges[j+1].DeviceY >= edges[j].DeviceY+d.up+cffMinCounterExperimental {
			edges[j].DeviceY += d.up
			if edges[j].PairTop {
				if j == 0 {
					return fontErr("broken deferred pair")
				}
				edges[j-1].DeviceY += d.up
			}
		}
	}
	for i := 1; i < len(edges); i++ {
		if edges[i].DeviceY < edges[i-1].DeviceY {
			return fontErr("second pass ordering")
		}
	}
	return nil
}
