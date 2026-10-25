// mary-skelter-runtime-qa performs read-only production runtime checks.
// It does not modify the project, fonts, private source DAT or game output.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"maryskelter-unburger/internal/graphicsruntime"
)

type report struct {
	Schema         int    `json:"schema"`
	Platform       string `json:"platform"`
	Python         string `json:"python"`
	Pillow         string `json:"pillow"`
	NumPy          string `json:"numpy"`
	RuntimeSHA     string `json:"runtime_manifest_sha256_validated"`
	Smoke          string `json:"native_import_and_graphics_smoke"`
	SwitchSelfTest string `json:"switch_canonical_self_test"`
	Status         string `json:"status"`
}

func check(root string, switchTest bool) (report, error) {
	r := report{Schema: 1, Platform: runtime.GOOS + "-" + runtime.GOARCH, Status: "FAIL"}
	if !filepath.IsAbs(root) {
		return r, fmt.Errorf("absolute --root required")
	}
	py, err := graphicsruntime.Resolve(root)
	if err != nil {
		return r, err
	}
	r.RuntimeSHA = "PASS"
	r.Python, r.Pillow, r.NumPy = py.PythonVersion, py.PillowVersion, py.NumPyVersion
	if err := py.Smoke(); err != nil {
		return r, err
	}
	r.Smoke = "PASS"
	r.SwitchSelfTest = "NOT_RUN"
	if switchTest {
		script, err := privateassets.Resolve(root, "renderer-switch-script")
		if err != nil {
			return r, err
		}
		cmd, err := py.GraphicsCommand(root, script, "--project-root", root, "--self-test")
		if err != nil {
			return r, err
		}
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return r, fmt.Errorf("read-only Switch canonical self-test: %w: %s", err, truncate(out.String(), 500))
		}
		if !strings.Contains(out.String(), "R32C3 SELF_TEST PASS") {
			return r, fmt.Errorf("Switch self-test did not produce expected PASS marker: %s", truncate(out.String(), 500))
		}
		r.SwitchSelfTest = "PASS"
	}
	r.Status = "PASS"
	return r, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func main() {
	root := flag.String("root", "", "Absolute path to installed Mary Skelter project")
	switchTest := flag.Bool("switch-self-test", true, "Run read-only Switch graphics manifest/IPS self-test")
	flag.Parse()
	r, err := check(*root, *switchTest)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err != nil {
		_ = enc.Encode(r)
		fmt.Fprintln(os.Stderr, "RUNTIME QA FAILED:", err)
		os.Exit(1)
	}
	if err := enc.Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
