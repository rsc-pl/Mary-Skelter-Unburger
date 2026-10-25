package maintainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"maryskelter-unburger/internal/mskit"
)

// PCGDATMergeReport is an exact entry-level provenance ledger for merging
// independently rebuilt DATs against a single SHA-pinned pristine source.
// A source entry may be changed by multiple candidates only when every new
// byte is identical. Different edits to one entry need sequential rebuild,
// never last-writer-wins replacement.
type PCGDATMergeReport struct {
	SourceSHA256              string   `json:"source_sha256"`
	CandidateSHA256           []string `json:"candidate_sha256"`
	OutputSHA256              string   `json:"output_sha256"`
	TotalEntries              int      `json:"total_entries"`
	ChangedEntries            []int    `json:"changed_entries"`
	DuplicateIdenticalEntries []int    `json:"duplicate_identical_entries"`
	UnchangedEntries          int      `json:"unchanged_entries"`
}

func mergeSHA(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// MergePCGDATVariants validates the *entire* pristine GDAT and each candidate.
// It never accepts a candidate against a guessed baseline and never resolves a
// same-entry semantic conflict by discarding data. It is pure/read-only.
func MergePCGDATVariants(pristine []byte, candidates [][]byte, expectedPristineSHA string) ([]byte, PCGDATMergeReport, error) {
	r := PCGDATMergeReport{SourceSHA256: mergeSHA(pristine)}
	if expectedPristineSHA == "" || r.SourceSHA256 != expectedPristineSHA {
		return nil, r, fmt.Errorf("pristine PC master DAT SHA mismatch or missing pin: got %s", r.SourceSHA256)
	}
	base, err := mskit.ParseGDAT(pristine)
	if err != nil {
		return nil, r, fmt.Errorf("pristine GDAT: %w", err)
	}
	if len(candidates) < 2 {
		return nil, r, fmt.Errorf("need two or more independent candidate DATs")
	}
	r.TotalEntries = len(base.Entries)
	merged := append([][]byte(nil), base.Entries...)
	owners := make([]int, len(merged))
	for i, cand := range candidates {
		cr, e := mskit.ParseGDAT(cand)
		if e != nil {
			return nil, r, fmt.Errorf("candidate %d invalid GDAT: %w", i, e)
		}
		if len(cr.Entries) != len(base.Entries) {
			return nil, r, fmt.Errorf("candidate %d entry-count drift %d != %d", i, len(cr.Entries), len(base.Entries))
		}
		r.CandidateSHA256 = append(r.CandidateSHA256, mergeSHA(cand))
		changed := 0
		for j := range cr.Entries {
			if bytes.Equal(cr.Entries[j], base.Entries[j]) {
				continue
			}
			changed++
			if owners[j] > 0 {
				if !bytes.Equal(merged[j], cr.Entries[j]) {
					return nil, r, fmt.Errorf("GDAT entry %d modified differently by candidate %d and %d; rebuild sequentially", j, owners[j]-1, i)
				}
				r.DuplicateIdenticalEntries = append(r.DuplicateIdenticalEntries, j)
			} else {
				merged[j] = cr.Entries[j]
				owners[j] = i + 1
			}
		}
		if changed == 0 {
			return nil, r, fmt.Errorf("candidate %d is a no-op against pristine source", i)
		}
	}
	for j := range owners {
		if owners[j] > 0 {
			r.ChangedEntries = append(r.ChangedEntries, j)
		}
	}
	r.UnchangedEntries = len(base.Entries) - len(r.ChangedEntries)
	out, err := mskit.BuildGDAT(merged)
	if err != nil {
		return nil, r, fmt.Errorf("build merged GDAT: %w", err)
	}
	check, err := mskit.ParseGDAT(out)
	if err != nil {
		return nil, r, fmt.Errorf("reparse merged GDAT: %w", err)
	}
	if len(check.Entries) != len(base.Entries) {
		return nil, r, fmt.Errorf("merge entry-count drift")
	}
	for i := range check.Entries {
		if !bytes.Equal(check.Entries[i], merged[i]) || (owners[i] == 0 && !bytes.Equal(check.Entries[i], base.Entries[i])) {
			return nil, r, fmt.Errorf("merged DAT entry %d changed unexpectedly", i)
		}
	}
	r.OutputSHA256 = mergeSHA(out)
	return out, r, nil
}
