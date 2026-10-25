package switchrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateCacheImportNoClobber(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "staged.dat")
	target := filepath.Join(dir, "original.dat")
	good := []byte("GDAT verified master bytes")
	if err := os.WriteFile(source, good, 0600); err != nil {
		t.Fatal(err)
	}
	want := checksumBytes(good)
	if err := publishPinnedPrivateCache(source, target, want); err != nil {
		t.Fatal(err)
	}
	if err := publishPinnedPrivateCache(source, target, want); err != nil {
		t.Fatalf("idempotent cache import blocked: %v", err)
	}
	// The importer removes its temporary staging name after publishing; otherwise
	// a hardlink correctly aliases the same inode and this is not an overwrite test.
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("different stage"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishPinnedPrivateCache(source, target, checksumBytes([]byte("different stage"))); err == nil || !strings.Contains(err.Error(), "differing SHA") {
		t.Fatalf("existing valid snapshot overwritten: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(good) {
		t.Fatalf("cache content overwritten: %v", err)
	}
}

func TestPrivateCacheRejectsSymlinkAndCorruptManifest(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "owner-original")
	if err := os.WriteFile(original, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "manifest.json")
	if err := os.Symlink(original, bad); err != nil {
		t.Fatal(err)
	}
	if err := writePinnedPrivateCacheManifest(bad, []byte("new manifest")); err == nil || !strings.Contains(err.Error(), "not regular") {
		t.Fatalf("symlink destination accepted: %v", err)
	}
	if b, err := os.ReadFile(original); err != nil || string(b) != "secret" {
		t.Fatalf("original overwritten: %v", err)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("stale manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writePinnedPrivateCacheManifest(bad, []byte("new manifest")); err == nil || !strings.Contains(err.Error(), "differing SHA") {
		t.Fatalf("corrupt manifest overwritten: %v", err)
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := writePinnedPrivateCacheManifest(bad, []byte("new manifest")); err != nil {
		t.Fatal(err)
	}
	if err := writePinnedPrivateCacheManifest(bad, []byte("new manifest")); err != nil {
		t.Fatalf("identical manifest not idempotent: %v", err)
	}
}
