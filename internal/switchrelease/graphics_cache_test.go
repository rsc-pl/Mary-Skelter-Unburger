package switchrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreserveCanonicalGraphicsExplicitReuse(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "stage.zip")
	if err := os.WriteFile(src, []byte("verified canonical bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	first, pin, err := preserveCanonicalGraphics(root, src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(first, pin+".zip") || len(pin) != 64 {
		t.Fatalf("unexpected cache %s / %s", first, pin)
	}
	second, secondPin, err := preserveCanonicalGraphics(root, src)
	if err != nil || second != first || secondPin != pin {
		t.Fatalf("non-idempotent cache %q/%q: %v", second, secondPin, err)
	}
	// The staged workdir may vanish, but its hardlinked content remains.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if actual, err := sumFile(first); err != nil || actual != pin {
		t.Fatalf("cache bytes lost: %s %v", actual, err)
	}
}

func TestPreserveCanonicalGraphicsRefusesSymlinkParent(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "canonical.zip")
	if err := os.WriteFile(src, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, ".local")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := preserveCanonicalGraphics(root, src); err == nil {
		t.Fatal("accepted symlink parent")
	}
}

func TestPreserveCanonicalGraphicsRefusesModifiedCachedArtifact(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "canonical.zip")
	if err := os.WriteFile(src, []byte("a"), 0600); err != nil {
		t.Fatal(err)
	}
	dest, _, err := preserveCanonicalGraphics(root, src)
	if err != nil {
		t.Fatal(err)
	}
	// Replacing a stored cache with arbitrary content must not be accepted.
	if err := os.Remove(dest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := preserveCanonicalGraphics(root, src); err == nil {
		t.Fatal("accepted tampered cache")
	}
}
