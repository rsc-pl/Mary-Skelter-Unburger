package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArchivePathGuard(t *testing.T) {
	for _, bad := range []string{"", "/abs", "../escape", "a/../b", "a//b", "C:\\evil", ".", "a/./b"} {
		if safeRel(bad) {
			t.Errorf("allowed unsafe archive name %q", bad)
		}
	}
	for _, good := range []string{"manifest.json", "python/bin/python3", "site-packages/PIL/__init__.py"} {
		if !safeRel(good) {
			t.Errorf("refused safe path %q", good)
		}
	}
}
func TestInvalidBundledChunkIsRejected(t *testing.T) {
	r := t.TempDir()
	dir := bundleDir(r)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-amd64.json"), []byte(`{"schema":1,"platform":"linux-amd64","pieces":[{"file":"linux-amd64.part001","sha256":"wrong","size":5}]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-amd64.part001"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifyParts(r, "linux-amd64"); err == nil {
		t.Fatal("accepted forged SHA")
	}
	if err := restore(r, "linux-amd64"); err == nil {
		t.Fatal("restored forged runtime")
	}
	if _, err := os.Stat(runtimeDir(r, "linux-amd64")); !os.IsNotExist(err) {
		t.Fatalf("invalid runtime installed: %v", err)
	}
}
