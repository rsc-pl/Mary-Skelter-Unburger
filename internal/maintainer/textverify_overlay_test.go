package maintainer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This protects the no-GiB-copy contract as well as legacy user data.
func TestOverlayUsesLinksAndNeverTouchesHistoricalDirectory(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "original")
	rebuilt := filepath.Join(root, "rebuilt")
	for _, p := range []string{src, rebuilt} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	old := filepath.Join(root, "_go_verify_overlay")
	if e := os.Mkdir(old, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(old, "owner-saved.txt"), []byte("do not delete"), 0600); e != nil {
		t.Fatal(e)
	}
	sourceData := bytes.Repeat([]byte("src!"), 1024)
	editedData := bytes.Repeat([]byte("edit"), 1024)
	for p, b := range map[string][]byte{
		filepath.Join(src, "GxArchivedFile052.dat"):     sourceData,
		filepath.Join(src, "GxArchivedFile053.dat"):     sourceData,
		filepath.Join(rebuilt, "GxArchivedFile053.dat"): editedData,
		filepath.Join(rebuilt, "metadata.bin"):          {1, 2, 3},
	} {
		if e := os.WriteFile(p, b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	view, err := buildReadOnlyOverlay(rebuilt, src, root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(view)
	if view == old || !strings.HasPrefix(filepath.Base(view), ".go-verify-overlay-") {
		t.Fatalf("unexpected disposable path %q", view)
	}
	for file, want := range map[string][]byte{"GxArchivedFile052.dat": sourceData, "GxArchivedFile053.dat": editedData, "metadata.bin": {1, 2, 3}} {
		path := filepath.Join(view, file)
		b, e := os.ReadFile(path)
		if e != nil || !bytes.Equal(b, want) {
			t.Fatalf("overlay read %s: %v", file, e)
		}
		fi, e := os.Lstat(path)
		if e != nil {
			t.Fatal(e)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			realFi, e := os.Stat(path)
			if e != nil {
				t.Fatal(e)
			}
			original := filepath.Join(src, file)
			if file == "GxArchivedFile053.dat" || file == "metadata.bin" {
				original = filepath.Join(rebuilt, file)
			}
			inputFi, e := os.Stat(original)
			if e != nil {
				t.Fatal(e)
			}
			if !os.SameFile(fi, inputFi) || !os.SameFile(realFi, inputFi) {
				t.Fatalf("overlay copied bytes instead of linking %s", file)
			}
		}
	}
	if e := os.RemoveAll(view); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(filepath.Join(old, "owner-saved.txt")); e != nil || string(b) != "do not delete" {
		t.Fatalf("legacy directory changed: %v", e)
	}
	if b, e := os.ReadFile(filepath.Join(rebuilt, "GxArchivedFile053.dat")); e != nil || !bytes.Equal(b, editedData) {
		t.Fatalf("rebuilt modified: %v", e)
	}
}

func TestOverlayRejectsLinkedMasterInputWithoutTouchingHistory(t *testing.T) {
	root := t.TempDir()
	src, rebuilt := filepath.Join(root, "src"), filepath.Join(root, "rebuilt")
	for _, d := range []string{src, rebuilt} {
		if e := os.Mkdir(d, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.Symlink("/tmp/arbitrary-file", filepath.Join(src, "GxArchivedFile052.dat")); e != nil {
		t.Skipf("symlink unavailable: %v", e)
	}
	if _, e := buildReadOnlyOverlay(rebuilt, src, root); e == nil {
		t.Fatal("expected fail closed")
	}
	matches, e := filepath.Glob(filepath.Join(root, ".go-verify-overlay-*"))
	if e != nil || len(matches) != 0 {
		t.Fatalf("scratch not cleaned after failure: %v %v", matches, e)
	}
}
