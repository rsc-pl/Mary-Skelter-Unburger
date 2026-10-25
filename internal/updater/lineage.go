package updater

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ContentLineage records only prior modded content states that are required to
// upgrade an already-patched installation after that exact managed content has
// changed. Installer/UI-only releases must not add entries here.
type ContentLineage struct {
	SchemaVersion int               `json:"schema_version"`
	GDAT          []GDATLineageItem `json:"gdat_prior_targets"`
}

type GDATLineageItem struct {
	GameKey     string   `json:"game"`
	Archive     string   `json:"archive"`
	EntryIndex  int      `json:"entry_index"`
	PriorSHA256 []string `json:"prior_target_sha256"`
}

func LoadContentLineage(path string) (ContentLineage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ContentLineage{}, err
	}
	var l ContentLineage
	if err := json.Unmarshal(raw, &l); err != nil {
		return ContentLineage{}, err
	}
	if l.SchemaVersion != 1 {
		return ContentLineage{}, fmt.Errorf("unsupported content lineage schema %d", l.SchemaVersion)
	}
	seen := map[string]bool{}
	for _, item := range l.GDAT {
		if item.GameKey == "" || item.Archive == "" || item.EntryIndex < 0 || len(item.PriorSHA256) == 0 {
			return ContentLineage{}, fmt.Errorf("invalid GDAT lineage item: %+v", item)
		}
		key := fmt.Sprintf("%s\x00%s\x00%d", item.GameKey, item.Archive, item.EntryIndex)
		if seen[key] {
			return ContentLineage{}, fmt.Errorf("duplicate GDAT lineage item %s/%s/%d", item.GameKey, item.Archive, item.EntryIndex)
		}
		seen[key] = true
		for _, sha := range item.PriorSHA256 {
			b, err := hex.DecodeString(sha)
			if err != nil || len(b) != 32 {
				return ContentLineage{}, fmt.Errorf("invalid lineage SHA-256 %q", sha)
			}
		}
	}
	return l, nil
}

func (l ContentLineage) GDATPriorSources(game, archive string, index int) []string {
	var out []string
	seen := map[string]bool{}
	for _, item := range l.GDAT {
		if item.GameKey != game || item.Archive != archive || item.EntryIndex != index {
			continue
		}
		for _, sha := range item.PriorSHA256 {
			if !seen[sha] {
				seen[sha] = true
				out = append(out, sha)
			}
		}
	}
	sort.Strings(out)
	return out
}
