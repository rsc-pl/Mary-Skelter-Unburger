package maintainer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstalledRuntimeCannotBeBypassedOnPC(t *testing.T) {
	root := t.TempDir()
	writeGraphicsFixture(t, root, false)
	dir := filepath.Join(root, "toolchain", "python-runtime", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "0") // reach Python selector without fixture codec
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "0")
	err := BuildGraphics(root, CachedSources{}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "system Python graphics are not supported") {
		t.Fatalf("runtime bypass: %v", err)
	}
}
