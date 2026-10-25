package graphicsruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortablePythonCommandsStayInsideVersionedToolchain(t *testing.T) {
	host, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("optional isolated-command oracle needs Python only to test command construction")
	}
	host, err = filepath.Abs(host)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	scripts := filepath.Join(root, "toolchain", "python-src", "scripts")
	packages := filepath.Join(root, "toolchain", "python-runtime", "linux-amd64", "site-packages")
	for _, p := range []string{scripts, packages} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(scripts, "runner.py")
	if err := os.WriteFile(script, []byte("import os,sys\nassert sys.argv[1:]==['value']\nassert 'PYTHONPATH' not in os.environ\nprint('PINNED-COMMAND-OK')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	py := Python{Path: host, SitePackages: packages}
	t.Setenv("PYTHONPATH", root)
	cmd, err := py.ToolCommand(root, "toolchain/python-src/scripts/runner.py", "value")
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("portable command: %v %s", err, out)
	}
	if !strings.Contains(string(out), "PINNED-COMMAND-OK") {
		t.Fatalf("unexpected output: %s", out)
	}
	cmd, err = py.ToolCommand(root, "-m", "compileall", "-q", "toolchain/python-src/scripts")
	if err != nil {
		t.Fatal(err)
	}
	if out, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("compileall: %v %s", err, out)
	}
	if _, err = py.ToolCommand(root, "../../outside.py"); err == nil {
		t.Fatal("tool outside tracked root accepted")
	}
	if _, err = py.ToolCommand(root, "-m", "pip", "install", "anything"); err == nil {
		t.Fatal("pip unexpectedly allowed")
	}
}
