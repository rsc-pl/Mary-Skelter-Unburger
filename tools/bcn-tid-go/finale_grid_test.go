package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestGridPublicIntegrity(t *testing.T) {
	root, _ := filepath.Abs("../..")
	a, e := loadFinaleGrid(root)
	if e != nil {
		t.Fatal(e)
	}
	if a.Index != 71 || a.Archive != 73 || len(a.Replacements) != 8 {
		t.Fatalf("wrong canonical atlas %+v", a)
	}
	if _, e = renderFinaleGrid([]byte("unverified source"), a); e == nil || !strings.Contains(e.Error(), "stale") {
		t.Fatalf("not fail-closed: %v", e)
	}
	glyphs := map[byte]gridGlyph{'A': {W: 2, H: 2, Pix: []byte{255, 255, 255, 255, 0, 0, 0, 0, 255, 255, 255, 255, 255, 255, 255, 255}}}
	out, e := gridCompose(glyphs, "AA", 4, -1, 12)
	if e != nil || out.W != 7 || out.H != 4 {
		t.Fatalf("raster synthetic glyph compositor %+v: %v", out, e)
	}
	if _, e = gridCompose(glyphs, "AZ", 4, -1, 12); e == nil {
		t.Fatal("missing original glyph accepted")
	}
	if _, e = parseFamilyBatch([]byte(`{"version":1,"project_root":".","jobs":[{"source":"a","source_sha256":"` + strings.Repeat("a", 64) + `","output":"b","families":["MSF_EN_FINALE_CHARACTER_GRID"],"quality":6,"max_alpha":64,"max_visible":64}]}`)); e != nil {
		t.Fatalf("allowlist: %v", e)
	}
	if bytes.Equal(out.Pix, make([]byte, len(out.Pix))) {
		t.Fatal("no ink")
	}
}
