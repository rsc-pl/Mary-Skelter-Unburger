package maintainer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceRecordFixture(t *testing.T) (string, CachedSources, string) {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, ".local", "source_cache", "records")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	for _, ds := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"} {
		if err := os.WriteFile(filepath.Join(cache, ds+".jsonl"), []byte("NEW_"+ds), 0600); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(root, ".local", "build", "source_records")
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	for _, ds := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"} {
		if err := os.WriteFile(filepath.Join(old, ds+".jsonl"), []byte("OLD_"+ds), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root, CachedSources{Records: cache}, old
}

func assertSourceRecords(t *testing.T, out, prefix string) {
	t.Helper()
	for _, ds := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"} {
		b, err := os.ReadFile(filepath.Join(out, ds+".jsonl"))
		if err != nil || string(b) != prefix+ds {
			t.Fatalf("records %s changed: %q %v", ds, b, err)
		}
	}
}
func TestRecordStageCompleteAtomicRefresh(t *testing.T) {
	root, c, out := sourceRecordFixture(t)
	if err := prepareSourceRecords(root, c); err != nil {
		t.Fatal(err)
	}
	assertSourceRecords(t, out, "NEW_")
	matches, err := filepath.Glob(filepath.Join(root, ".local", "build", ".source-records-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("leftover stage/backup: %v %v", matches, err)
	}
}
func TestRecordStageMissingLateInputLeavesOld(t *testing.T) {
	root, c, out := sourceRecordFixture(t)
	if err := os.Remove(filepath.Join(c.Records, "MSF_PC_SYSTEM.jsonl")); err != nil {
		t.Fatal(err)
	}
	err := prepareSourceRecords(root, c)
	if err == nil || !strings.Contains(err.Error(), "missing cached source records") {
		t.Fatalf("expected validation failure: %v", err)
	}
	assertSourceRecords(t, out, "OLD_")
}
func TestRecordStageRejectsUnexpectedOwnerFileAndSymlinks(t *testing.T) {
	root, c, out := sourceRecordFixture(t)
	sentinel := filepath.Join(out, "owner.private")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	err := prepareSourceRecords(root, c)
	if err == nil || !strings.Contains(err.Error(), "noncanonical") {
		t.Fatalf("unknown owner file accepted: %v", err)
	}
	b, _ := os.ReadFile(sentinel)
	if string(b) != "preserve" {
		t.Fatal("owner file changed")
	}
	assertSourceRecords(t, out, "OLD_")
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(c.Records, "MSF_PC_SYSTEM.jsonl")
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(c.Records, "MSF_PC_LOCALIZED.jsonl"), src); err != nil {
		t.Skip("symlinks unavailable")
	}
	err = prepareSourceRecords(root, c)
	if err == nil || !strings.Contains(err.Error(), "nonregular/symlink") {
		t.Fatalf("source symlink accepted: %v", err)
	}
	assertSourceRecords(t, out, "OLD_")
}
func TestRecordStageRejectsLinkedDestinationWithoutFollowing(t *testing.T) {
	root, c, out := sourceRecordFixture(t)
	if err := os.RemoveAll(out); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "owner-data")
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	s := filepath.Join(outside, "keep")
	if err := os.WriteFile(s, []byte("owner"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, out); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := prepareSourceRecords(root, c); err == nil {
		t.Fatal("linked output accepted")
	}
	b, _ := os.ReadFile(s)
	if string(b) != "owner" {
		t.Fatal("linked owner data changed")
	}
}
func TestRecordStageSimulatedPublishFailureRestoresPrior(t *testing.T) {
	root, c, out := sourceRecordFixture(t)
	injected := errors.New("test injected publisher failure")
	err := prepareSourceRecordsWithHook(root, c, func() error { return injected })
	if !errors.Is(err, injected) {
		t.Fatalf("expected exact publish fault: %v", err)
	}
	assertSourceRecords(t, out, "OLD_")
	matches, _ := filepath.Glob(filepath.Join(root, ".local", "build", ".source-records-*"))
	if len(matches) != 0 {
		t.Fatalf("leftover stage/backup after recovered fault: %v", matches)
	}
}
