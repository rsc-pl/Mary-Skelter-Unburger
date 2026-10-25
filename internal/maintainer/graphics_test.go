package maintainer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"maryskelter-unburger/internal/updater"
)

func writeGraphicsFixture(t *testing.T, root string, duplicate bool) {
	t.Helper()
	assets := []map[string]any{{
		"id": "TEST_GRAPHICS", "enabled": true, "dataset": "MSF_PC_LOCALIZED",
		"archive": 74, "gdat_index": 8, "cl3_inner_name": "commonBE.tid",
		"renderer": "raster_atlas_v1",
		"source_variants": []map[string]any{{
			"tid_sha256": strings.Repeat("a", 64), "width": 64, "height": 64, "format": "DXT5",
		}},
	}}
	if duplicate {
		assets = append(assets, assets[0])
	}
	raw, err := json.Marshal(map[string]any{"schema_version": 2, "assets": assets})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "graphics"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "graphics", "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGraphicsManifestValidation(t *testing.T) {
	root := t.TempDir()
	writeGraphicsFixture(t, root, false)
	_, enabled, err := loadGraphicsManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].ID != "TEST_GRAPHICS" {
		t.Fatalf("enabled=%+v", enabled)
	}

	root2 := t.TempDir()
	writeGraphicsFixture(t, root2, true)
	if _, _, err := loadGraphicsManifest(root2); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate manifest accepted: %v", err)
	}
}

func TestBuildGraphicsSeedsAndOwnsCanonicalReport(t *testing.T) {
	allowLegacyGraphicsUnitTests = true
	t.Cleanup(func() { allowLegacyGraphicsUnitTests = false })

	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell fake python3")
	}
	root := t.TempDir()
	writeGraphicsFixture(t, root, false)
	master := filepath.Join(root, "pristine", "masterWindows")
	if err := os.MkdirAll(master, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(master, "GxArchivedFile074.dat")
	if err := os.WriteFile(src, []byte("pristine-archive"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(bin, "python3")
	sha, err := updater.SHA256File(src)
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
set -eu
test "$1" = "toolchain/python-src/scripts/graphics/build_graphics.py"
test -n "$MARY_SKELTER_GRAPHICS_CONTAINER_HELPER"
test -n "$MARY_SKELTER_GRAPHICS_TEXTURE_HELPER"
test "$MARY_SKELTER_GRAPHICS_CONTAINER_HELPER" = "$MARY_SKELTER_GRAPHICS_TEXTURE_HELPER"
test -f ".local/build/rebuilt/MSF_PC_LOCALIZED/masterWindows/GxArchivedFile074.dat"
cat > "$MARY_SKELTER_GRAPHICS_REPORT" <<'JSON'
{
  "enabled_assets": 1,
  "encoder": {"helper":"fixture"},
  "results": [{"id":"TEST_GRAPHICS","dataset":"MSF_PC_LOCALIZED","archive":74,"renderer":"raster_atlas_v1","archive_sha256_before_graphics":"%s","archive_sha256_after_graphics":"%s","graphics_only_archive_seeded":false}],
  "status": "PASS"
}
JSON
`, sha, sha)
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+oldPath)

	t.Setenv("MS_PC_GRAPHICS_NATIVE_BC7", "0") // isolated fake Python backend; production defaults to Go
	c := CachedSources{FinaleMaster: master}
	if err := BuildGraphics(root, c, nil, nil); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, ".local", "build", "rebuilt", "MSF_PC_LOCALIZED", "masterWindows", "GxArchivedFile074.dat")
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "pristine-archive" {
		t.Fatalf("seeded archive=%q", got)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".local", "build", "reports", "graphics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rep graphicsBackendReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) != 1 || !rep.Results[0].GraphicsOnlyArchiveSeeded {
		t.Fatalf("canonical report did not restore Go seed evidence: %+v", rep)
	}
	if _, err := os.Stat(filepath.Join(root, ".local", "build", "reports", "graphics.backend.json")); !os.IsNotExist(err) {
		t.Fatalf("backend report should be consumed, stat err=%v", err)
	}
}

func TestCanonicalizeGraphicsReportRejectsOrderDrift(t *testing.T) {
	assets := []graphicsAssetSpec{{ID: "A", Dataset: "MS2_PC_LOCALIZED", Archive: 55, Renderer: "raster_atlas_v1"}}
	raw := []byte(`{"enabled_assets":1,"encoder":{},"results":[{"id":"B","dataset":"MS2_PC_LOCALIZED","archive":55,"renderer":"raster_atlas_v1"}],"status":"PASS"}`)
	if _, err := canonicalizeGraphicsReport("", raw, assets, nil, nil); err == nil {
		t.Fatal("backend result order/id drift accepted")
	}
}

func TestRepositoryGraphicsManifestValid(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manifest, enabled, err := loadGraphicsManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 2 || len(enabled) != 31 {
		t.Fatalf("repository graphics manifest schema/assets = %d/%d", manifest.SchemaVersion, len(enabled))
	}
}

func TestCanonicalizeGraphicsReportRejectsArchiveChainDrift(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".local", "build", "rebuilt", "MS2_PC_LOCALIZED", "masterWindows")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	dat := filepath.Join(path, "GxArchivedFile055.dat")
	if err := os.WriteFile(dat, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := updater.SHA256File(dat)
	if err != nil {
		t.Fatal(err)
	}
	before := strings.Repeat("1", 64)
	assets := []graphicsAssetSpec{
		{ID: "A", Dataset: "MS2_PC_LOCALIZED", Archive: 55, Renderer: "raster_atlas_v1"},
		{ID: "B", Dataset: "MS2_PC_LOCALIZED", Archive: 55, Renderer: "raster_atlas_v1"},
	}
	raw := []byte(fmt.Sprintf(`{"enabled_assets":2,"encoder":{},"results":[{"id":"A","dataset":"MS2_PC_LOCALIZED","archive":55,"renderer":"raster_atlas_v1","archive_sha256_before_graphics":"%s","archive_sha256_after_graphics":"%s"},{"id":"B","dataset":"MS2_PC_LOCALIZED","archive":55,"renderer":"raster_atlas_v1","archive_sha256_before_graphics":"%s","archive_sha256_after_graphics":"%s"}],"status":"PASS"}`, before, after, before, after))
	baseline := map[graphicsArchiveKey]string{{Dataset: "MS2_PC_LOCALIZED", Archive: 55}: before}
	if _, err := canonicalizeGraphicsReport(root, raw, assets, nil, baseline); err == nil || !strings.Contains(err.Error(), "chain drift") {
		t.Fatalf("archive chain drift accepted: %v", err)
	}
}
