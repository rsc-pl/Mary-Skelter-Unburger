package maintainer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Abrupt process termination or power loss bypasses deferred rollback.
// Refuse to start another build when any prior rollback/publication slot still
// exists, and hold an exclusive directory lock across source preparation,
// records, graphics, packaging and final dist publication. The lock itself is
// retained after a crash and must NEVER be removed by an automatic retry.
// This gate does NOT purport to make filesystem renames power-loss atomic.
const rebuildLockDir = ".rebuild-active"
const rebuildLockNoteName = "RECOVERY_NOTE.txt"

func unfinishedBuildTransactions(build string) ([]string, error) {
	entries, err := os.ReadDir(build)
	if err != nil {
		return nil, err
	}
	var pending []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".rebuild-transaction-") ||
			strings.HasPrefix(name, ".dist-publish-") ||
			strings.HasPrefix(name, ".rebuild-active-") ||
			name == "dist.previous" {
			pending = append(pending, filepath.Join(build, name))
		}
	}
	return pending, nil
}

func withExclusiveRebuild(root string, work func() error) (result error) {
	build, err := safeBuildParent(root)
	if err != nil {
		return err
	}
	lock := filepath.Join(build, rebuildLockDir)
	// Atomic Mkdir arbitrates concurrent processes on the same local FS.
	// Never interpret an existing lock as ours; it may be the only crash clue.
	if err = os.Mkdir(lock, 0700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("REFUSE concurrent/interrupted build: lock %s exists; inspect the owner note and recovery slots before any manual action", lock)
		}
		return err
	}
	note := fmt.Sprintf("Mary Skelter rebuild lock\nPID: %d\nUTC: %s\n\nIf this process is still running, do not start another builder.\nIf it crashed, inspect every .rebuild-transaction-* and .dist-publish-* under .local/build and the old/new dist before removing this lock manually.\nNever delete owner data or transaction folders without a verified recovery plan.\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
	notePath := filepath.Join(lock, rebuildLockNoteName)
	if err = os.WriteFile(notePath, []byte(note), 0600); err != nil {
		// Only our just-created empty directory can be removed here.
		_ = os.Remove(lock)
		return fmt.Errorf("cannot write exclusive build lock note: %w", err)
	}
	defer func() {
		// Avoid deleting an unexpected user replacement of our lock/note.
		data, readErr := os.ReadFile(notePath)
		if readErr != nil || string(data) != note {
			result = errors.Join(result, fmt.Errorf("build lock changed; retained for inspection: %s", lock))
			return
		}
		if err := os.Remove(notePath); err != nil {
			result = errors.Join(result, fmt.Errorf("cannot remove lock note; preserved %s: %w", lock, err))
			return
		}
		if err := os.Remove(lock); err != nil {
			result = errors.Join(result, fmt.Errorf("cannot release build lock; preserved %s: %w", lock, err))
		}
	}()

	pending, err := unfinishedBuildTransactions(build)
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return fmt.Errorf("REFUSE unfinished prior rebuild/publication recovery paths (nothing modified): %s", strings.Join(pending, ", "))
	}
	return work()
}
