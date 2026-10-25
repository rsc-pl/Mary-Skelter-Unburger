package maintainer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverySlotsRefuseBeforeWrite(t *testing.T) {
	for _, name := range []string{
		".rebuild-transaction-interrupted", ".dist-publish-interrupted",
		".rebuild-active-previous", ".rebuild-transaction-legacy", "dist.previous",
	} {
		t.Run(name, func(t *testing.T) {
			root, build := outputTransactionFixture(t)
			old := snapshotOldGeneratedOutputs(t, build)
			recovery := filepath.Join(build, name)
			writeTestOutput(t, filepath.Join(recovery, "OWNER-SAVED.txt"), "unique prior recovery")
			called := false
			err := withExclusiveRebuild(root, func() error { called = true; return nil })
			if err == nil || !strings.Contains(err.Error(), recovery) || called {
				t.Fatalf("failed to refuse recovery %q: %v", recovery, err)
			}
			if got := readTestOutput(t, filepath.Join(recovery, "OWNER-SAVED.txt")); got != "unique prior recovery" {
				t.Fatal("recovery data touched")
			}
			if got := readTestOutput(t, filepath.Join(build, "rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat")); got != old["rebuilt/MS2_PC_LOCALIZED/masterWindows/GxArchivedFile055.dat"] {
				t.Fatal("old DAT touched")
			}
			if _, err := os.Lstat(filepath.Join(build, rebuildLockDir)); !os.IsNotExist(err) {
				t.Fatalf("lock left after ordinary refusal: %v", err)
			}
		})
	}
}

func TestSingleWriterLockCoversEntireWorkAndReleases(t *testing.T) {
	root, build := outputTransactionFixture(t)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error)
	go func() { done <- withExclusiveRebuild(root, func() error { close(started); <-release; return nil }) }()
	<-started
	info, err := os.ReadFile(filepath.Join(build, rebuildLockDir, rebuildLockNoteName))
	if err != nil || !strings.Contains(string(info), "PID:") {
		t.Fatalf("missing lock note: %v", err)
	}
	called := false
	err = withExclusiveRebuild(root, func() error { called = true; return nil })
	if err == nil || called || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("concurrent builder admitted: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := withExclusiveRebuild(root, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(build, rebuildLockDir)); !os.IsNotExist(err) {
		t.Fatalf("released lock remains %v", err)
	}
}

func TestFailureReleasesOwnLockAndPreservesUnknown(t *testing.T) {
	root, build := outputTransactionFixture(t)
	writeTestOutput(t, filepath.Join(build, "owner-private.txt"), "unchanged")
	err := withExclusiveRebuild(root, func() error { return errors.New("simulated graphics failure") })
	if err == nil || !strings.Contains(err.Error(), "simulated") {
		t.Fatalf("lost failure: %v", err)
	}
	if got := readTestOutput(t, filepath.Join(build, "owner-private.txt")); got != "unchanged" {
		t.Fatal("owner data touched")
	}
	if _, err := os.Lstat(filepath.Join(build, rebuildLockDir)); !os.IsNotExist(err) {
		t.Fatalf("lock retained after normal failure: %v", err)
	}
}

func TestExistingInterruptedLockRefusesWithoutDeleting(t *testing.T) {
	root, build := outputTransactionFixture(t)
	lock := filepath.Join(build, rebuildLockDir)
	writeTestOutput(t, filepath.Join(lock, rebuildLockNoteName), "old interrupted process evidence")
	if err := withExclusiveRebuild(root, func() error { t.Fatal("must not execute"); return nil }); err == nil {
		t.Fatal("accepted stale lock")
	}
	if got := readTestOutput(t, filepath.Join(lock, rebuildLockNoteName)); got != "old interrupted process evidence" {
		t.Fatal("deleted crash evidence")
	}
}

func TestLockNoteCannotBeSilentlyDiscardedIfAltered(t *testing.T) {
	root, build := outputTransactionFixture(t)
	err := withExclusiveRebuild(root, func() error {
		return os.WriteFile(filepath.Join(build, rebuildLockDir, rebuildLockNoteName), []byte("owner revised"), 0600)
	})
	if err == nil || !strings.Contains(err.Error(), "lock changed") {
		t.Fatalf("silently discarded altered lock: %v", err)
	}
	if got := readTestOutput(t, filepath.Join(build, rebuildLockDir, rebuildLockNoteName)); got != "owner revised" {
		t.Fatal("note modified")
	}
}
