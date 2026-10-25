package maintainer

// GraphicsSourceGapAudit reads only the three pristine PC archives needed for
// the five source variants not yet recovered into the private reference corpus.
// It never substitutes a similar Switch/modified TID for an exact PC source.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type GraphicsGapMasters struct{ MS2, Finale string }
type GraphicsGapItem struct {
	ID          string `json:"id"`
	Dataset     string `json:"dataset"`
	Archive     int    `json:"archive"`
	Index       int    `json:"gdat_index"`
	Inner       string `json:"inner"`
	ExpectedSHA string `json:"expected_sha256"`
	ActualSHA   string `json:"actual_sha256,omitempty"`
	Status      string `json:"status"`
	Detail      string `json:"detail,omitempty"`
}
type GraphicsGapAuditReport struct {
	Status    string            `json:"status"`
	Recovered int               `json:"matched_exact"`
	Total     int               `json:"required"`
	Items     []GraphicsGapItem `json:"items"`
}

type gapManifest struct {
	SchemaVersion int `json:"schema_version"`
	Assets        []struct {
		ID       string `json:"id"`
		Dataset  string `json:"dataset"`
		Enabled  bool   `json:"enabled"`
		Archive  int    `json:"archive"`
		Index    int    `json:"gdat_index"`
		Inner    string `json:"cl3_inner_name"`
		Variants []struct {
			SHA    string `json:"tid_sha256"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Format string `json:"format"`
		} `json:"source_variants"`
	} `json:"assets"`
}

var graphicsMissingPCSourceIDs = map[string]bool{
	"MS1_INCLUDED_RESIDENCE_NAMES": true,
	"MSF_EQUIPMENT_HEADER_NAMES":   true,
	"MS2_TITLE_MOD_FOOTER_MAIN":    true,
	"MS2_TITLE_MOD_FOOTER_BE":      true,
	"MSF_TITLE_MOD_FOOTER":         true,
}

func gapSourcePath(root string, archive int) string {
	name := fmt.Sprintf("GxArchivedFile%03d.dat", archive)
	if root == "" {
		return ""
	}
	candidate := filepath.Join(root, "masterWindows", name)
	if st, err := os.Lstat(filepath.Join(root, "masterWindows")); err == nil && st.IsDir() {
		return candidate
	}
	return filepath.Join(root, name)
}

// GraphicsSourceGapAudit inspects ONLY exact canonical source identities from
// graphics/manifest.json, never writes or extracts originals and never follows
// symlink input DATs. A missing master is a reported HOLD, not fabricated PASS.
func GraphicsSourceGapAudit(projectRoot string, masters GraphicsGapMasters) (GraphicsGapAuditReport, error) {
	out := GraphicsGapAuditReport{Status: "PARTIAL_OR_UNVERIFIED", Total: len(graphicsMissingPCSourceIDs), Items: []GraphicsGapItem{}}
	raw, err := os.ReadFile(filepath.Join(projectRoot, "graphics", "manifest.json"))
	if err != nil {
		return out, err
	}
	var m gapManifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return out, err
	}
	if m.SchemaVersion != 2 {
		return out, fmt.Errorf("unsupported graphics schema %d", m.SchemaVersion)
	}
	seen := map[string]bool{}
	type key struct {
		dataset string
		archive int
	}
	grouped := map[key][]int{}
	for _, a := range m.Assets {
		if !graphicsMissingPCSourceIDs[a.ID] {
			continue
		}
		if seen[a.ID] || !a.Enabled || a.Archive < 0 || a.Index < 0 || a.Inner == "" || len(a.Variants) != 1 || !validGraphicsTexture(graphicsVariant{TIDSHA256: a.Variants[0].SHA, Width: a.Variants[0].Width, Height: a.Variants[0].Height, Format: a.Variants[0].Format}) || (a.Dataset != "MS2_PC_LOCALIZED" && a.Dataset != "MSF_PC_LOCALIZED") {
			return out, fmt.Errorf("invalid/duplicated missing-source manifest entry %s", a.ID)
		}
		if (a.ID == "MS1_INCLUDED_RESIDENCE_NAMES" || strings.HasPrefix(a.ID, "MS2_TITLE_MOD_")) && (a.Dataset != "MS2_PC_LOCALIZED" || a.Archive != 55) {
			return out, fmt.Errorf("%s source route drift", a.ID)
		}
		if a.ID == "MSF_EQUIPMENT_HEADER_NAMES" && (a.Dataset != "MSF_PC_LOCALIZED" || a.Archive != 74) {
			return out, fmt.Errorf("%s source route drift", a.ID)
		}
		if a.ID == "MSF_TITLE_MOD_FOOTER" && (a.Dataset != "MSF_PC_LOCALIZED" || a.Archive != 73) {
			return out, fmt.Errorf("%s source route drift", a.ID)
		}
		seen[a.ID] = true
		out.Items = append(out.Items, GraphicsGapItem{ID: a.ID, Dataset: a.Dataset, Archive: a.Archive, Index: a.Index, Inner: a.Inner, ExpectedSHA: strings.ToLower(a.Variants[0].SHA), Status: "UNTESTED"})
		grouped[key{a.Dataset, a.Archive}] = append(grouped[key{a.Dataset, a.Archive}], len(out.Items)-1)
	}
	if len(seen) != len(graphicsMissingPCSourceIDs) {
		return out, fmt.Errorf("only %d/5 missing source entries in manifest", len(seen))
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].ID < out.Items[j].ID })
	grouped = map[key][]int{}
	for i, v := range out.Items {
		grouped[key{v.Dataset, v.Archive}] = append(grouped[key{v.Dataset, v.Archive}], i)
	}
	for group, indexes := range grouped {
		master := masters.MS2
		if group.dataset == "MSF_PC_LOCALIZED" {
			master = masters.Finale
		}
		path := gapSourcePath(master, group.archive)
		if path == "" {
			for _, i := range indexes {
				out.Items[i].Status = "MASTER_NOT_CONFIGURED"
			}
			continue
		}
		st, e := os.Lstat(path)
		if os.IsNotExist(e) {
			for _, i := range indexes {
				out.Items[i].Status = "ARCHIVE_MISSING"
			}
			continue
		}
		if e != nil {
			return out, fmt.Errorf("stat pristine %s: %w", path, e)
		}
		if !st.Mode().IsRegular() {
			for _, i := range indexes {
				out.Items[i].Status = "UNSAFE_OR_NONREGULAR_ARCHIVE"
			}
			continue
		}
		// A strict read-only source identity check; never open a symlink DAT.
		raw, e := os.ReadFile(path)
		if e != nil {
			return out, fmt.Errorf("read pristine %s: %w", path, e)
		}
		for _, i := range indexes {
			item := &out.Items[i]
			tid, _, ex := mskit.ExtractGraphicsTID(raw, item.Index, item.Inner)
			if ex != nil {
				item.Status = "TID_NOT_EXTRACTABLE"
				item.Detail = ex.Error()
				continue
			}
			sum := sha256.Sum256(tid)
			item.ActualSHA = hex.EncodeToString(sum[:])
			if item.ActualSHA != item.ExpectedSHA {
				item.Status = "SHA_MISMATCH"
				continue
			}
			item.Status = "EXACT_ORIGINAL_MATCH"
			out.Recovered++
		}
	}
	if out.Recovered == out.Total {
		out.Status = "ALL_FIVE_EXACT_ORIGINALS_FOUND"
	}
	return out, nil
}
