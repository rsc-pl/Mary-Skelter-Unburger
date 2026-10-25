package maintainer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// mergeBundleConflict is used only when two actual *different* rebuilt PC
// datasets produce distinct DAT candidates for one physical archive. It reads
// the user-provided pristine cache, checks the canonical whole-DAT SHA and size,
// then performs the strict entry-level three-way merge. No guessed originals.
type bundleCandidate struct{ dataset, path, sha string }

func mergeBundleConflict(root, game, archive string, variants []bundleCandidate) ([]byte, PCGDATMergeReport, error) {
	var empty PCGDATMergeReport
	cache := cachedSources(root)
	master, ds := cache.MS2Master, "MS2_PC_LOCALIZED"
	if game == "MSF" {
		master, ds = cache.FinaleMaster, "MSF_PC_LOCALIZED"
	}
	if game != "MS2" && game != "MSF" {
		return nil, empty, fmt.Errorf("unknown dataset game %q", game)
	}
	rows, err := readSourceHashManifest(filepath.Join(root, "toolchain", "manifests", "expected_pc_archive_sha256.tsv"))
	if err != nil {
		return nil, empty, err
	}
	var pin string
	var size int64
	for _, row := range rows {
		if row.Dataset == ds && row.File == archive {
			if pin != "" {
				return nil, empty, fmt.Errorf("duplicate canonical source hash row")
			}
			pin = row.SHA256
			size = row.Bytes
		}
	}
	if pin == "" {
		return nil, empty, fmt.Errorf("no pinned pristine hash for %s/%s", ds, archive)
	}
	path := filepath.Join(master, archive)
	st, err := os.Lstat(path)
	if err != nil {
		return nil, empty, fmt.Errorf("pristine DAT absent: %w", err)
	}
	if !st.Mode().IsRegular() || st.Size() != size {
		return nil, empty, fmt.Errorf("pristine DAT size/type mismatch: %s", path)
	}
	pristine, err := os.ReadFile(path)
	if err != nil {
		return nil, empty, err
	}
	if !strings.EqualFold(mergeSHA(pristine), pin) {
		return nil, empty, fmt.Errorf("pristine %s has wrong whole-file SHA", archive)
	}
	data := make([][]byte, 0, len(variants))
	for _, v := range variants {
		if v.dataset == "" || v.sha == "" {
			return nil, empty, fmt.Errorf("missing candidate provenance")
		}
		b, e := os.ReadFile(v.path)
		if e != nil {
			return nil, empty, e
		}
		if mergeSHA(b) != v.sha {
			return nil, empty, fmt.Errorf("candidate %s SHA drift", v.dataset)
		}
		data = append(data, b)
	}
	return MergePCGDATVariants(pristine, data, pin)
}
