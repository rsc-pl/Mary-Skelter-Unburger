package maintainer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGraphicsNativeBC7Cutover(t *testing.T) {
	allowLegacyGraphicsUnitTests = true
	t.Cleanup(func() { allowLegacyGraphicsUnitTests = false })

	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake Python backend")
	}
	root := t.TempDir()
	writeGraphicsFixture(t, root, false)
	master := filepath.Join(root, "pristine", "masterWindows")
	if err := os.MkdirAll(master, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(master, "GxArchivedFile074.dat"), []byte("original-source"), 0644); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(fakeBin, "private-bcn-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
test "$1" = "toolchain/python-src/scripts/graphics/build_graphics.py"
test "$MARY_SKELTER_GRAPHICS_NATIVE_BC7" = "$EXPECTED_NATIVE_BC7"
test "$MARY_SKELTER_GRAPHICS_BCN_BINARY" = "$EXPECTED_BCN_BINARY"
BEFORE=$(sha256sum .local/build/rebuilt/MSF_PC_LOCALIZED/masterWindows/GxArchivedFile074.dat | cut -d' ' -f1)
cat > "$MARY_SKELTER_GRAPHICS_REPORT" <<JSON
{"enabled_assets":1,"encoder":{},"results":[{"id":"TEST_GRAPHICS","dataset":"MSF_PC_LOCALIZED","archive":74,"renderer":"raster_atlas_v1","archive_sha256_before_graphics":"$BEFORE","archive_sha256_after_graphics":"$BEFORE","graphics_only_archive_seeded":false}],"status":"PASS"}
JSON
`
	if err := os.WriteFile(filepath.Join(fakeBin, "python3"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BILZ", "0")
	t.Setenv("MS_PC_GRAPHICS_BCN_BINARY", helper)
	t.Setenv("EXPECTED_BCN_BINARY", helper)
	c := CachedSources{FinaleMaster: master}
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "")
	t.Setenv("EXPECTED_NATIVE_BC7", "1")
	if err := BuildGraphics(root, c, nil, nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "1")
	t.Setenv("EXPECTED_NATIVE_BC7", "1")
	t.Setenv("EXPECTED_BCN_BINARY", helper)
	if err := BuildGraphics(root, c, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"yes", "false", "2"} {
		t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", bad)
		if err := BuildGraphics(root, c, nil, nil); err == nil || !strings.Contains(err.Error(), "must be 0 or 1") {
			t.Fatalf("accepted mode %q: %v", bad, err)
		}
	}
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "1")
	for _, bad := range []string{"relative", "/missing/path"} {
		t.Setenv("MS_PC_GRAPHICS_BCN_BINARY", bad)
		if err := BuildGraphics(root, c, nil, nil); err == nil {
			t.Fatalf("accepted unavailable binary %q", bad)
		}
	}
}
