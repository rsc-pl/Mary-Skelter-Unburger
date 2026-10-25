package maintainer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func nativeGraphicsFixture(t *testing.T) string {
	t.Helper()
	original, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"manifest.json", "canon_replacements.json", "ui_typography.json"} {
		src := filepath.Join(original, "graphics", name)
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(root, "graphics", name)
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(original, "graphics", "assets", "room_selector", "selected_bg.png")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "graphics", "assets", "room_selector", "selected_bg.png")
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestNativeGraphicsPreflightBaseline(t *testing.T) {
	root := nativeGraphicsFixture(t)
	if err := VerifyGraphicsConfigNative(root, true); err != nil {
		t.Fatal(err)
	}
	if err := VerifyGraphicsConfigNative(root, false); err == nil || !strings.Contains(err.Error(), "font file missing") {
		t.Fatalf("must require private fonts: %v", err)
	}
}
func TestNativeGraphicsPreflightRejectsDrift(t *testing.T) {
	tests := []struct {
		name, file string
		mutate     func(any)
	}{
		{"manifest-schema", "manifest.json", func(x any) { gcObj(x)["schema_version"] = 3.0 }},
		{"manifest-internal-metadata", "manifest.json", func(x any) { gcObj(gcAt(gcGet(x, "assets"), 0))["runtime_evidence"] = "private" }},
		{"manifest-source-tid", "manifest.json", func(x any) {
			for _, a := range gcArr(gcGet(x, "assets")) {
				if gcStr(gcGet(a, "id")) == "MS2_CAMP_TOP2_CHARACTER_NAMES" {
					gcObj(gcAt(gcGet(a, "source_variants"), 0))["tid_sha256"] = strings.Repeat("1", 64)
				}
			}
		}},
		{"manifest-bad-footer", "manifest.json", func(x any) {
			for _, a := range gcArr(gcGet(x, "assets")) {
				if gcStr(gcGet(a, "id")) == "MS2_TITLE_MOD_FOOTER_MAIN" {
					gcObj(a)["cl3_inner_name"] = "title_moji.tid"
				}
			}
		}},
		{"manifest-drift-row", "manifest.json", func(x any) {
			for _, a := range gcArr(gcGet(x, "assets")) {
				if gcStr(gcGet(a, "id")) == "MS2_HEROINE_ROOM_LIST_BLUE" {
					gcObj(gcAt(gcGet(a, "rows"), 0))["name"] = "LOCALE"
				}
			}
		}},
		{"canon-name", "canon_replacements.json", func(x any) { gcObj(gcGet(gcGet(x, "character_labels"), "MS2"))["OTSUU"] = "OTSUU" }},
		{"typography-preset", "ui_typography.json", func(x any) { delete(gcObj(gcGet(x, "presets")), "r32c3_select_subtitle") }},
		{"typography-font-reference", "ui_typography.json", func(x any) { gcObj(gcGet(gcGet(x, "fonts"), "neofara"))["reference_sha256"] = "none" }},
		{"typography-battle-angle", "ui_typography.json", func(x any) { gcObj(gcGet(gcGet(x, "presets"), "battle_nameplate"))["rotation_degrees"] = 0.0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := nativeGraphicsFixture(t)
			p := filepath.Join(root, "graphics", tc.file)
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			var obj any
			if err := json.Unmarshal(b, &obj); err != nil {
				t.Fatal(err)
			}
			tc.mutate(obj)
			b, err = json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, b, 0644); err != nil {
				t.Fatal(err)
			}
			if err := VerifyGraphicsConfigNative(root, true); err == nil {
				t.Fatal("invalid configuration accepted")
			}
			// The unmodified Python validator must independently reject the
			// same mutation. It is a test oracle, never a production subprocess.
			py, pyErr := exec.LookPath("python3")
			if pyErr == nil {
				sourceRoot, err := filepath.Abs(filepath.Join("..", ".."))
				if err != nil {
					t.Fatal(err)
				}
				original := filepath.Join(sourceRoot, "toolchain", "python-src", "scripts", "graphics", "verify_graphics_config.py")
				data, err := os.ReadFile(original)
				if err != nil {
					t.Fatal(err)
				}
				dest := filepath.Join(root, "toolchain", "python-src", "scripts", "graphics", "verify_graphics_config.py")
				if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dest, data, 0644); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(py, dest, "--skip-font-presence")
				cmd.Dir = root
				cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
				if out, err := cmd.CombinedOutput(); err == nil {
					t.Fatalf("Python oracle accepted the same mutation: %s", out)
				}
			}
		})
	}
}
func TestNativeGraphicsPythonOracleOnActual(t *testing.T) {
	// The legacy validator is an independent parity oracle, not a production dependency.
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyGraphicsConfigNative(root, true); err != nil {
		t.Fatal(err)
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python oracle not installed")
	}
	cmd := exec.Command(py, "toolchain/python-src/scripts/graphics/verify_graphics_config.py", "--skip-font-presence")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python oracle drift: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "graphics config: PASS") {
		t.Fatalf("unexpected Python oracle output: %s", out)
	}
}
