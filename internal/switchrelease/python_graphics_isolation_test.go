package switchrelease

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPartialBundledRuntimeFailsClosed(t *testing.T) {
	root := t.TempDir()
	partial := filepath.Join(root, "toolchain", "python-runtime", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(partial, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "")
	err := runGraphicsPython(root, "python3", []string{"missing.py"}, nil, nil)
	if err == nil {
		t.Fatal("partial runtime silently fell back to system Python")
	}
	if !strings.Contains(err.Error(), "manifest.json") {
		t.Fatalf("expected manifest error, got %v", err)
	}
}
