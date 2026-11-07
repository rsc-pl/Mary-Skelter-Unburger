package builderui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionReloadedFromProjectBeforeRelease(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "version.txt")
	if err := os.WriteFile(path, []byte("0.4.2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stale := Options{Root: root, Version: "0.4.2"}
	if err := os.WriteFile(path, []byte("0.4.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	current, err := refreshReleaseVersion(stale)
	if err != nil || current.Version != "0.4.3" {
		t.Fatalf("version reload got %q, %v", current.Version, err)
	}
	if err := requireCurrentReleaseVersion(stale); err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatalf("stale version should stop before heavy build, got %v", err)
	}
	if err := requireCurrentReleaseVersion(current); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("../bad\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := refreshReleaseVersion(current); err == nil {
		t.Fatal("invalid version accepted")
	}
}
