package maintainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This test protects owner .local/build scratch and cache when the public
// runtime has not been installed yet or an obsolete override tries to use
// system Python. No private DAT, source folder, or original font is needed.
func TestRebuildRejectsMissingRuntimeWithoutTouchingOwnerScratch(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, ".local", "build", "records", "owner.keep")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("legacy owner sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "")
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "")
	t.Setenv("MS_PC_GRAPHICS_BCN_BINARY", "")
	err := Rebuild(root, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "required public Python runtime") {
		t.Fatalf("missing runtime did not fail early: %v", err)
	}
	got, e := os.ReadFile(sentinel)
	if e != nil || string(got) != "legacy owner sentinel" {
		t.Fatalf("owner scratch changed: %q %v", got, e)
	}
	if _, e := os.Lstat(filepath.Join(root, ".local", "source_cache")); !os.IsNotExist(e) {
		t.Fatalf("source cache touched before runtime gate: %v", e)
	}
}

func TestRebuildRejectsLegacyOverridesWithoutTouchingScratch(t *testing.T) {
	root := t.TempDir()
	s := filepath.Join(root, ".local", "build", "dont-remove")
	if err := os.MkdirAll(filepath.Dir(s), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s, []byte("owner"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "0")
	if err := preflightRebuildDependencies(root); err == nil || !strings.Contains(err.Error(), "system Python graphics") {
		t.Fatalf("legacy Python accepted: %v", err)
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "1")
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "0")
	if err := preflightRebuildDependencies(root); err == nil || !strings.Contains(err.Error(), "native Go BCn") {
		t.Fatalf("legacy C mode accepted: %v", err)
	}
	if got, err := os.ReadFile(s); err != nil || string(got) != "owner" {
		t.Fatalf("changed scratch: %s %v", got, err)
	}
}

// An optional full public-binary proof, invoked by the maintainer with
// MS__REAL_RUNTIME_ROOT pointing at a standalone, verified extracted
// legacy checkout. It cannot manufacture successful font/game-master parity.
func TestRealHermeticRuntimeAndEncoderPreflightWhenPresent(t *testing.T) {
	root := os.Getenv("MS__REAL_RUNTIME_ROOT")
	if root == "" {
		t.Skip("the pinned Python runtime is not installed in this source tree")
	}
	t.Setenv("MS_BUNDLED_GRAPHICS_PYTHON", "1")
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "1")
	t.Setenv("MS_PC_GRAPHICS_BCN_BINARY", "")
	if err := preflightRebuildDependencies(root); err != nil {
		t.Fatalf("real runtime/Go encoder preflight: %v", err)
	}
}
