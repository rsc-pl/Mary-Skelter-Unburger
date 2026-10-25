package switchrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphicsInputReuseFailsClosedWithoutOriginals(t *testing.T) {
	root := t.TempDir()
	if _, _, ok := findReusableGraphics(root); ok {
		t.Fatal("empty source directory reused an image")
	}
	inputs, cached, _ := GraphicsReadiness(root)
	if inputs || cached {
		t.Fatal("missing source and font inputs reported ready")
	}
}

func TestGraphicsCacheRejectsUnpinnedAndLinkedZIP(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "candidate.zip")
	if err := os.WriteFile(p, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validatePinnedCanonicalGraphics(p, strings.Repeat("0", 64)); err == nil {
		t.Fatal("wrong digest accepted")
	}
	link := filepath.Join(root, "link.zip")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if err := validatePinnedCanonicalGraphics(link, strings.Repeat("0", 64)); err == nil {
		t.Fatal("linked candidate accepted")
	}
}
