package maintainer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"maryskelter-unburger/internal/updater"
)

type bundleDatasetRow struct {
	Dataset       string   `json:"dataset"`
	ChangedFields int      `json:"changed_fields"`
	Files         []string `json:"files"`
}

type bundleManifest struct {
	Dataset             string             `json:"dataset"`
	ChangedFields       int                `json:"changed_fields"`
	Files               []string           `json:"files"`
	Datasets            []bundleDatasetRow `json:"datasets"`
	GraphicalTextAssets []string           `json:"graphical_text_assets,omitempty"`
}

type graphicsBuildResult struct {
	ID      string `json:"id"`
	Dataset string `json:"dataset"`
	Archive int    `json:"archive"`
}

type graphicsBuildReport struct {
	Status  string                `json:"status"`
	Results []graphicsBuildResult `json:"results"`
}

type bundleChangeInfo struct {
	ChangedFields int `json:"changed_fields"`
}

func gameForDataset(ds string) (string, error) {
	if strings.HasPrefix(ds, "MS2_") {
		return "MS2", nil
	}
	if strings.HasPrefix(ds, "MSF_") {
		return "MSF", nil
	}
	return "", fmt.Errorf("unsupported patch dataset %s", ds)
}

func readGraphicsBuildResults(path string) (map[string][]graphicsBuildResult, error) {
	out := map[string][]graphicsBuildResult{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var rep graphicsBuildReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, err
	}
	if rep.Status != "PASS" {
		return nil, fmt.Errorf("graphics report exists but is not PASS")
	}
	for _, row := range rep.Results {
		if row.Dataset == "" || row.Archive < 0 {
			return nil, fmt.Errorf("invalid graphics report result: %+v", row)
		}
		out[row.Dataset] = append(out[row.Dataset], row)
	}
	return out, nil
}

func nonIdenticalRebuildArchives(path string) ([]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rep struct {
		Archives []struct {
			ArchiveNumber int  `json:"archive_number"`
			ByteIdentical bool `json:"byte_identical"`
		} `json:"archives"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	var out []int
	for _, a := range rep.Archives {
		if !a.ByteIdentical && !seen[a.ArchiveNumber] {
			seen[a.ArchiveNumber] = true
			out = append(out, a.ArchiveNumber)
		}
	}
	sort.Ints(out)
	return out, nil
}

// BuildPatchBundles assembles only verified changed DATs into the private
// maintainer dist staging tree. Save conversion is a native client feature, so
// the bundle must not carry a second Python implementation.
func BuildPatchBundles(root, distStage string, stdout io.Writer) error {
	raw, err := os.ReadFile(filepath.Join(root, ".local", "build", "change_manifest.json"))
	if err != nil {
		return err
	}
	changes := map[string]bundleChangeInfo{}
	if err := json.Unmarshal(raw, &changes); err != nil {
		return err
	}
	graphics, err := readGraphicsBuildResults(filepath.Join(root, ".local", "build", "reports", "graphics.json"))
	if err != nil {
		return err
	}
	datasets := map[string]bool{}
	for ds, info := range changes {
		if info.ChangedFields > 0 {
			datasets[ds] = true
		}
	}
	for ds := range graphics {
		datasets[ds] = true
	}

	for _, game := range []string{"MS2", "MSF"} {
		var gameDS []string
		for ds := range datasets {
			g, e := gameForDataset(ds)
			if e != nil {
				return e
			}
			if g == game {
				gameDS = append(gameDS, ds)
			}
		}
		sort.Strings(gameDS)
		if len(gameDS) == 0 {
			continue
		}
		profileKey := "ms2"
		if game == "MSF" {
			profileKey = "finale"
		}
		bundleName := updater.Profiles[profileKey].BundleDir
		bundle := filepath.Join(distStage, bundleName)
		if err := os.RemoveAll(bundle); err != nil {
			return err
		}
		dest := filepath.Join(bundle, "masterWindows")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}

		copied := map[string]bundleCandidate{}
		collisionCandidates := map[string][]bundleCandidate{}
		var rows []bundleDatasetRow
		var gameGraphics []graphicsBuildResult
		for _, ds := range gameDS {
			changed := changes[ds].ChangedFields
			src := filepath.Join(root, ".local", "build", "rebuilt", ds, "masterWindows")
			aset := map[int]bool{}
			if changed > 0 {
				rep := filepath.Join(root, ".local", "build", "reports", ds+".rebuild.json")
				if !dir(src) || !file(rep) {
					return fmt.Errorf("missing rebuild for %s", ds)
				}
				arcs, e := nonIdenticalRebuildArchives(rep)
				if e != nil {
					return e
				}
				for _, a := range arcs {
					aset[a] = true
				}
			}
			for _, gr := range graphics[ds] {
				aset[gr.Archive] = true
				gameGraphics = append(gameGraphics, gr)
			}
			if len(aset) > 0 && !dir(src) {
				return fmt.Errorf("missing rebuilt/graphics archive tree for %s: %s", ds, src)
			}
			var arcs []int
			for a := range aset {
				arcs = append(arcs, a)
			}
			sort.Ints(arcs)
			files := make([]string, 0, len(arcs))
			for _, a := range arcs {
				name := fmt.Sprintf("GxArchivedFile%03d.dat", a)
				p := filepath.Join(src, name)
				if !file(p) {
					return fmt.Errorf("canonical build names missing file: %s", p)
				}
				digest, e := updater.SHA256File(p)
				if e != nil {
					return e
				}
				if prev, ok := copied[name]; ok && prev.sha != digest {
					if len(collisionCandidates[name]) == 0 {
						collisionCandidates[name] = append(collisionCandidates[name], prev)
					}
					collisionCandidates[name] = append(collisionCandidates[name], bundleCandidate{ds, p, digest})
				}
				if _, ok := copied[name]; !ok {
					copied[name] = bundleCandidate{ds, p, digest}
				}
				files = append(files, name)
			}
			rows = append(rows, bundleDatasetRow{Dataset: ds, ChangedFields: changed, Files: files})
		}
		names := make([]string, 0, len(copied))
		for n := range copied {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			destination := filepath.Join(dest, n)
			if variants := collisionCandidates[n]; len(variants) > 0 {
				merged, rep, e := mergeBundleConflict(root, game, n, variants)
				if e != nil {
					return fmt.Errorf("merge %s: %w", n, e)
				}
				if e = os.WriteFile(destination, merged, 0o644); e != nil {
					return e
				}
				if stdout != nil {
					fmt.Fprintf(stdout, "NATIVE GDAT MERGE %s: %d changed, %d untouched entries; sha %s\n", n, len(rep.ChangedEntries), rep.UnchangedEntries, rep.OutputSHA256)
				}
			} else if err := copyFile(copied[n].path, destination); err != nil {
				return err
			}
		}
		total := 0
		labels := make([]string, len(rows))
		for i, r := range rows {
			total += r.ChangedFields
			labels[i] = r.Dataset
		}
		mf := bundleManifest{Dataset: strings.Join(labels, " + "), ChangedFields: total, Files: names, Datasets: rows}
		if len(rows) == 1 {
			mf.Dataset = rows[0].Dataset
		}
		install := "Back up the original game files first.\nCopy the files from masterWindows/ over the same names in the game masterWindows directory.\nOnly DATs changed by the verified canonical build are included.\n"
		if len(gameGraphics) > 0 {
			for _, gr := range gameGraphics {
				mf.GraphicalTextAssets = append(mf.GraphicalTextAssets, gr.ID)
			}
			install += "Canonical graphical-text fixes listed in PATCH_MANIFEST.json are already integrated into these DATs.\n"
		}
		if err := jsonWrite(filepath.Join(bundle, "PATCH_MANIFEST.json"), mf); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(bundle, "INSTALL.txt"), []byte(install), 0o644); err != nil {
			return err
		}
		if stdout != nil {
			if len(names) == 0 {
				fmt.Fprintf(stdout, "%s patch files: (none)\n", game)
			} else {
				fmt.Fprintf(stdout, "%s patch files: %s\n", game, strings.Join(names, ", "))
			}
		}
	}
	return nil
}
