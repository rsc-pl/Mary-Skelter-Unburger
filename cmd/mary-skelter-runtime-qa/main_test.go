package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRejectRelativeRoot(t *testing.T) {
	r, e := check("relative/path", false)
	if e == nil || !strings.Contains(e.Error(), "absolute") || r.Status != "FAIL" {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestAbsentPinnedRuntimeFailsClosed(t *testing.T) {
	dir := t.TempDir()
	r, e := check(filepath.Clean(dir), false)
	if e == nil || !strings.Contains(e.Error(), "required public Python runtime") || r.Status != "FAIL" || r.Smoke != "" {
		t.Fatalf("%+v %v", r, e)
	}
}
