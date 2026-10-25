package graphicsruntime

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSmokePinnedRuntimeWhenInstalled exercises the exact published runtime.
// Source-only distributions intentionally omit its immutable binary payload.
// An explicit test root allows isolated source QA to check the owner's
// separately installed runtime without copying it into the source snapshot.
func TestSmokePinnedRuntimeWhenInstalled(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	external := os.Getenv("MS_GRAPHICS_RUNTIME_TEST_ROOT")
	if external != "" {
		if !filepath.IsAbs(external) {
			t.Fatal("runtime test root must be absolute")
		}
		root = external
	}
	location := filepath.Join(root, "toolchain", "python-runtime", runtime.GOOS+"-"+runtime.GOARCH)
	info, err := os.Lstat(location)
	if errors.Is(err, os.ErrNotExist) && external == "" {
		t.Skip("immutable public runtime is intentionally excluded from the source archive")
	}
	if err != nil {
		t.Fatalf("public runtime path: %v", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("public runtime directory must be real, not a symlink")
	}
	py, err := Resolve(root)
	if err != nil {
		t.Fatalf("published runtime validation failed: %v", err)
	}
	if err := py.Smoke(); err != nil {
		t.Fatalf("published runtime smoke failed: %v", err)
	}
}
