package maintainer

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeGoDatasetSurface(t *testing.T) {
	if err := verifyNativeTextDatasetSurface(); err != nil {
		t.Fatalf("native canonical surface: %v", err)
	}
	if _, err := sourceDatasetInfoFor("MSF_PC_KO"); err == nil {
		t.Fatal("Korean dataset must be unsupported")
	}
}

func TestNativeGoVerifyRejectsWithoutRunningPython(t *testing.T) {
	root := t.TempDir()
	// A non-existent Go binary is an unambiguous first-stage failure. No
	// legacy Python runner is even eligible to execute at this boundary.
	t.Setenv("PATH", root)
	err := VerifyNativeGo(root, true, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "go test") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, e := os.Stat(filepath.Join(root, ".local")); !os.IsNotExist(e) {
		t.Fatalf("native failure wrote private output: %v", e)
	}
}
