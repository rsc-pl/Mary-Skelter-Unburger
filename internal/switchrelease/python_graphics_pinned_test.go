package switchrelease

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstalledPublicPythonCannotBeBypassed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "toolchain", "python-runtime", runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "0")
	err := runGraphicsPython(root, "python3", []string{"test.py"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "system Python graphics are not supported") {
		t.Fatalf("installed bundle bypassed: %v", err)
	}
}
