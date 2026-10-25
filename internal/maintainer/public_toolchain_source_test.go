package maintainer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectToolsUseVersionedSourcesNotPrivateArchive(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{
		"toolchain/python-src/scripts/graphics/build_graphics.py",
		"toolchain/python-src/scripts/graphics/canonical_bilz_wrap.py",
		"toolchain/python-src/toolchain/tests/test_title_footer_rendering.py",
		"toolchain/python-src/toolchain/tests/test_title_footer_discovery.py",
	}
	forbidden := ".local" + "/" + "legacy_python"
	for _, p := range paths {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), forbidden) || strings.Contains(string(b), "'.local'/'legacy_python'") {
			t.Errorf("project tool %s still requires ignored maintainer files", p)
		}
	}
	if st, err := os.Lstat(filepath.Join(root, "toolchain", "PUBLISH.md")); err == nil {
		t.Errorf("private publishing guide incorrectly included in public toolchain: %v", st.Mode())
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
