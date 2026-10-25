package maintainer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMissingPublishedPythonFailsBeforeGraphicsWrites(t *testing.T) {
	root := t.TempDir()
	writeGraphicsFixture(t, root, false)
	helper := filepath.Join(root, "dummy-bcn")
	if runtime.GOOS == "windows" {
		helper += ".exe"
	}
	if err := os.WriteFile(helper, []byte("placeholder"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS_PC_GRAPHICS_BCN_BINARY", helper)
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "1")
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "")
	err := BuildGraphics(root, CachedSources{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "required public Python runtime") {
		t.Fatalf("missing public runtime accepted: %v", err)
	}
	if _, e := os.Lstat(filepath.Join(root, ".local", "build")); !os.IsNotExist(e) {
		t.Fatalf("graphics wrote scratch despite missing runtime: %v", e)
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "0")
	err = BuildGraphics(root, CachedSources{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "system Python graphics are not supported") {
		t.Fatalf("system fallback accepted: %v", err)
	}
}
