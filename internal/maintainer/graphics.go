package maintainer

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"

	"maryskelter-unburger/internal/graphicsruntime"

	"maryskelter-unburger/internal/updater"
)

// Never set by production: legacy fake-Python tests exercise container
// postprocessing without carrying the >300 MiB public runtime fixture.
var allowLegacyGraphicsUnitTests bool

type graphicsVariant struct {
	TIDSHA256 string `json:"tid_sha256"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Format    string `json:"format"`
}

type graphicsTarget struct {
	TIDSHA256 string `json:"tid_sha256"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Format    string `json:"format"`
}

type graphicsAssetSpec struct {
	ID             string            `json:"id"`
	Enabled        bool              `json:"enabled"`
	Dataset        string            `json:"dataset"`
	Archive        int               `json:"archive"`
	Renderer       string            `json:"renderer"`
	Grouped        bool              `json:"grouped"`
	SourceVariants []graphicsVariant `json:"source_variants"`
	Targets        []graphicsTarget  `json:"targets"`
}

type graphicsManifestSpec struct {
	SchemaVersion int                 `json:"schema_version"`
	Assets        []graphicsAssetSpec `json:"assets"`
}

type graphicsBackendResult struct {
	ID                          string `json:"id"`
	Dataset                     string `json:"dataset"`
	Archive                     int    `json:"archive"`
	Renderer                    string `json:"renderer"`
	ArchiveSHA256BeforeGraphics string `json:"archive_sha256_before_graphics"`
	ArchiveSHA256AfterGraphics  string `json:"archive_sha256_after_graphics"`
	GraphicsOnlyArchiveSeeded   bool   `json:"graphics_only_archive_seeded"`
}

type graphicsBackendReport struct {
	EnabledAssets int                     `json:"enabled_assets"`
	Encoder       json.RawMessage         `json:"encoder"`
	Results       []graphicsBackendResult `json:"results"`
	Status        string                  `json:"status"`
}

var graphicsRenderers = map[string]bool{
	"result_nameplate_v2":                   true,
	"raster_atlas_v1":                       true,
	"font_atlas_v2":                         true,
	"multiline_raster_labels_v1":            true,
	"ms2_living_quarters_selector_v1":       true,
	"ms2_myroom_tsuu_direct_v1":             true,
	"ms2_myroom_title_font_v2":              true,
	"ms2_myroom_title_font_v3":              true,
	"ms1_residence_names_font_v1":           true,
	"ms1_residence_names_raster_v2":         true,
	"mixedcase_raster_atlas_v1":             true,
	"ms2_battle_nameplates_v1":              true,
	"ms2_battle_nameplates_font_v2":         true,
	"ms2_battle_nameplates_font_v3":         true,
	"msf_battle_nameplates_v1":              true,
	"msf_battle_nameplates_font_v2":         true,
	"ms2_heroine_room_list_v1":              true,
	"ms2_room_selector_font_v4":             true,
	"ms2_heroine_room_blue_font_v4":         true,
	"ms2_heroine_room_blue_font_v5":         true,
	"ms2_heroine_room_blue_font_v6":         true,
	"ms2_camp_top2_character_names_font_v3": true,
	"ms2_affection_font_v2":                 true,
	"ms2_camp_top_legacy_names_font_v2":     true,
	"ms2_name_family_font_v1":               true,
	"ms2_result_font_v2":                    true,
	"title_footer_chiaro_v1":                true,
	"title_copyright_footer_chiaro_v2":      true,
	"ms2_dlc_job_labels_v1":                 true,
	"ms2_heroine_room_banners_v1":           true,
	"ms2_heroine_room_banners_font_v2":      true,
	"ms2_heroine_room_banners_raster_v3":    true,
}

func validGraphicsSHA(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validGraphicsTexture(v graphicsVariant) bool {
	return validGraphicsSHA(v.TIDSHA256) && v.Width > 0 && v.Height > 0 && (v.Format == "BC7" || v.Format == "DXT5")
}

func loadGraphicsManifest(root string) (graphicsManifestSpec, []graphicsAssetSpec, error) {
	var manifest graphicsManifestSpec
	raw, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return manifest, nil, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, nil, fmt.Errorf("parse graphics manifest: %w", err)
	}
	if manifest.SchemaVersion != 2 {
		return manifest, nil, fmt.Errorf("unsupported graphics manifest schema %d", manifest.SchemaVersion)
	}
	ids := map[string]bool{}
	var enabled []graphicsAssetSpec
	for i, a := range manifest.Assets {
		if a.ID == "" {
			return manifest, nil, fmt.Errorf("graphics asset %d has no id", i)
		}
		if ids[a.ID] {
			return manifest, nil, fmt.Errorf("duplicate graphics asset id %s", a.ID)
		}
		ids[a.ID] = true
		if !a.Enabled {
			continue
		}
		if a.Dataset != "MS2_PC_LOCALIZED" && a.Dataset != "MSF_PC_LOCALIZED" {
			return manifest, nil, fmt.Errorf("%s: unsupported graphics dataset %q", a.ID, a.Dataset)
		}
		if a.Archive < 0 || a.Renderer == "" || !graphicsRenderers[a.Renderer] {
			return manifest, nil, fmt.Errorf("%s: invalid archive/renderer (%d, %q)", a.ID, a.Archive, a.Renderer)
		}
		if a.Grouped {
			if len(a.Targets) == 0 {
				return manifest, nil, fmt.Errorf("%s: grouped graphics asset has no targets", a.ID)
			}
			for j, t := range a.Targets {
				if !validGraphicsSHA(t.TIDSHA256) || t.Width <= 0 || t.Height <= 0 || (t.Format != "BC7" && t.Format != "DXT5") {
					return manifest, nil, fmt.Errorf("%s: invalid grouped target %d source guard", a.ID, j)
				}
			}
		} else {
			if len(a.SourceVariants) == 0 {
				return manifest, nil, fmt.Errorf("%s: graphics asset has no source variants", a.ID)
			}
			for j, v := range a.SourceVariants {
				if !validGraphicsTexture(v) {
					return manifest, nil, fmt.Errorf("%s: invalid source variant %d", a.ID, j)
				}
			}
		}
		enabled = append(enabled, a)
	}
	if len(enabled) == 0 {
		return manifest, nil, fmt.Errorf("graphics manifest has no enabled assets")
	}
	return manifest, enabled, nil
}

type graphicsArchiveKey struct {
	Dataset string
	Archive int
}

func graphicsSourceMaster(c CachedSources, dataset string) (string, error) {
	switch dataset {
	case "MS2_PC_LOCALIZED":
		return c.MS2Master, nil
	case "MSF_PC_LOCALIZED":
		return c.FinaleMaster, nil
	default:
		return "", fmt.Errorf("unsupported graphics dataset %s", dataset)
	}
}

func seedGraphicsArchives(root string, c CachedSources, assets []graphicsAssetSpec, stdout io.Writer) (map[graphicsArchiveKey]bool, map[graphicsArchiveKey]string, error) {
	required := map[graphicsArchiveKey]bool{}
	for _, a := range assets {
		required[graphicsArchiveKey{a.Dataset, a.Archive}] = true
	}
	keys := make([]graphicsArchiveKey, 0, len(required))
	for k := range required {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Dataset == keys[j].Dataset {
			return keys[i].Archive < keys[j].Archive
		}
		return keys[i].Dataset < keys[j].Dataset
	})
	seeded := map[graphicsArchiveKey]bool{}
	baseline := map[graphicsArchiveKey]string{}
	for _, k := range keys {
		dst := filepath.Join(root, ".local", "build", "rebuilt", k.Dataset, "masterWindows", fmt.Sprintf("GxArchivedFile%03d.dat", k.Archive))
		if !file(dst) {
			master, err := graphicsSourceMaster(c, k.Dataset)
			if err != nil {
				return nil, nil, err
			}
			src := filepath.Join(master, fmt.Sprintf("GxArchivedFile%03d.dat", k.Archive))
			if !file(src) {
				return nil, nil, fmt.Errorf("%s graphics-only archive %03d missing from text rebuild and pristine source: %s", k.Dataset, k.Archive, src)
			}
			if err := copyFile(src, dst); err != nil {
				return nil, nil, fmt.Errorf("seed graphics archive %s: %w", filepath.Base(src), err)
			}
			seeded[k] = true
			if stdout != nil {
				fmt.Fprintf(stdout, "GRAPHICS: seeded graphics-only archive from pristine source: %s (%s)\n", filepath.Base(src), k.Dataset)
			}
		}
		sha, err := updater.SHA256File(dst)
		if err != nil {
			return nil, nil, fmt.Errorf("hash graphics baseline %s: %w", dst, err)
		}
		baseline[k] = sha
	}
	return seeded, baseline, nil
}

func canonicalizeGraphicsReport(root string, raw []byte, assets []graphicsAssetSpec, seeded map[graphicsArchiveKey]bool, baseline map[graphicsArchiveKey]string) ([]byte, error) {
	var rep graphicsBackendReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("parse graphics backend report: %w", err)
	}
	if rep.Status != "PASS" {
		return nil, fmt.Errorf("graphics backend report status %q", rep.Status)
	}
	if rep.EnabledAssets != len(assets) || len(rep.Results) != len(assets) {
		return nil, fmt.Errorf("graphics backend result count drift: enabled=%d results=%d expected=%d", rep.EnabledAssets, len(rep.Results), len(assets))
	}
	last := map[graphicsArchiveKey]string{}
	seedReported := map[graphicsArchiveKey]bool{}
	for i, a := range assets {
		r := &rep.Results[i]
		if r.ID != a.ID || r.Dataset != a.Dataset || r.Archive != a.Archive || r.Renderer != a.Renderer {
			return nil, fmt.Errorf("graphics backend result %d drift: got %+v expected %s/%s/%03d/%s", i, *r, a.ID, a.Dataset, a.Archive, a.Renderer)
		}
		if !validGraphicsSHA(r.ArchiveSHA256BeforeGraphics) || !validGraphicsSHA(r.ArchiveSHA256AfterGraphics) {
			return nil, fmt.Errorf("%s: graphics backend archive SHA evidence malformed", a.ID)
		}
		k := graphicsArchiveKey{a.Dataset, a.Archive}
		expectedBefore := baseline[k]
		if prev := last[k]; prev != "" {
			expectedBefore = prev
		}
		if expectedBefore == "" || r.ArchiveSHA256BeforeGraphics != expectedBefore {
			return nil, fmt.Errorf("%s: graphics archive chain drift before render: got %s expected %s", a.ID, r.ArchiveSHA256BeforeGraphics, expectedBefore)
		}
		last[k] = r.ArchiveSHA256AfterGraphics
		// Match the old backend report semantics: only the first asset touching an
		// archive that Go had to seed is marked as the seeding operation.
		r.GraphicsOnlyArchiveSeeded = seeded[k] && !seedReported[k]
		if r.GraphicsOnlyArchiveSeeded {
			seedReported[k] = true
		}
	}
	for k, want := range last {
		path := filepath.Join(root, ".local", "build", "rebuilt", k.Dataset, "masterWindows", fmt.Sprintf("GxArchivedFile%03d.dat", k.Archive))
		got, err := updater.SHA256File(path)
		if err != nil {
			return nil, fmt.Errorf("hash final graphics archive %s: %w", path, err)
		}
		if got != want {
			return nil, fmt.Errorf("final graphics archive SHA drift for %s/%03d: got %s report %s", k.Dataset, k.Archive, got, want)
		}
	}
	// Preserve backend encoder evidence while making the canonical report
	// deterministic and owned by the Go orchestrator.
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// BuildGraphics owns graphics manifest validation, source/archive selection,
// graphics-only archive seeding, work/report lifecycle and report verification.
// Pillow/NumPy owns rasterization; the source-aware BCn codec and GDAT/CL3/BILZ
// writer are implemented in native Go. An isolated vendored Python is preferred.
func BuildGraphics(root string, c CachedSources, stdout, stderr io.Writer) error {
	_, assets, err := loadGraphicsManifest(root)
	if err != nil {
		return err
	}
	// legacy: explicit native BC7 encoder mode. The offline pure-Go bcn binary
	// is supplied by the caller; never download or compile C at build time.
	nativeBC7 := os.Getenv("MS_PC_GRAPHICS_NATIVE_BC7")
	if nativeBC7 == "" {
		nativeBC7 = "1"
	} // Native BC7 is mandatory by default; no network/C compilation.
	if nativeBC7 != "0" && nativeBC7 != "1" {
		return fmt.Errorf("MS_PC_GRAPHICS_NATIVE_BC7 must be 0 or 1")
	}
	bc7Bin := ""
	if nativeBC7 == "1" {
		bc7Bin = os.Getenv("MS_PC_GRAPHICS_BCN_BINARY")
		if bc7Bin == "" {
			platform := runtime.GOOS + "-" + runtime.GOARCH
			name := "bcn-tid-go"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			bc7Bin = filepath.Join(root, "toolchain", "bin", platform, name)
			if err := verifyBundledEncoderBC7(platform, bc7Bin); err != nil {
				return err
			}
		}
		if !filepath.IsAbs(bc7Bin) {
			return fmt.Errorf("MS_PC_GRAPHICS_BCN_BINARY must be an absolute executable path with MS_PC_GRAPHICS_NATIVE_BC7=1")
		}
		st, err := os.Stat(bc7Bin)
		if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0111 == 0) {
			return fmt.Errorf("MS_PC_GRAPHICS_BCN_BINARY missing or not executable")
		}
	}
	// Resolve runtime and verify hashes BEFORE touching any .local output or
	// copying large source DATs into staging; failed runtime must be zero-write.
	mode := os.Getenv("MS_BUNDLED_GRAPHICS_PYTHON")
	if mode != "" && mode != "0" && mode != "1" {
		return fmt.Errorf("MS_BUNDLED_GRAPHICS_PYTHON must be 0 or 1")
	}
	// Production graphics always require the separately installed, SHA-pinned
	// public runtime. A missing bundle must fail before staging any source DAT.
	// Old fake-python container tests have an in-process, unexported test hook;
	// they are not a supported production fallback or environment override.
	if mode == "0" && !allowLegacyGraphicsUnitTests {
		return fmt.Errorf("system Python graphics are not supported; public runtime required")
	}
	bundledGraphics := !allowLegacyGraphicsUnitTests || mode == "1"
	var verifiedGraphicsPython graphicsruntime.Python
	if bundledGraphics {
		if nativeBC7 != "1" {
			return fmt.Errorf("bundled graphics forbids legacy C BC7")
		}
		var e error
		verifiedGraphicsPython, e = graphicsruntime.Resolve(root)
		if e != nil {
			return e
		}
		if e := verifiedGraphicsPython.Smoke(); e != nil {
			return e
		}
	}
	build := filepath.Join(root, ".local", "build")
	work := filepath.Join(build, "graphics")
	backendReport := filepath.Join(build, "reports", "graphics.backend.json")
	finalReport := filepath.Join(build, "reports", "graphics.json")
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(finalReport), 0o755); err != nil {
		return err
	}
	_ = os.Remove(backendReport)
	_ = os.Remove(finalReport)

	seeded, baseline, err := seedGraphicsArchives(root, c, assets, stdout)
	if err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "[native-go] Graphics plan: %d asset(s), %d archive(s), %d graphics-only seed(s)\n", len(assets), countGraphicsArchives(assets), len(seeded))
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve builder executable for graphics container helper: %w", err)
	}
	// legacy: Native Go BILZ/CL3/GDAT publication is the default for every
	// graphics family. The retained Python renderer still owns raster and
	// texture encoding until those families finish their independent Go QA.
	// MS_PC_GRAPHICS_NATIVE_BILZ=0 is an explicit, documented comparison-only
	// escape hatch for the historical canonical zlib output; never silently
	// fall back to it after a native failure.
	nativeBILZ := os.Getenv("MS_PC_GRAPHICS_NATIVE_BILZ")
	if nativeBILZ == "" {
		nativeBILZ = "1"
	}
	if nativeBILZ != "" && nativeBILZ != "0" && nativeBILZ != "1" {
		return fmt.Errorf("MS_PC_GRAPHICS_NATIVE_BILZ must be 0 or 1")
	}
	env := append(c.env(),
		"MARY_SKELTER_GRAPHICS_NATIVE_BILZ="+nativeBILZ,
		"MARY_SKELTER_GRAPHICS_NATIVE_BC7="+nativeBC7,
		"MARY_SKELTER_GRAPHICS_BCN_BINARY="+bc7Bin,
		"MARY_SKELTER_GRAPHICS_WORK="+work,
		"MARY_SKELTER_GRAPHICS_REPORT="+backendReport,
		"MARY_SKELTER_GRAPHICS_GO_ORCHESTRATED=1",
		"MARY_SKELTER_GRAPHICS_CONTAINER_HELPER="+exe,
		"MARY_SKELTER_GRAPHICS_TEXTURE_HELPER="+exe,
	)
	if bundledGraphics {
		py := verifiedGraphicsPython
		cmd, commandErr := py.GraphicsCommand(root, "toolchain/python-src/scripts/graphics/build_graphics.py")
		if commandErr != nil {
			return commandErr
		}
		cmd.Dir = root
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		cmd.Stdin = os.Stdin
		cmd.Env = append(cmd.Env, env...)
		if e := cmd.Run(); e != nil {
			return fmt.Errorf("bundled graphics raster: %w", e)
		}
	} else {
		if !allowLegacyGraphicsUnitTests {
			return fmt.Errorf("bundled Python graphics runtime is required")
		}
		cmd := exec.Command("python3", "toolchain/python-src/scripts/graphics/build_graphics.py")
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, os.Stdin
		cmd.Env = append(os.Environ(), env...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("graphics fixture: %w", err)
		}
	}
	raw, err := os.ReadFile(backendReport)
	if err != nil {
		return fmt.Errorf("read graphics backend report: %w", err)
	}
	canon, err := canonicalizeGraphicsReport(root, raw, assets, seeded, baseline)
	if err != nil {
		return err
	}
	tmp := finalReport + ".tmp"
	if err := os.WriteFile(tmp, canon, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, finalReport); err != nil {
		return err
	}
	_ = os.Remove(backendReport)
	if stdout != nil {
		fmt.Fprintf(stdout, "GRAPHICS ORCHESTRATION: PASS (%d canonical asset(s)); report: %s\n", len(assets), finalReport)
	}
	return nil
}

func countGraphicsArchives(assets []graphicsAssetSpec) int {
	m := map[string]bool{}
	for _, a := range assets {
		m[fmt.Sprintf("%s:%03d", a.Dataset, a.Archive)] = true
	}
	return len(m)
}
