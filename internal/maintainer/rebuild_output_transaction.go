package maintainer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Generated outputs are isolated from the canonical semantic corpus and owner inputs.
// These four paths are intentionally scratch; nothing else under .local/build
// may be moved or deleted by this transaction. No concurrent maintainers.
var generatedOutputDirs = []string{"rebuilt", "reports", "graphics", "dist.staging"}
var archiveDatFilenameRE = regexp.MustCompile(`^GxArchivedFile[0-9]{3}\.dat$`)
var canonicalTextDatasets = map[string]bool{"MS2_PC_LOCALIZED": true, "MSF_PC_LOCALIZED": true, "MSF_PC_SYSTEM": true}

func safeBuildParent(root string) (string, error) {
	build := filepath.Join(root, ".local", "build")
	for _, p := range []string{root, filepath.Join(root, ".local"), build} {
		st, e := os.Lstat(p)
		if e != nil {
			return "", e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("refuse linked/non-directory build parent: %s", p)
		}
	}
	return build, nil
}

// Only known generated output shapes can be discarded at successful commit.
// Unrecognized manual files/symlinks fail closed instead of being swept away.
// scanGeneratedTree separates managed products from unknown legacy/owner data.
// Unknown paths are NEVER removed: older builders emitted many unregistered
// verification extracts, so guessing names is not a safe ownership policy.
// Direct symlinks remain prohibited; opaque historical overlays are retained.
func scanGeneratedTree(path, kind string, retainUnknown bool) ([]string, error) {
	unknown := []string{}

	st, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return unknown, nil
	}
	if e != nil {
		return nil, e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refuse linked/non-directory generated output: %s", path)
	}
	err := filepath.WalkDir(path, func(p string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == path {
			return nil
		}
		if de.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink in generated %s", p)
		}
		rel, e := filepath.Rel(path, p)
		if e != nil {
			return e
		}
		parts := strings.Split(filepath.ToSlash(rel), "/")
		isDir := de.IsDir()
		// Old read-only master views may contain multiple GiB of real DAT
		// hardlinks/symlinks.  They are OWNER DATA, not disposable scratch.
		// Preserve an exact legacy overlay directory as an opaque unit;
		// never walk its children or remove it with the old build generation.
		if kind == "rebuilt" && len(parts) == 2 &&
			canonicalTextDatasets[parts[0]] && parts[1] == "_go_verify_overlay" && isDir {
			if retainUnknown {
				unknown = append(unknown, rel)
				return filepath.SkipDir
			}
			return fmt.Errorf("unmanaged owner overlay in generated %s: %s", kind, rel)
		}
		ok := false
		switch kind {
		case "rebuilt":
			// Earlier releases placed temporary comparison extracts either
			// directly in the dataset output directory (the observed failure)
			// or in its masterWindows subdirectory. Both paths are generated
			// scratch, but *only* these two exact regular-file names qualify.
			isPriorDiffExtract := func(name string) bool {
				return name == "_go_diff_rebuilt.jsonl" || name == "_go_diff_source.jsonl"
			}
			switch len(parts) {
			case 1:
				ok = isDir && canonicalTextDatasets[parts[0]]
			case 2:
				ok = canonicalTextDatasets[parts[0]] && ((isDir && parts[1] == "masterWindows") || (!isDir && isPriorDiffExtract(parts[1])))
			case 3:
				ok = !isDir && canonicalTextDatasets[parts[0]] && parts[1] == "masterWindows" && (archiveDatFilenameRE.MatchString(parts[2]) || isPriorDiffExtract(parts[2]))
			}
		case "reports":
			if len(parts) == 1 && !isDir && strings.HasSuffix(parts[0], ".json") {
				ok = parts[0] == "graphics.json" || parts[0] == "graphics.backend.json"
				for ds := range canonicalTextDatasets {
					for _, suffix := range []string{".rebuild.json", ".verify.json", ".diff.json"} {
						if parts[0] == ds+suffix {
							ok = true
						}
					}
				}
			}
		case "graphics":
			if isDir {
				ok = len(parts) == 1 && (parts[0] == "previews" || strings.HasPrefix(parts[0], "ms-"))
			} else {
				ext := strings.ToLower(filepath.Ext(parts[len(parts)-1]))
				ok = ext == ".png" || ext == ".rgba" || ext == ".bc7" || ext == ".tid" || ext == ".tmp" || ext == ".dds" || ext == ".json" || ext == ".dxt5" || ext == ".bc3"
			}
		case "dist.staging":
			if len(parts) == 1 {
				ok = isDir && (parts[0] == "MS2_EN_FIDELITY_PATCH" || parts[0] == "MSF_EN_FIDELITY_PATCH")
			}
			if len(parts) == 2 {
				ok = (isDir && parts[1] == "masterWindows") || (!isDir && (parts[1] == "PATCH_MANIFEST.json" || parts[1] == "INSTALL.txt" || strings.HasSuffix(strings.ToLower(parts[1]), ".exe")))
			}
			if len(parts) == 3 {
				ok = !isDir && parts[1] == "masterWindows" && archiveDatFilenameRE.MatchString(parts[2])
			}
		}
		if !ok {
			if !retainUnknown {
				return fmt.Errorf("refuse unknown owner content in generated %s: %s", kind, rel)
			}
			unknown = append(unknown, rel)
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDir && !de.Type().IsRegular() {
			return fmt.Errorf("refuse nonregular generated file: %s", p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return unknown, nil
}

func checkGeneratedTree(path, kind string) error {
	_, err := scanGeneratedTree(path, kind, false)
	return err
}

type rebuildOutputTxn struct {
	build, stage string
	moved        map[string]bool
	// stage contains backups; new paths are created at their canonical location
}

func beginRebuildOutputTransaction(root string) (_ *rebuildOutputTxn, err error) {
	build, e := safeBuildParent(root)
	if e != nil {
		return nil, e
	}
	// Check every preexisting path BEFORE moving any output.
	for _, name := range generatedOutputDirs {
		if _, e = scanGeneratedTree(filepath.Join(build, name), name, true); e != nil {
			return nil, e
		}
	}
	stage, e := os.MkdirTemp(build, ".rebuild-transaction-")
	if e != nil {
		return nil, e
	}
	tx := &rebuildOutputTxn{build: build, stage: stage, moved: map[string]bool{}}
	for _, name := range generatedOutputDirs {
		path := filepath.Join(build, name)
		_, e = os.Lstat(path)
		if errors.Is(e, os.ErrNotExist) {
			// Missing generated trees are normal on a fresh or repeated
			// build. Do not carry os.ErrNotExist into post-loop handling.
			e = nil
			continue
		}
		if e != nil {
			break
		}
		if e = os.Rename(path, filepath.Join(stage, name)); e != nil {
			break
		}
		tx.moved[name] = true
	}
	if e != nil {
		restore := tx.rollback()
		if restore != nil {
			return nil, fmt.Errorf("staging generated outputs: %v; RESTORE FAILED: %v; preserved %s", e, restore, stage)
		}
		return nil, e
	}
	return tx, nil
}

func (tx *rebuildOutputTxn) rollback() error {
	// Never replace unexpected content or symlinks: quarantine all created outputs
	// inside the transaction before restoring the previous generation.
	var errs []error
	for _, name := range generatedOutputDirs {
		target := filepath.Join(tx.build, name)
		_, e := os.Lstat(target)
		if e == nil {
			quarantined := filepath.Join(tx.stage, "aborted-"+name)
			if e = os.Rename(target, quarantined); e != nil {
				errs = append(errs, fmt.Errorf("quarantine %s: %w", name, e))
				continue
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			errs = append(errs, e)
			continue
		}
		if tx.moved[name] {
			if e := os.Rename(filepath.Join(tx.stage, name), target); e != nil {
				errs = append(errs, fmt.Errorf("restore %s: %w", name, e))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	// Aborted new outputs were built in this run, never owner's previous outputs.
	return os.RemoveAll(tx.stage)
}

// All previously unregistered outputs, not just a growing list of _go_ names,
// are owner-retained.  Rename without dereferencing symlinks in an opaque
// historical overlay or copying multi-GiB hardlinked DATs. Known generated
// files are the only things that the successful transaction may delete.
func (tx *rebuildOutputTxn) preserveUnknownPriorOutputs() error {
	type retained struct{ from, to string }
	var targets []retained
	for _, kind := range generatedOutputDirs {
		if !tx.moved[kind] {
			continue
		}
		base := filepath.Join(tx.stage, kind)
		paths, err := scanGeneratedTree(base, kind, true)
		if err != nil {
			return err
		}
		for _, rel := range paths {
			targets = append(targets, retained{filepath.Join(base, rel), filepath.Join(kind, rel)})
		}
	}
	if len(targets) == 0 {
		return nil
	}
	archive, err := os.MkdirTemp(tx.build, ".preserved-unregistered-output-")
	if err != nil {
		return err
	}
	moved := make([]retained, 0, len(targets))
	rollback := func(cause error) error {
		var failures []error
		for i := len(moved) - 1; i >= 0; i-- {
			if e := os.Rename(moved[i].to, moved[i].from); e != nil {
				failures = append(failures, e)
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("retention: %w; RESTORE FAILED: %v; preserved %s", cause, errors.Join(failures...), archive)
		}
		_ = os.RemoveAll(archive)
		return cause
	}
	for _, item := range targets {
		destination := filepath.Join(archive, item.to)
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return rollback(err)
		}
		if err := os.Rename(item.from, destination); err != nil {
			return rollback(err)
		}
		moved = append(moved, retained{item.from, destination})
	}
	fmt.Printf("Preserved %d unregistered prior build entries without deletion or copying: %s\n", len(moved), archive)
	return nil
}

func (tx *rebuildOutputTxn) commit() error {
	// Verify old snapshots are precisely generated directories before deletion;
	// changes made by concurrent processes must not be silently overwritten.
	for _, name := range generatedOutputDirs {
		if tx.moved[name] {
			if _, err := scanGeneratedTree(filepath.Join(tx.stage, name), name, true); err != nil {
				return fmt.Errorf("retain previous generated output at %s: %w", tx.stage, err)
			}
		}
	}
	// A previous verifier left persistent master-DAT overlays inside rebuilt.
	// Preserve them OUTSIDE the disposable generation before deleting it.
	if err := tx.preserveUnknownPriorOutputs(); err != nil {
		return err
	}
	// At this point the prior game bundle is already published, and old scratch
	// is no longer needed. Delete only the audited prior generated trees.
	if err := os.RemoveAll(tx.stage); err != nil {
		return fmt.Errorf("new build published; old generated scratch retained at %s: %w", tx.stage, err)
	}
	return nil
}

func withGeneratedOutputTransaction(root string, work func() error) error {
	tx, e := beginRebuildOutputTransaction(root)
	if e != nil {
		return e
	}
	if e = work(); e != nil {
		if rb := tx.rollback(); rb != nil {
			return fmt.Errorf("rebuild failed: %v; SCRATCH RESTORE FAILED: %v; preserved %s", e, rb, tx.stage)
		}
		return e
	}
	return tx.commit()
}
