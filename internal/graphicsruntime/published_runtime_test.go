package graphicsruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishedRuntimeManifestFailClosed(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "toolchain", "python-runtime", "linux-amd64", "manifest.json")
	if err := VerifyPublishedRuntimeManifest(root, "linux", "amd64"); err == nil {
		t.Fatal("accepted missing public runtime")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPublishedRuntimeManifest(root, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "SHA mismatch") {
		t.Fatalf("accepted unpinned manifest: %v", err)
	}
	if err := VerifyPublishedRuntimeManifest(root, "freebsd", "amd64"); err == nil {
		t.Fatal("accepted unpinned OS")
	}
}
