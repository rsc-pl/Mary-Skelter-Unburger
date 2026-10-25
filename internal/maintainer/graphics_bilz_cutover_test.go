package maintainer

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This controls only the container compression boundary, not raster/codec
// production. The pre-existing Python renderer must still get invoked.
func TestGraphicsBILZCutoverEnvironment(t *testing.T) {
	allowLegacyGraphicsUnitTests = true
	t.Cleanup(func() { allowLegacyGraphicsUnitTests = false })

	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake Python helper")
	}
	root := t.TempDir()
	writeGraphicsFixture(t, root, false)
	master := filepath.Join(root, "pristine", "masterWindows")
	if err := os.MkdirAll(master, 0755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(master, "GxArchivedFile074.dat")
	if err := os.WriteFile(src, []byte("pristine-source"), 0644); err != nil {
		t.Fatal(err)
	}
	fakeBin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(fakeBin, 0755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
test "$1" = "toolchain/python-src/scripts/graphics/build_graphics.py"
test "$MARY_SKELTER_GRAPHICS_NATIVE_BILZ" = "$EXPECTED_NATIVE_BILZ"
test -n "$MARY_SKELTER_GRAPHICS_CONTAINER_HELPER"
SOURCE_SHA=$(sha256sum .local/build/rebuilt/MSF_PC_LOCALIZED/masterWindows/GxArchivedFile074.dat | cut -d' ' -f1)
cat > "$MARY_SKELTER_GRAPHICS_REPORT" <<JSON
{"enabled_assets":1,"encoder":{},"results":[{"id":"TEST_GRAPHICS","dataset":"MSF_PC_LOCALIZED","archive":74,"renderer":"raster_atlas_v1","archive_sha256_before_graphics":"$SOURCE_SHA","archive_sha256_after_graphics":"$SOURCE_SHA","graphics_only_archive_seeded":false}],"status":"PASS"}
JSON
`
	if err := os.WriteFile(filepath.Join(fakeBin, "python3"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "0") // isolated fake Python backend; production defaults to Go
	c := CachedSources{FinaleMaster: master}
	for _, mode := range []string{"", "0", "1"} {
		t.Run(fmt.Sprintf("mode_%q", mode), func(t *testing.T) {
			t.Setenv("MS_PC_GRAPHICS_NATIVE_BILZ", mode)
			expected := mode
			if expected == "" {
				expected = "1"
			} // native is now the default
			t.Setenv("EXPECTED_NATIVE_BILZ", expected)
			if err := BuildGraphics(root, c, nil, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Setenv("MS_PC_GRAPHICS_NATIVE_BILZ", "yes")
	if err := BuildGraphics(root, c, nil, nil); err == nil || !strings.Contains(err.Error(), "must be 0 or 1") {
		t.Fatalf("invalid opt-in value accepted: %v", err)
	}
}
