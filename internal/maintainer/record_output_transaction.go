package maintainer

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// BuildRecords publishes the three generated JSONLs and change manifest as a
// rollback-protected generation. The original files are retained on every
// pre-publication failure; unknown/linked owner output is never overwritten.
func BuildRecords(root string, stdout io.Writer) (map[string]recordSummary, error) {
	return buildRecordsTransactional(root, stdout, nil)
}

// fault is a test-only simulated failure between the two publication renames.
func buildRecordsTransactional(root string, stdout io.Writer, fault func(string) error) (map[string]recordSummary, error) {
	build := filepath.Join(root, ".local", "build")
	for _, p := range []string{filepath.Join(root, ".local"), build} {
		st, err := os.Lstat(p)
		if err != nil {
			return nil, fmt.Errorf("record bridge parent %s: %w", p, err)
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("refuse linked/non-directory record build parent %s", p)
		}
	}
	records := filepath.Join(build, "records")
	manifest := filepath.Join(build, "change_manifest.json")
	had := [2]bool{}
	for i, p := range []string{records, manifest} {
		st, err := os.Lstat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		had[i] = true
		if i == 0 {
			if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("refuse linked/non-directory generated records %s", p)
			}
			entries, err := os.ReadDir(p)
			if err != nil {
				return nil, err
			}
			allow := map[string]bool{"MS2_PC_LOCALIZED.jsonl": true, "MSF_PC_LOCALIZED.jsonl": true, "MSF_PC_SYSTEM.jsonl": true}
			for _, e := range entries {
				if !allow[e.Name()] || !e.Type().IsRegular() {
					return nil, fmt.Errorf("refuse unknown/linked owner file in generated records: %s", e.Name())
				}
			}
		} else if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("refuse linked/nonregular generated manifest %s", p)
		}
	}
	stage, err := os.MkdirTemp(build, ".records-generation-*")
	if err != nil {
		return nil, err
	}
	retain := false
	defer func() {
		if !retain {
			_ = os.RemoveAll(stage)
		}
	}()
	stagedRecords := filepath.Join(stage, "generated")
	stagedManifest := filepath.Join(stage, "generated-manifest.json")
	summary, err := buildRecordsInto(root, stagedRecords, stagedManifest)
	if err != nil {
		return nil, err
	}
	// The source bridge is only valid for exactly the canonical three datasets.
	for _, ds := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"} {
		if _, ok := summary[ds]; !ok {
			return nil, fmt.Errorf("record generation omitted canonical dataset %s", ds)
		}
	}
	if len(summary) != 3 {
		return nil, fmt.Errorf("record generation produced %d datasets, want exactly three", len(summary))
	}
	// Genuine source checkouts include the frozen canonical semantic-record count ledger.
	// Validate against the STAGED change manifest before any previous generated
	// records are moved. Tiny unit fixtures omit that ledger deliberately.
	if _, statErr := os.Lstat(filepath.Join(root, "toolchain", "manifests", "canonical_counts.json")); statErr == nil {
		if err := validateChangeManifestCanonicalPath(root, stagedManifest); err != nil {
			return nil, fmt.Errorf("staged canonical record-count validation: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	targets := [2]string{records, manifest}
	staged := [2]string{stagedRecords, stagedManifest}
	backups := [2]string{filepath.Join(stage, "previous-records"), filepath.Join(stage, "previous-manifest.json")}
	movedOld := [2]bool{}
	movedNew := [2]bool{}
	rollback := func(cause error) (map[string]recordSummary, error) {
		var restoreErr error
		for i := 1; i >= 0; i-- {
			if movedNew[i] {
				if e := os.Rename(targets[i], staged[i]); e != nil && restoreErr == nil {
					restoreErr = e
				}
			}
			if movedOld[i] {
				if e := os.Rename(backups[i], targets[i]); e != nil && restoreErr == nil {
					restoreErr = e
				}
			}
		}
		if restoreErr != nil {
			retain = true
			return nil, fmt.Errorf("publish generated records: %v; RESTORE FAILED: %v; recovery=%s", cause, restoreErr, stage)
		}
		return nil, fmt.Errorf("publish generated records: %w", cause)
	}
	for i := 0; i < 2; i++ {
		if had[i] {
			if e := os.Rename(targets[i], backups[i]); e != nil {
				return rollback(e)
			}
			movedOld[i] = true
		}
		if e := os.Rename(staged[i], targets[i]); e != nil {
			return rollback(e)
		}
		movedNew[i] = true
		if fault != nil {
			if e := fault(targets[i]); e != nil {
				return rollback(e)
			}
		}
	}
	if stdout != nil {
		keys := make([]string, 0, len(summary))
		for k := range summary {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, ds := range keys {
			v := summary[ds]
			fmt.Fprintf(stdout, "%s: mapped=%d changed=%d\n", ds, v.MappedWorkingFields, v.ChangedFields)
		}
	}
	return summary, nil
}
