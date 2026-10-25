package graphicsruntime

import (
	"errors"
	"fmt"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The interpreter is invoked with -I -S. Neither system site-packages,
// user-site nor inherited PYTHONPATH can alter the package search path.
// The approved, SHA-checked public site-packages and project-owned legacy
// raster scripts are inserted explicitly in a small standard-library bootstrap.
// Keep arguments outside the Python command string to avoid shell expansion.
const isolatedBootstrap = `import os, runpy, sys
script, packages, legacy = sys.argv[1:4]
sys.path[:0] = [packages, legacy, os.path.dirname(script)]
sys.argv = [script] + sys.argv[4:]
runpy.run_path(script, run_name="__main__")
`

// GraphicsCommand constructs a hermetic invocation of an approved project
// renderer. The caller must Resolve() the SHA-pinned interpreter first.
// This function does not mutate files, spawn the command, or use a shell.
func (py Python) GraphicsCommand(root, script string, args ...string) (*exec.Cmd, error) {
	if !filepath.IsAbs(root) || py.Path == "" || py.SitePackages == "" {
		return nil, errors.New("verified interpreter and absolute project root required")
	}
	legacy := filepath.Join(root, "toolchain", "python-src")
	// The Switch renderer is provided by the versioned source tree.
	// Legacy paths are accepted only when explicitly listed as compatibility inputs.
	if !filepath.IsAbs(script) {
		script = filepath.Join(root, script)
	}
	clean, err := filepath.Abs(script)
	if err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return nil, err
	}
	legacyActual, err := filepath.EvalSymlinks(legacy)
	if err != nil {
		return nil, err
	}
	legacyRel, relErr := filepath.Rel(legacy, clean)
	legacyRequested := relErr == nil && legacyRel != "." && legacyRel != ".." &&
		!strings.HasPrefix(legacyRel, ".."+string(filepath.Separator))
	legacyActualRel, actualErr := filepath.Rel(legacyActual, actual)
	legacySafe := legacyRequested && actualErr == nil && legacyActualRel != "." &&
		legacyActualRel != ".." && !strings.HasPrefix(legacyActualRel, ".."+string(filepath.Separator))
	publicSwitch := filepath.Join(root, "toolchain", "switch_port", "render_graphics.py")
	publicSwitchSafe := clean == publicSwitch && actual == publicSwitch
	switchSafe := publicSwitchSafe
	if !legacySafe && !publicSwitchSafe {
		approved, lookupErr := privateassets.Resolve(root, "renderer-switch-script")
		switchSafe = lookupErr == nil && clean == approved && actual == approved
	}
	if !legacySafe && !switchSafe {
		return nil, fmt.Errorf("graphics script is outside approved renderer paths: %s", script)
	}
	fi, err := os.Stat(actual)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || filepath.Ext(actual) != ".py" {
		return nil, fmt.Errorf("renderer must be a regular Python file: %s", script)
	}
	argv := []string{"-I", "-S", "-B", "-c", isolatedBootstrap, clean, py.SitePackages, legacyActual}
	argv = append(argv, args...)
	cmd := exec.Command(py.Path, argv...)
	cmd.Dir = root
	cmd.Env = py.Environment(os.Environ(), "")
	return cmd, nil
}

// ToolCommand invokes a checked-in project Python tool or compiles project
// sources with the frozen interpreter. No system Python or pip fallback exists.
func (py Python) ToolCommand(root string, args ...string) (*exec.Cmd, error) {
	if len(args) == 0 {
		return nil, errors.New("missing Python tool arguments")
	}
	if args[0] == "-m" {
		if len(args) < 4 || args[1] != "compileall" || args[2] != "-q" {
			return nil, errors.New("only compileall -q is permitted")
		}
		for _, name := range args[3:] {
			path := filepath.Join(root, name)
			base := filepath.Join(root, "toolchain", "python-src")
			rel, e := filepath.Rel(base, path)
			if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, fmt.Errorf("Python compilation escapes project scripts: %s", name)
			}
		}
		cmd := exec.Command(py.Path, append([]string{"-I", "-S", "-B"}, args...)...)
		cmd.Dir = root
		cmd.Env = py.Environment(os.Environ(), "")
		return cmd, nil
	}
	return py.GraphicsCommand(root, args[0], args[1:]...)
}
