package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBatchRejectsExistingOutputBeforeExpensiveSourceProcessing(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "already.dat")
	sentinel := []byte("OWNER OUTPUT MUST STAY UNTOUCHED")
	if err := os.WriteFile(out, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	// Source is intentionally absent and has no GDAT structure. If rendering/
	// source-reading precedes the conflict guard, the observed error is wrong.
	sp := familyBatchSpec{Version: 1, ProjectRoot: ".", Jobs: []familyBatchJob{{Source: "missing.dat", SourceSHA256: strings.Repeat("0", 64), Output: out, Families: []string{finaleGridID}, Quality: 6, MaxAlpha: 64, MaxVisible: 64}}}
	raw, _ := json.Marshal(sp)
	manifest := filepath.Join(dir, "spec.json")
	_ = os.WriteFile(manifest, raw, 0600)
	_, err := RunSourceFamilyBatch(manifest)
	if err == nil || !strings.Contains(err.Error(), "output already exists") {
		t.Fatalf("no early output guard: %v", err)
	}
	after, _ := os.ReadFile(out)
	if string(after) != string(sentinel) {
		t.Fatal("owner output was changed")
	}
}
func TestBatchPreflightAcrossAllJobs(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "late.dat")
	_ = os.WriteFile(out, []byte("PRESERVE"), 0600)
	j := func(src, dst string) familyBatchJob {
		return familyBatchJob{Source: src, SourceSHA256: strings.Repeat("0", 64), Output: dst, Families: []string{finaleGridID}, Quality: 6, MaxAlpha: 64, MaxVisible: 64}
	}
	sp := familyBatchSpec{Version: 1, ProjectRoot: ".", Jobs: []familyBatchJob{j("missing1.dat", "candidate.dat"), j("missing2.dat", out)}}
	raw, _ := json.Marshal(sp)
	manifest := filepath.Join(dir, "spec.json")
	_ = os.WriteFile(manifest, raw, 0600)
	_, err := RunSourceFamilyBatch(manifest)
	if err == nil || !strings.Contains(err.Error(), "job 1 output already exists") {
		t.Fatalf("second output not checked before first render: %v", err)
	}
	if _, err = os.Lstat(filepath.Join(dir, "candidate.dat")); !os.IsNotExist(err) {
		t.Fatal("batch published first output")
	}
	sp.Jobs[1].Output = "missing1.dat"
	raw, _ = json.Marshal(sp)
	_ = os.WriteFile(manifest, raw, 0600)
	_, err = RunSourceFamilyBatch(manifest)
	if err == nil || !strings.Contains(err.Error(), "aliases another input") {
		t.Fatalf("cross-job source output alias accepted: %v", err)
	}
}
func TestBatchRejectsDanglingSymlinkDestination(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "out.dat")
	if err := os.Symlink("not-found", link); err != nil {
		t.Skip(err)
	}
	sp := familyBatchSpec{Version: 1, ProjectRoot: ".", Jobs: []familyBatchJob{{Source: "missing.dat", SourceSHA256: strings.Repeat("0", 64), Output: link, Families: []string{finaleGridID}, Quality: 6, MaxAlpha: 64, MaxVisible: 64}}}
	raw, _ := json.Marshal(sp)
	manifest := filepath.Join(dir, "spec.json")
	_ = os.WriteFile(manifest, raw, 0600)
	_, err := RunSourceFamilyBatch(manifest)
	if err == nil || !strings.Contains(err.Error(), "output already exists") {
		t.Fatalf("dangling owner symlink not protected: %v", err)
	}
}
