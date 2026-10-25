package maintainer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func outputTransactionFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	build := filepath.Join(root, ".local", "build")
	if e := os.MkdirAll(build, 0700); e != nil {
		t.Fatal(e)
	}
	return root, build
}
func writeTestOutput(t *testing.T, path, contents string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(contents), 0600); e != nil {
		t.Fatal(e)
	}
}
func readTestOutput(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func snapshotOldGeneratedOutputs(t *testing.T, build string) map[string]string {
	t.Helper()
	paths := map[string]string{
		"rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat": "old-DAT",
		"reports/MS2_PC_LOCALIZED.rebuild.json":                        "old-report",
		"graphics/previews/MS2_result_rendered.png":                    "old-preview",
		"dist.staging/MS2_EN_FIDELITY_PATCH/INSTALL.txt":               "old-staged-install",
	}
	for path, text := range paths {
		writeTestOutput(t, filepath.Join(build, path), text)
	}
	return paths
}
func TestRebuildOutputFailedLateRestoresFourPreviousTrees(t *testing.T) {
	root, build := outputTransactionFixture(t)
	old := snapshotOldGeneratedOutputs(t, build)
	sentinel := filepath.Join(build, "records", "MS2_PC_LOCALIZED.jsonl")
	writeTestOutput(t, sentinel, "frozen-record-bridge")
	err := withGeneratedOutputTransaction(root, func() error {
		for p := range old {
			writeTestOutput(t, filepath.Join(build, p), "new-generated")
		}
		return errors.New("late graphics encoder failure")
	})
	if err == nil || !strings.Contains(err.Error(), "late graphics") {
		t.Fatalf("bad failure: %v", err)
	}
	for p, want := range old {
		if got := readTestOutput(t, filepath.Join(build, p)); got != want {
			t.Fatalf("old scratch changed %s: %q", p, got)
		}
	}
	if got := readTestOutput(t, sentinel); got != "frozen-record-bridge" {
		t.Fatalf("record bridge damaged: %q", got)
	}
	entries, e := filepath.Glob(filepath.Join(build, ".rebuild-transaction-*"))
	if e != nil || len(entries) > 0 {
		t.Fatalf("stale transaction %v %v", entries, e)
	}
}
func TestRebuildOutputSuccessReplacesGeneratedOnly(t *testing.T) {
	root, build := outputTransactionFixture(t)
	old := snapshotOldGeneratedOutputs(t, build)
	writeTestOutput(t, filepath.Join(build, "change_manifest.json"), "frozen-canonical")
	if e := withGeneratedOutputTransaction(root, func() error {
		for p := range old {
			writeTestOutput(t, filepath.Join(build, p), "new-generated")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	for p := range old {
		if got := readTestOutput(t, filepath.Join(build, p)); got != "new-generated" {
			t.Fatalf("new scratch missing %s", p)
		}
	}
	if got := readTestOutput(t, filepath.Join(build, "change_manifest.json")); got != "frozen-canonical" {
		t.Fatalf("manifest damaged")
	}
}
func TestRebuildOutputRetainsUnknownOwnerContentAndRefusesSymlinks(t *testing.T) {
	for _, poison := range []string{"unknown", "symlink", "linked-parent"} {
		t.Run(poison, func(t *testing.T) {
			root, build := outputTransactionFixture(t)
			old := snapshotOldGeneratedOutputs(t, build)
			switch poison {
			case "unknown":
				writeTestOutput(t, filepath.Join(build, "reports", "owner-notes.txt"), "owner")
			case "symlink":
				if e := os.Symlink(filepath.Join(root, "secret"), filepath.Join(build, "graphics", "link")); e != nil {
					t.Fatal(e)
				}
			case "linked-parent":
				if e := os.RemoveAll(filepath.Join(build, "graphics")); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(root, filepath.Join(build, "graphics")); e != nil {
					t.Fatal(e)
				}
			}
			executed := false
			e := withGeneratedOutputTransaction(root, func() error { executed = true; return nil })
			if poison == "unknown" {
				if e != nil || !executed {
					t.Fatalf("failed safe preservation: %v", e)
				}
				retained, _ := filepath.Glob(filepath.Join(build, ".preserved-unregistered-output-*", "reports", "owner-notes.txt"))
				if len(retained) != 1 || readTestOutput(t, retained[0]) != "owner" {
					t.Fatalf("owner bytes lost: %v", retained)
				}
				return
			}
			if e == nil || executed {
				t.Fatalf("accepted unsafe linked content: %v", e)
			}
			if got := readTestOutput(t, filepath.Join(build, "rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat")); got != old["rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat"] {
				t.Fatalf("clobbered valid prior")
			}
		})
	}
}
func TestDistPublishFaultRestoresPriorAndProtectsRecoverySlot(t *testing.T) {
	root, build := outputTransactionFixture(t)
	old := filepath.Join(root, ".local", "dist", "MS2_EN_FIDELITY_PATCH", "INSTALL.txt")
	stage := filepath.Join(build, "dist.staging")
	newPath := filepath.Join(stage, "MS2_EN_FIDELITY_PATCH", "INSTALL.txt")
	writeTestOutput(t, old, "previous shipped bundle")
	writeTestOutput(t, newPath, "new bundle")
	if e := activateDistTransactional(root, stage, func() error { return errors.New("injected publish error") }); e == nil {
		t.Fatal("publication accepted injected failure")
	}
	if got := readTestOutput(t, old); got != "previous shipped bundle" {
		t.Fatalf("old dist not restored: %q", got)
	}
	if got := readTestOutput(t, newPath); got != "new bundle" {
		t.Fatalf("stage changed: %q", got)
	}
	if e := activateDist(root, stage); e != nil {
		t.Fatal(e)
	}
	if got := readTestOutput(t, old); got != "new bundle" {
		t.Fatalf("new dist not published: %q", got)
	}
	if _, e := os.Stat(stage); !os.IsNotExist(e) {
		t.Fatalf("stage still present %v", e)
	}
	writeTestOutput(t, filepath.Join(build, "dist.previous", "KEEP"), "owner-recovery")
	writeTestOutput(t, newPath, "another bundle")
	if e := activateDist(root, stage); e == nil {
		t.Fatal("accepted existing recovery slot")
	}
	if got := readTestOutput(t, filepath.Join(build, "dist.previous", "KEEP")); got != "owner-recovery" {
		t.Fatal("owner backup altered")
	}
	if got := readTestOutput(t, old); got != "new bundle" {
		t.Fatal("current dist altered")
	}
}
func TestDistPublishRefusesUntrackedOwnerFile(t *testing.T) {
	root, build := outputTransactionFixture(t)
	writeTestOutput(t, filepath.Join(root, ".local", "dist", "owner.notes"), "private")
	stage := filepath.Join(build, "dist.staging", "MS2_EN_FIDELITY_PATCH", "INSTALL.txt")
	writeTestOutput(t, stage, "new")
	if e := activateDist(root, filepath.Join(build, "dist.staging")); e == nil {
		t.Fatal("owner dist unknown overwritten")
	}
	if got := readTestOutput(t, filepath.Join(root, ".local", "dist", "owner.notes")); got != "private" {
		t.Fatal("unknown modified")
	}
}

// Regression: the previous successful builder run leaves its own text-diff
// extracts beside master DATs. Those exact generated files must not make the
// next run fail, and a failure must restore them intact.
func TestRebuildOutputAcceptsKnownPriorDiffExtracts(t *testing.T) {
	root, build := outputTransactionFixture(t)
	old := snapshotOldGeneratedOutputs(t, build)
	for _, name := range []string{"_go_diff_rebuilt.jsonl", "_go_diff_source.jsonl"} {
		path := filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED", "masterWindows", name)
		writeTestOutput(t, path, "previous-generated-"+name)
		old[filepath.Join("rebuilt", "MS2_PC_LOCALIZED", "masterWindows", name)] = "previous-generated-" + name
	}
	if err := withGeneratedOutputTransaction(root, func() error { return errors.New("simulated encoding error") }); err == nil {
		t.Fatal("expected rollback")
	}
	for p, want := range old {
		if got := readTestOutput(t, filepath.Join(build, p)); got != want {
			t.Fatalf("lost prior generated output %s", p)
		}
	}
	if err := withGeneratedOutputTransaction(root, func() error {
		writeTestOutput(t, filepath.Join(build, "rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat"), "new DAT")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"_go_diff_rebuilt.jsonl", "_go_diff_source.jsonl"} {
		path := filepath.Join(build, "rebuilt/MS2_PC_LOCALIZED/masterWindows", name)
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("obsolete diff extract was not retired: %s: %v", path, err)
		}
	}
}

func TestRebuildOutputPreservesUnknownExtractAndRejectsLinks(t *testing.T) {
	for _, kind := range []string{"unknown", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, build := outputTransactionFixture(t)
			dir := filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED", "masterWindows")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "_go_diff_owner.jsonl")
			if kind == "symlink" {
				path = filepath.Join(dir, "_go_diff_rebuilt.jsonl")
				if err := os.Symlink(filepath.Join(root, "owner"), path); err != nil {
					t.Fatal(err)
				}
			} else {
				writeTestOutput(t, path, "owner note")
			}
			executed := false
			err := withGeneratedOutputTransaction(root, func() error { executed = true; return nil })
			if kind == "unknown" {
				if err != nil || !executed {
					t.Fatalf("did not preserve unknown: %v", err)
				}
				retained, _ := filepath.Glob(filepath.Join(build, ".preserved-unregistered-output-*", "rebuilt", "MS2_PC_LOCALIZED", "masterWindows", "_go_diff_owner.jsonl"))
				if len(retained) != 1 || readTestOutput(t, retained[0]) != "owner note" {
					t.Fatalf("missing retained owner bytes: %v", retained)
				}
			} else if err == nil || executed {
				t.Fatalf("accepted symlink: %v", err)
			}
		})
	}
}

// Check the historical extracts in BOTH locations; a prior regression test
// accidentally covered masterWindows only, while real builds left these
// files directly below the dataset directory.
func TestRebuildOutputPriorDiffExtractsAllDatasetLayouts(t *testing.T) {
	for ds := range canonicalTextDatasets {
		for _, placement := range []string{"dataset", "masterWindows"} {
			t.Run(ds+"/"+placement, func(t *testing.T) {
				root, build := outputTransactionFixture(t)
				old := snapshotOldGeneratedOutputs(t, build)
				for _, name := range []string{"_go_diff_rebuilt.jsonl", "_go_diff_source.jsonl"} {
					pieces := []string{"rebuilt", ds}
					if placement == "masterWindows" {
						pieces = append(pieces, "masterWindows")
					}
					rel := filepath.Join(append(pieces, name)...)
					old[rel] = "previous-diff-" + ds + "-" + name
					writeTestOutput(t, filepath.Join(build, rel), old[rel])
				}
				if err := withGeneratedOutputTransaction(root, func() error {
					return errors.New("synthetic verifier failure")
				}); err == nil {
					t.Fatal("expected abort")
				}
				for rel, expected := range old {
					if got := readTestOutput(t, filepath.Join(build, rel)); got != expected {
						t.Fatalf("previous file was not restored %s: %q", rel, got)
					}
				}
				if err := withGeneratedOutputTransaction(root, func() error {
					writeTestOutput(t, filepath.Join(build, "rebuilt", ds, "masterWindows", "GxArchivedFile055.dat"), "rebuilt")
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for rel := range old {
					if !strings.HasPrefix(rel, "rebuilt/"+ds+"/") || !strings.HasSuffix(rel, ".jsonl") {
						continue
					}
					if _, err := os.Lstat(filepath.Join(build, rel)); !os.IsNotExist(err) {
						t.Fatalf("old generated extract not retired: %s: %v", rel, err)
					}
				}
			})
		}
	}
}

func TestRebuildOutputDatasetUnknownRetainedAndSymlinksRefused(t *testing.T) {
	for _, mode := range []string{"unknown", "linked"} {
		t.Run(mode, func(t *testing.T) {
			root, build := outputTransactionFixture(t)
			dir := filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "_go_diff_other.jsonl")
			if mode == "linked" {
				path = filepath.Join(dir, "_go_diff_rebuilt.jsonl")
				if err := os.Symlink(filepath.Join(root, "owner-data"), path); err != nil {
					t.Fatal(err)
				}
			} else {
				writeTestOutput(t, path, "OWNER CONTENT")
			}
			called := false
			err := withGeneratedOutputTransaction(root, func() error { called = true; return nil })
			if mode == "unknown" {
				if err != nil || !called {
					t.Fatalf("unexpected preservation failure: %v", err)
				}
				retained, _ := filepath.Glob(filepath.Join(build, ".preserved-unregistered-output-*", "rebuilt", "MS2_PC_LOCALIZED", "_go_diff_other.jsonl"))
				if len(retained) != 1 || readTestOutput(t, retained[0]) != "OWNER CONTENT" {
					t.Fatalf("lost unknown content: %v", retained)
				}
			} else {
				if err == nil || called {
					t.Fatalf("unsafe transaction: %v", err)
				}
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("input was modified: %v", err)
				}
			}
		})
	}
}

// Historical _go_verify_overlay directories are owner-retained master views,
// sometimes made of hardlinks or symlinks to multi-GiB pristine DATs. A new
// build must never discard them alongside disposable rebuilt scratch.
func TestRebuildPreservesHistoricalVerificationOverlayWithoutCopy(t *testing.T) {
	root, build := outputTransactionFixture(t)
	old := snapshotOldGeneratedOutputs(t, build)
	master := filepath.Join(root, "source-dat", "GxArchivedFile052.dat")
	writeTestOutput(t, master, "pristine-owner-dat")
	for _, dataset := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED"} {
		view := filepath.Join(build, "rebuilt", dataset, "_go_verify_overlay")
		if err := os.MkdirAll(view, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(master, filepath.Join(view, "GxArchivedFile052.dat")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(master, filepath.Join(view, "GxArchivedFile053.dat")); err != nil {
			t.Fatal(err)
		}
		writeTestOutput(t, filepath.Join(view, "owner-note.bin"), "keep every byte")
	}
	if err := withGeneratedOutputTransaction(root, func() error {
		writeTestOutput(t, filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED", "masterWindows", "GxArchivedFile055.dat"), "new-DAT")
		return errors.New("simulated failure")
	}); err == nil {
		t.Fatal("expected failure")
	}
	for rel, expected := range old {
		if got := readTestOutput(t, filepath.Join(build, rel)); got != expected {
			t.Fatalf("rollback changed %s", rel)
		}
	}
	for _, dataset := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED"} {
		if got := readTestOutput(t, filepath.Join(build, "rebuilt", dataset, "_go_verify_overlay", "owner-note.bin")); got != "keep every byte" {
			t.Fatal(got)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(build, ".preserved-unregistered-output-*")); len(matches) != 0 {
		t.Fatalf("failed build left preserved dirs: %v", matches)
	}
	if err := withGeneratedOutputTransaction(root, func() error {
		writeTestOutput(t, filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED", "masterWindows", "GxArchivedFile055.dat"), "new-DAT")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	places, err := filepath.Glob(filepath.Join(build, ".preserved-unregistered-output-*"))
	if err != nil || len(places) != 1 {
		t.Fatalf("expected one permanent preservation directory: %v, %v", places, err)
	}
	for _, dataset := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED"} {
		view := filepath.Join(places[0], "rebuilt", dataset, "_go_verify_overlay")
		fi, err := os.Lstat(filepath.Join(view, "GxArchivedFile052.dat"))
		if err != nil {
			t.Fatal(err)
		}
		masterFi, err := os.Stat(master)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(fi, masterFi) {
			t.Fatal("historical DAT was copied, not renamed")
		}
		fi, err = os.Lstat(filepath.Join(view, "GxArchivedFile053.dat"))
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatal("historic symlink not preserved")
		}
		if got := readTestOutput(t, filepath.Join(view, "owner-note.bin")); got != "keep every byte" {
			t.Fatal(got)
		}
		if _, err := os.Lstat(filepath.Join(build, "rebuilt", dataset, "_go_verify_overlay")); !os.IsNotExist(err) {
			t.Fatalf("old legacy overlay left in regenerated directory: %v", err)
		}
	}
	if got := readTestOutput(t, filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED", "masterWindows", "GxArchivedFile055.dat")); got != "new-DAT" {
		t.Fatal(got)
	}
	if got := readTestOutput(t, master); got != "pristine-owner-dat" {
		t.Fatal("source DAT changed")
	}
	if err := withGeneratedOutputTransaction(root, func() error {
		writeTestOutput(t, filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED", "masterWindows", "GxArchivedFile055.dat"), "newer-DAT")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := readTestOutput(t, filepath.Join(places[0], "rebuilt", "MS2_PC_LOCALIZED", "_go_verify_overlay", "owner-note.bin")); got != "keep every byte" {
		t.Fatal("preserved bytes lost on subsequent build")
	}
}

func TestRebuildHistoricalVerificationOverlayRootSymlinkAndUnknownRefused(t *testing.T) {
	for _, failure := range []string{"overlay-symlink", "unknown-directory"} {
		t.Run(failure, func(t *testing.T) {
			root, build := outputTransactionFixture(t)
			old := snapshotOldGeneratedOutputs(t, build)
			dataset := filepath.Join(build, "rebuilt", "MS2_PC_LOCALIZED")
			if failure == "overlay-symlink" {
				if err := os.Symlink(root, filepath.Join(dataset, "_go_verify_overlay")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeTestOutput(t, filepath.Join(dataset, "owner-extra", "file.dat"), "owner")
			}
			called := false
			err := withGeneratedOutputTransaction(root, func() error { called = true; return nil })
			if failure == "overlay-symlink" {
				if err == nil || called {
					t.Fatalf("unsafe begin: %v called=%t", err, called)
				}
				for path, want := range old {
					if got := readTestOutput(t, filepath.Join(build, path)); got != want {
						t.Fatalf("modified %s", path)
					}
				}
			} else {
				if err != nil || !called {
					t.Fatalf("failed to preserve unknown dir: %v", err)
				}
				retained, _ := filepath.Glob(filepath.Join(build, ".preserved-unregistered-output-*", "rebuilt", "MS2_PC_LOCALIZED", "owner-extra", "file.dat"))
				if len(retained) != 1 || readTestOutput(t, retained[0]) != "owner" {
					t.Fatalf("missing retained directory: %v", retained)
				}
			}
		})
	}
}

// Systemic regression: rebuild must never reject its earlier verifier's own
// arbitrary scratch extracts. Preserve everything not provably generated,
// including nested unknown directories and legacy linked master views.
func TestRebuildLegacyMixedOutputsPreservedAndRepeatedBuilds(t *testing.T) {
	root, build := outputTransactionFixture(t)
	old := snapshotOldGeneratedOutputs(t, build)
	extras := []string{
		"rebuilt/MS2_PC_LOCALIZED/_go_verify_source_records.jsonl",
		"rebuilt/MS2_PC_LOCALIZED/_go_verify_records.jsonl",
		"rebuilt/MSF_PC_LOCALIZED/_go_a_future_verifier_artifact.jsonl",
		"rebuilt/MSF_PC_SYSTEM/masterWindows/_unregistered_extra.bin",
		"rebuilt/MS2_PC_LOCALIZED/owner-owned/dummy.dat",
		"reports/unknown-owner-report.log",
		"graphics/user-extras/custom.bin",
	}
	for _, rel := range extras {
		writeTestOutput(t, filepath.Join(build, rel), "preserve:"+rel)
	}
	for _, rel := range extras {
		old[rel] = "preserve:" + rel
	}
	if err := withGeneratedOutputTransaction(root, func() error {
		writeTestOutput(t, filepath.Join(build, "rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat"), "wrong-new")
		return errors.New("synthetic text verification failure")
	}); err == nil {
		t.Fatal("rollback did not fail")
	}
	for rel, want := range old {
		if got := readTestOutput(t, filepath.Join(build, rel)); got != want {
			t.Fatalf("rollback lost %s", rel)
		}
	}
	if err := withGeneratedOutputTransaction(root, func() error {
		writeTestOutput(t, filepath.Join(build, "rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat"), "fresh-DAT")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	retained, _ := filepath.Glob(filepath.Join(build, ".preserved-unregistered-output-*"))
	if len(retained) != 1 {
		t.Fatalf("retention archives %v", retained)
	}
	for _, rel := range extras {
		if got := readTestOutput(t, filepath.Join(retained[0], rel)); got != "preserve:"+rel {
			t.Fatalf("unknown lost %s", rel)
		}
		if _, err := os.Lstat(filepath.Join(build, rel)); !os.IsNotExist(err) {
			t.Fatalf("old extra still active %s: %v", rel, err)
		}
	}
	if err := withGeneratedOutputTransaction(root, func() error { return nil }); err != nil {
		t.Fatalf("third build still blocked: %v", err)
	}
	for _, rel := range extras {
		if got := readTestOutput(t, filepath.Join(retained[0], rel)); got != "preserve:"+rel {
			t.Fatalf("retention corrupted %s", rel)
		}
	}
}
