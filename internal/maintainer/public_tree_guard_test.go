package maintainer

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Public orchestration is Go; approved project Python sources and runtimes are in toolchain; the pinned runtime is
// toolchain/python-runtime for the vendored, isolated graphics-only runtime.
// Maintainer history and game-derived private inputs remain under .local.
func TestPublicTreeContainsNoPythonOrResearch(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".local" || d.Name() == ".git" || d.Name() == "__pycache__") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		lower := strings.ToLower(d.Name())
		if rel, e := filepath.Rel(root, path); e == nil && (strings.HasPrefix(filepath.ToSlash(rel), "toolchain/python-runtime/") || strings.HasPrefix(filepath.ToSlash(rel), "toolchain/python-src/") || filepath.ToSlash(rel) == "toolchain/switch_port/render_graphics.py") {
			return nil // explicitly selected public graphics runtime, not game assets
		}
		if strings.HasSuffix(lower, ".py") || strings.HasSuffix(lower, ".pyc") || strings.HasSuffix(lower, ".pyo") {
			t.Errorf("Python source/bytecode leaked to public tree: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	build, err := os.ReadFile(filepath.Join(root, "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(build), "go run ./cmd/mary-skelter-builder") || strings.Contains(string(build), "mary-skelter-builder rebuild") {
		t.Fatal("public build.sh must open the Go wizard by default; rebuild must be an explicit command")
	}
}
