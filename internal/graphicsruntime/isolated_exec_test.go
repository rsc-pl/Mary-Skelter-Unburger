package graphicsruntime

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIsolatedPythonSmokeAndPathGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runner smoke uses POSIX python3; Windows runs on native CI")
	}
	pyexe, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("no Python runtime available for isolated smoke")
	}
	root := t.TempDir()
	scripts := filepath.Join(root, "toolchain", "python-src", "scripts")
	libs := filepath.Join(root, "toolchain", "python-runtime", "linux-amd64", "site-packages")
	evil := filepath.Join(root, "evil")
	for _, d := range []string{scripts, libs, evil} {
		if e := os.MkdirAll(d, 0755); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(libs, "trusted.py"), []byte("VALUE = 'GOOD'\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(evil, "poison.py"), []byte("raise RuntimeError('HOST IMPORT')\n"), 0644); e != nil {
		t.Fatal(e)
	}
	body := `import sys, importlib.util, trusted
assert sys.argv[1:] == ['a b', '--okay']
assert trusted.VALUE == 'GOOD'
assert importlib.util.find_spec('poison') is None, 'untrusted PYTHONPATH leaked'
assert not any('site-packages' in p and 'python-runtime' not in p for p in sys.path), sys.path
print('ISOLATED-RUNTIME-PASS')
`
	file := filepath.Join(scripts, "run.py")
	if e := os.WriteFile(file, []byte(body), 0644); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PYTHONPATH", evil)
	t.Setenv("PYTHONINSPECT", "1")
	py := Python{Path: pyexe, SitePackages: libs, Platform: "linux-amd64"}
	cmd, e := py.GraphicsCommand(root, file, "a b", "--okay")
	if e != nil {
		t.Fatal(e)
	}
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("isolated invocation failed: %v\n%s", e, output)
	}
	if !strings.Contains(string(output), "ISOLATED-RUNTIME-PASS") {
		t.Fatalf("bad stdout: %s", output)
	}
	if _, e = py.GraphicsCommand(root, filepath.Join(root, "evil", "poison.py")); e == nil {
		t.Fatal("external file accepted")
	}
	link := filepath.Join(scripts, "link.py")
	if e = os.Symlink(filepath.Join(evil, "poison.py"), link); e == nil {
		if _, e = py.GraphicsCommand(root, link); e == nil {
			t.Fatal("escaped symlink accepted")
		}
	}
}
