package switchrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwitchProductionNeverFallsBack(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "marker.txt")
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "")
	err := runGraphicsPython(root, "python3", []string{"ignored.py"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "required public Python runtime") {
		t.Fatalf("silent system Python fallback: %v", err)
	}
	if _, e := os.Lstat(marker); !os.IsNotExist(e) {
		t.Fatalf("unexpected side effect: %v", e)
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "0")
	err = runGraphicsPython(root, "python3", []string{"ignored.py"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "system Python graphics are not supported") {
		t.Fatalf("explicit fallback accepted: %v", err)
	}
}
