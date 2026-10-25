package maintainer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var canonicalRecordDatasetNames = []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"}

func recordGenerationFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	build := filepath.Join(root, ".local", "build")
	raw := filepath.Join(build, "source_records")
	records := filepath.Join(build, "records")
	for _, p := range []string{raw, records} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, ds := range canonicalRecordDatasetNames {
		if err := os.WriteFile(filepath.Join(raw, ds+".jsonl"), []byte(`{"record_key":"`+ds+`#1","text":"OLD"}`+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(records, ds+".jsonl"), []byte("PREVIOUS-"+ds), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(build, "change_manifest.json")
	if err := os.WriteFile(manifest, []byte("PREVIOUS MANIFEST"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, records, manifest
}
func assertPreviousRecords(t *testing.T, records, manifest string) {
	t.Helper()
	for _, ds := range canonicalRecordDatasetNames {
		b, err := os.ReadFile(filepath.Join(records, ds+".jsonl"))
		if err != nil || string(b) != "PREVIOUS-"+ds {
			t.Fatalf("previous record lost %s: %q %v", ds, b, err)
		}
	}
	b, err := os.ReadFile(manifest)
	if err != nil || string(b) != "PREVIOUS MANIFEST" {
		t.Fatalf("previous manifest lost %q %v", b, err)
	}
}
func assertNoGeneratedScratch(t *testing.T, root string) {
	t.Helper()
	a, _ := filepath.Glob(filepath.Join(root, ".local", "build", ".records-generation-*"))
	if len(a) != 0 {
		t.Fatalf("scratch left behind: %v", a)
	}
}
func TestBuildRecordsPublishesCompleteGeneration(t *testing.T) {
	root, records, manifest := recordGenerationFixture(t)
	out, err := BuildRecords(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("unexpected summaries %v", out)
	}
	for _, ds := range canonicalRecordDatasetNames {
		b, err := os.ReadFile(filepath.Join(records, ds+".jsonl"))
		if err != nil || !strings.Contains(string(b), `"record_key":"`+ds+`#1"`) {
			t.Fatalf("not published: %s %s %v", ds, b, err)
		}
	}
	b, err := os.ReadFile(manifest)
	if err != nil || !strings.Contains(string(b), `"MSF_PC_SYSTEM"`) {
		t.Fatalf("missing new manifest %s %v", b, err)
	}
	assertNoGeneratedScratch(t, root)
}
func TestLateInputErrorKeepsWholeGeneration(t *testing.T) {
	root, records, manifest := recordGenerationFixture(t)
	p := filepath.Join(root, ".local", "build", "source_records", "MSF_PC_SYSTEM.jsonl")
	if err := os.WriteFile(p, []byte("NOT JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecords(root, nil); err == nil {
		t.Fatal("invalid last source accepted")
	}
	assertPreviousRecords(t, records, manifest)
	assertNoGeneratedScratch(t, root)
}
func TestUnknownOutputAndSymlinkRefused(t *testing.T) {
	root, records, manifest := recordGenerationFixture(t)
	sentinel := filepath.Join(records, "owner.txt")
	if err := os.WriteFile(sentinel, []byte("must survive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecords(root, nil); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown owner file not blocked: %v", err)
	}
	assertPreviousRecords(t, records, manifest)
	b, _ := os.ReadFile(sentinel)
	if string(b) != "must survive" {
		t.Fatal("owner file altered")
	}
	if err := os.Remove(sentinel); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "owner-secret")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, manifest); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := BuildRecords(root, nil); err == nil || !strings.Contains(err.Error(), "linked") {
		t.Fatalf("manifest symlink accepted: %v", err)
	}
	b, _ = os.ReadFile(target)
	if string(b) != "secret" {
		t.Fatal("symlink target altered")
	}
	assertNoGeneratedScratch(t, root)
}
func TestPublishFailureAfterRecordsRestoresBothOutputs(t *testing.T) {
	root, records, manifest := recordGenerationFixture(t)
	injected := errors.New("legacy injected failure")
	_, err := buildRecordsTransactional(root, nil, func(p string) error {
		if p == records {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("fault was not propagated: %v", err)
	}
	assertPreviousRecords(t, records, manifest)
	assertNoGeneratedScratch(t, root)
}
func TestPublishFailureAfterManifestRestoresBothOutputs(t *testing.T) {
	root, records, manifest := recordGenerationFixture(t)
	injected := errors.New("legacy injected manifest failure")
	_, err := buildRecordsTransactional(root, nil, func(p string) error {
		if p == manifest {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("fault was not propagated: %v", err)
	}
	assertPreviousRecords(t, records, manifest)
	assertNoGeneratedScratch(t, root)
}
func TestNoExistingGeneratedOutputs(t *testing.T) {
	root, records, manifest := recordGenerationFixture(t)
	if err := os.RemoveAll(records); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecords(root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(records, "MSF_PC_SYSTEM.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatal(err)
	}
	assertNoGeneratedScratch(t, root)
}

func TestStagedCanonicalCountMismatchPreservesPrior(t *testing.T) {
	root, records, manifest := recordGenerationFixture(t)
	p := filepath.Join(root, "toolchain", "manifests")
	if err := os.MkdirAll(p, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "canonical_counts.json"), []byte(`{"expected_changed_fields_by_dataset":{"MS2_PC_LOCALIZED":64689}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRecords(root, nil); err == nil || !strings.Contains(err.Error(), "canonical record-count") {
		t.Fatalf("canonical drift not blocked: %v", err)
	}
	assertPreviousRecords(t, records, manifest)
	assertNoGeneratedScratch(t, root)
}
