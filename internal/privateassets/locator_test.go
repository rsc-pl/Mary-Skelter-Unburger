package privateassets

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, path string, data []byte) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".local/internal"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, ".local", path)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0600); err != nil {
		t.Fatal(err)
	}
	sha := sha256.Sum256(data)
	manifest := fmt.Sprintf(`{"schema":1,"entries":{"source":{"path":".local/%s","kind":"file","sha256":"%s"}}}`, path, hex.EncodeToString(sha[:]))
	if err := os.WriteFile(filepath.Join(root, indexRelative), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestSourceBoundLookup(t *testing.T) {
	root := fixture(t, "example/test.dat", []byte("original"))
	p, e := Resolve(root, "source")
	if e != nil || !strings.HasSuffix(p, "test.dat") {
		t.Fatalf("valid source failed %v %s", e, p)
	}
	if err := os.WriteFile(p, []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := Resolve(root, "source"); e == nil {
		t.Fatal("digest drift accepted")
	}
	if _, e := Resolve(root, "unknown"); e == nil {
		t.Fatal("unknown key accepted")
	}
}
func TestSymlinkAndTraversalRefused(t *testing.T) {
	root := fixture(t, "example/test.dat", []byte("original"))
	target := filepath.Join(root, ".local/example/test.dat")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", target); err != nil {
		t.Skip(err)
	}
	if _, e := Resolve(root, "source"); e == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(root, indexRelative)
	if err := os.WriteFile(idx, []byte(`{"schema":1,"entries":{"source":{"path":"../escape","kind":"file","sha256":"`+strings.Repeat("0", 64)+`"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, e := Resolve(root, "source"); e == nil {
		t.Fatal("traversal accepted")
	}
}
