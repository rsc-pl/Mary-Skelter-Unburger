package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineGoBundlePackRestoreAndTamper(t *testing.T) {
	root := t.TempDir()
	for _, module := range []string{"bcn", "xsys"} {
		f := filepath.Join(goSourceDir(root), module, "go.mod")
		if e := os.MkdirAll(filepath.Dir(f), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(f, []byte("module example.com/"+module+"\n"), 0644); e != nil {
			t.Fatal(e)
		}
	}
	if e := os.WriteFile(filepath.Join(goSourceDir(root), "bcn", "source.go"), []byte("package bcn\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := goPack(root); e != nil {
		t.Fatal(e)
	}
	if e := goPack(root); e != nil {
		t.Fatal("idempotent pack:", e)
	}
	m, e := goReadManifest(root)
	if e != nil {
		t.Fatal(e)
	}
	if e = goVerifyArchive(root, m); e != nil {
		t.Fatal(e)
	}
	if e = os.RemoveAll(goSourceDir(root)); e != nil {
		t.Fatal(e)
	}
	if e = goRestore(root); e != nil {
		t.Fatal(e)
	}
	if e = goRestore(root); e != nil {
		t.Fatal("idempotent restore:", e)
	}
	f := filepath.Join(goSourceDir(root), "bcn", "source.go")
	if e = os.WriteFile(f, []byte("malicious replacement"), 0644); e != nil {
		t.Fatal(e)
	}
	if e = goRestore(root); e == nil {
		t.Fatal("accepted changed installed Go dependency")
	}
	if e = os.RemoveAll(goSourceDir(root)); e != nil {
		t.Fatal(e)
	}
	z := goBundleZip(root)
	file, e := os.OpenFile(z, os.O_APPEND|os.O_WRONLY, 0644)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = file.Write([]byte("tampered")); e != nil {
		t.Fatal(e)
	}
	file.Close()
	if e = goRestore(root); e == nil || !strings.Contains(e.Error(), "integrity failure") {
		t.Fatalf("tampered archive accepted: %v", e)
	}
	if _, e = os.Stat(goSourceDir(root)); !os.IsNotExist(e) {
		t.Fatalf("tampered install created Go cache: %v", e)
	}
}
func TestRejectUnsafeGoPaths(t *testing.T) {
	for _, p := range []string{"", ".", "..", "../x", "a/../b", "a//b", "/abs", "C:/drive", "a\\b"} {
		if goPathSafe(p) {
			t.Errorf("accepted unsafe path %q", p)
		}
	}
	for _, p := range []string{"bcn/go.mod", "xsys/windows/svc/mgr/recovery.go"} {
		if !goPathSafe(p) {
			t.Errorf("rejected safe path %q", p)
		}
	}
}
