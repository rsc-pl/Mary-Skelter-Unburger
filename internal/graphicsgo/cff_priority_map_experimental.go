package graphicsgo

// research-only coupling between the verified source stem expansion,
// Blue capture and already-positioned edge insertion. It intentionally
// DOES NOT claim to implement cf2_hintmap_build: remaining non-captured stems,
// two-pass adjustHints, first-use history writes and path transforms are absent.
// The only complete map produced here is the INITIAL blue/locked-only map.
// For noninitial masks, Map is the prepositioned PRIORITY PART, not a usable
// substitute for the later full map. Neither result is wired into rendering.

type CFFPriorityMapExperimental struct {
	Map             *CFFHintMapFixedExperimental
	Candidates      []CFFSourceHorizontalHintExperimental
	CapturedIndices []int // candidate stems captured by Blue zones
	ReusedIndices   []int // candidate stems already used in an earlier map
	PendingIndices  []int // unlocked candidates NOT yet placed in this map
	Initial         bool
}

// BuildCFFPriorityMapExperimental uses all DECLARED horizontal stems on the
// initial build, regardless of the incoming drawing operation's active mask.
// Subsequent calls use exactly that operation's active mask and reuse the
// previous locked DS coordinates where provided. The Blue-zone implementation
// is currently only validated for LanguageGroup=0 and zero hint origin: fail
// closed rather than give silently incorrect maps for unsupported source data.
// It never writes to history, even when map insertion succeeds; writing stem
// history requires a complete, fitted noninitial map with accepted edge IDs.
func BuildCFFPriorityMapExperimental(plan CFFHintPlan, op CFFHintSourceOperation, scale, darkenY int64, stemDarkened, initial bool, languageGroup int, history map[int]CFFSourceStemHistoryExperimental) (CFFPriorityMapExperimental, error) {
	var result CFFPriorityMapExperimental
	result.Initial = initial
	if languageGroup != 0 {
		return result, fontErr("CFF priority map unsupported LanguageGroup")
	}
	if initial {
		for _, hist := range history {
			if hist.Used {
				return result, fontErr("CFF initial map cannot use historical stems")
			}
		}
	}
	blue, err := PrepareCFFBlueZonesFixedExperimental(plan.Private, scale, darkenY, stemDarkened)
	if err != nil {
		return result, err
	}
	selected := op
	if initial {
		selected.Active = make([]int, len(plan.Stems))
		for i := range selected.Active {
			selected.Active[i] = i
		}
	}
	hints, err := ExpandCFFSourceHorizontalHintsExperimental(plan, selected, scale, 0, darkenY, history)
	if err != nil {
		return result, err
	}
	// A fresh initial map is constructed BEFORE noninitial usage. In that
	// map only Blue-captured stems take priority; all others are pending.
	prepared := make([]CFFPrepositionedHintExperimental, 0, len(hints))
	for _, h := range hints {
		if h.Locked {
			result.ReusedIndices = append(result.ReusedIndices, h.StemIndex)
			prepared = append(prepared, h.Hint)
			result.Candidates = append(result.Candidates, h)
			continue
		}
		captured, yes, e := CaptureCFFBlueStemFixedExperimental(blue, h)
		if e != nil {
			return CFFPriorityMapExperimental{}, e
		}
		result.Candidates = append(result.Candidates, captured)
		if yes {
			result.CapturedIndices = append(result.CapturedIndices, h.StemIndex)
			prepared = append(prepared, captured.Hint)
		} else {
			result.PendingIndices = append(result.PendingIndices, h.StemIndex)
		}
	}
	result.Map, err = BuildCFFPrepositionedHintMapExperimental(scale, prepared, initial)
	if err != nil {
		return CFFPriorityMapExperimental{}, err
	}
	return result, nil
}
