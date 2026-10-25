package graphicsruntime

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCanonicalSwitchScriptAllowlist(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux isolated fixture")
	}
	interpreter, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no test interpreter")
	}
	root := t.TempDir()
	legacy := filepath.Join(root, "toolchain", "python-src")
	packages := filepath.Join(root, "toolchain", "python-runtime", "linux-amd64", "site-packages")
	switchDir := filepath.Join(root, ".local", "test-switch-renderer")
	for _, d := range []string{legacy, packages, switchDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	approved := filepath.Join(switchDir, "approved.py")
	if err := os.WriteFile(approved, []byte("print('CANONICAL_SWITCH_ISOLATION_PASS')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("print('CANONICAL_SWITCH_ISOLATION_PASS')\n"))
	index := fmt.Sprintf(`{"schema":1,"entries":{"renderer-switch-script":{"kind":"file","path":".local/test-switch-renderer/approved.py","sha256":"%x"}}}`, digest)
	if err := os.MkdirAll(filepath.Join(root, ".local", "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local", "internal", "private_asset_locations.json"), []byte(index), 0600); err != nil {
		t.Fatal(err)
	}
	py := Python{Path: interpreter, SitePackages: packages}
	cmd, err := py.GraphicsCommand(root, approved)
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "CANONICAL_SWITCH_ISOLATION_PASS") {
		t.Fatalf("Switch allowlist invocation failed: %v %s", err, output)
	}
	sibling := filepath.Join(switchDir, "unapproved.py")
	if err := os.WriteFile(sibling, []byte("print('NOT_ALLOWED')"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := py.GraphicsCommand(root, sibling); err == nil {
		t.Fatal("approved entire research tree instead of one Switch script")
	}
	out := filepath.Join(root, "outside.py")
	if err := os.WriteFile(out, []byte("print('NOT_ALLOWED')"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(approved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, approved); err != nil {
		t.Fatal(err)
	}
	if _, err := py.GraphicsCommand(root, approved); err == nil {
		t.Fatal("Switch renderer symlink escape accepted")
	}
}
