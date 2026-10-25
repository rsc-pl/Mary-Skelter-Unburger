package graphicsruntime

// Private game assets and fonts are never part of the public Python runtime.
// Only approved platform-owned Python and Pillow/NumPy binaries live in
// toolchain/python-runtime/<GOOS>-<GOARCH>/, with a SHA-256 inventory.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type Manifest struct {
	Schema        int               `json:"schema"`
	Platform      string            `json:"platform"`
	Python        string            `json:"python"`
	PythonVersion string            `json:"python_version"`
	PillowVersion string            `json:"pillow_version"`
	NumPyVersion  string            `json:"numpy_version"`
	Files         map[string]string `json:"files"`
}
type Python struct {
	Path          string
	SitePackages  string
	Platform      string
	PythonVersion string
	PillowVersion string
	NumPyVersion  string
}

// Resolve verifies the complete vendored runtime before it can be executed.
// This function never invokes a system Python, shell, pip or network.
func Resolve(root string) (Python, error) {
	// Production always pins the public, separately delivered runtime. The
	// synthetic ResolvePlatform fixtures remain usable for offline unit tests.
	if err := VerifyPublishedRuntimeManifest(root, runtime.GOOS, runtime.GOARCH); err != nil {
		return Python{}, err
	}
	return ResolvePlatform(root, runtime.GOOS, runtime.GOARCH)
}
func ResolvePlatform(root, goos, goarch string) (Python, error) {
	var z Python
	if !filepath.IsAbs(root) {
		return z, errors.New("absolute project root required")
	}
	if (goos != "linux" && goos != "windows") || goarch != "amd64" {
		return z, fmt.Errorf("unsupported Python runtime platform %s/%s", goos, goarch)
	}
	platform := goos + "-" + goarch
	dir := filepath.Join(root, "toolchain", "python-runtime", platform)
	fi, err := os.Lstat(dir)
	if err != nil {
		return z, fmt.Errorf("bundled %s runtime missing: %w", platform, err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return z, errors.New("non-regular bundled Python runtime directory")
	}
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return z, err
	}
	var m Manifest
	if err = json.Unmarshal(data, &m); err != nil {
		return z, err
	}
	if m.Schema != 1 || m.Platform != platform || m.PythonVersion != "3.13.15" || m.PillowVersion != "12.3.0" || m.NumPyVersion != "2.3.5" || len(m.Files) < 3 {
		return z, errors.New("incomplete bundled Python manifest")
	}
	exe := "python/bin/python3"
	if goos == "windows" {
		exe = "python/python.exe"
	}
	if m.Python != exe {
		return z, fmt.Errorf("bad interpreter path: %q", m.Python)
	}
	if _, ok := m.Files[exe]; !ok {
		return z, errors.New("missing interpreter digest")
	}
	for rel, want := range m.Files {
		if filepath.IsAbs(rel) || strings.Contains(rel, "\\") || rel == "" || rel == "." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") || strings.HasPrefix(rel, "/") || strings.HasSuffix(rel, "/") {
			return z, fmt.Errorf("unsafe runtime path %q", rel)
		}
		if len(want) != 64 {
			return z, fmt.Errorf("bad SHA length %s", rel)
		}
		if _, err := hex.DecodeString(want); err != nil {
			return z, err
		}
		path := filepath.Join(dir, filepath.FromSlash(rel))
		// Avoid following symlinks in the runtime. The offline assembler canonicalizes them.
		for cur := path; cur != dir; cur = filepath.Dir(cur) {
			fi, err := os.Lstat(cur)
			if err != nil {
				return z, err
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				return z, fmt.Errorf("runtime symlink rejected: %s", rel)
			}
		}
		f, err := os.Open(path)
		if err != nil {
			return z, err
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			return z, err
		}
		if !fi.Mode().IsRegular() {
			f.Close()
			return z, fmt.Errorf("nonregular runtime file: %s", rel)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return z, err
		}
		if hex.EncodeToString(h.Sum(nil)) != want {
			return z, fmt.Errorf("runtime checksum mismatch: %s", rel)
		}
	}
	// No unlisted module may be injected into an otherwise valid runtime.
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, visitErr error) error {
		if visitErr != nil {
			return visitErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected runtime symlink: %s", path)
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" {
			return nil
		}
		if _, ok := m.Files[rel]; !ok {
			return fmt.Errorf("unlisted runtime file: %s", rel)
		}
		return nil
	})
	if err != nil {
		return z, err
	}
	return Python{Path: filepath.Join(dir, filepath.FromSlash(exe)), SitePackages: filepath.Join(dir, "site-packages"), Platform: platform, PythonVersion: m.PythonVersion, PillowVersion: m.PillowVersion, NumPyVersion: m.NumPyVersion}, nil
}

// Environment removes inherited Python, pip and virtualenv settings.
// -I -S ignores user Python configuration and site initialization; import
// paths are explicitly supplied by the reviewed -c bootstrap instead.
// The projectLegacy argument is retained for old call sites but deliberately
// never contributes to PYTHONPATH in a bundled process.
func (py Python) Environment(base []string, projectLegacy string) []string {
	env := make([]string, 0, len(base)+2)
	for _, v := range base {
		key, _, _ := strings.Cut(v, "=")
		k := strings.ToUpper(key)
		if strings.HasPrefix(k, "PYTHON") || strings.HasPrefix(k, "PIP_") ||
			strings.HasPrefix(k, "UV_") || k == "VIRTUAL_ENV" || k == "CONDA_PREFIX" || k == "CONDA_DEFAULT_ENV" {
			continue
		}
		env = append(env, v)
	}
	return env
}
