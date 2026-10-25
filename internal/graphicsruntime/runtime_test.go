package graphicsruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBundledAndTamper(t *testing.T) {
	root := t.TempDir()
	d := filepath.Join(root, "toolchain", "python-runtime", "linux-amd64")
	for _, s := range []string{"python/bin", "site-packages"} {
		if err := os.MkdirAll(filepath.Join(d, s), 0755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{}
	for _, s := range []string{"python/bin/python3", "site-packages/PIL.test", "site-packages/numpy.test"} {
		b := []byte(s)
		if err := os.WriteFile(filepath.Join(d, s), b, 0755); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		files[s] = hex.EncodeToString(h[:])
	}
	m := Manifest{Schema: 1, Platform: "linux-amd64", Python: "python/bin/python3", PythonVersion: "3.13.15", PillowVersion: "12.3.0", NumPyVersion: "2.3.5", Files: files}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(d, "manifest.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	py, err := ResolvePlatform(root, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	env := py.Environment([]string{"HOME=/tmp", "PYTHONPATH=/evil", "PYTHONHOME=/host", "VIRTUAL_ENV=/tmp/venv"}, "/local/legacy")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "/evil") || strings.Contains(joined, "PYTHONHOME") || strings.Contains(joined, "VIRTUAL_ENV") || strings.Contains(joined, "/local/legacy") {
		t.Fatalf("unsafe environment: %s", joined)
	}
	os.WriteFile(filepath.Join(d, "site-packages/PIL.test"), []byte("tamper"), 0644)
	if _, err := ResolvePlatform(root, "linux", "amd64"); err == nil {
		t.Fatal("accepted modified Pillow")
	}
	if _, err := ResolvePlatform(root, "other", "amd64"); err == nil {
		t.Fatal("unsupported platform")
	}
}
