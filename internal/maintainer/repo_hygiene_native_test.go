package maintainer

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHygieneFile(t *testing.T, root, path string, content []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, content, 0644); err != nil {
		t.Fatal(err)
	}
}
func TestRepoHygieneNativeCleanAndAdverse(t *testing.T) {
	root := t.TempDir()
	writeHygieneFile(t, root, "README.md", nil)
	if err := CheckRepoHygieneNative(root); err != nil {
		t.Fatalf("clean root: %v", err)
	}
	// Private research files are intentionally ignored.
	writeHygieneFile(t, root, ".local/private_fixture/data.go", []byte("research"))
	writeHygieneFile(t, root, ".git/config", []byte("private"))
	if err := CheckRepoHygieneNative(root); err != nil {
		t.Fatalf("private files should be ignored: %v", err)
	}
	writeHygieneFile(t, root, "README.md", []byte("# Finished public README written by the owner\n"))
	writeHygieneFile(t, root, "graphics/untracked.png", []byte{1, 2})
	writeHygieneFile(t, root, "scripts/graphics/capture_name_assets.py", []byte("research"))
	writeHygieneFile(t, root, "scripts/test.go", []byte("package p\n// "+".local/"+"docs"+" is private\n"))
	writeHygieneFile(t, root, "install-Mary-Skelter-UNBURGER-legacy-Cumulative-RECOVERY.sh", []byte("#!/bin/sh\n"))
	writeHygieneFile(t, root, "third_party.zip", []byte("untracked private owner archive"))
	err := CheckRepoHygieneNative(root)
	if err == nil {
		t.Fatal("adverse root unexpectedly accepted")
	}
	for _, s := range []string{"unexpected public graphics file", "research helper must be local-only", "public implementation depends on private project state", "private recovery artifact in public root"} {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("missing %q: %v", s, err)
		}
	}
}
func TestRepoHygieneNativeFontHashAndConfig(t *testing.T) {
	root := t.TempDir()
	writeHygieneFile(t, root, "README.md", nil)
	font := []byte("test-face")
	sha := sha256.Sum256(font)
	writeHygieneFile(t, root, "third_party/fonts/sample.otf", font)
	cfg := `{"schema_version":1,"fonts":{"sample":{"repo_path":"third_party/fonts/sample.otf","reference_sha256":"` + hex.EncodeToString(sha[:]) + `"}},"presets":{}}`
	writeHygieneFile(t, root, "graphics/ui_typography.json", []byte(cfg))
	writeHygieneFile(t, root, "graphics/manifest.json", []byte(`{"schema_version":1,"assets":[]}`))
	if err := CheckRepoHygieneNative(root); err != nil {
		t.Fatalf("valid font: %v", err)
	}
	writeHygieneFile(t, root, "third_party/fonts/sample.otf", []byte("changed"))
	err := CheckRepoHygieneNative(root)
	if err == nil || !strings.Contains(err.Error(), "public build font hash mismatch") {
		t.Fatalf("wrong hash not detected: %v", err)
	}
}

// Regression for the installed full-release workflow: audit/recovery are
// legitimate terms and vendored sources must not influence project QA.
func TestRepoHygieneNativeScopedAuthoredInputs(t *testing.T) {
	root := t.TempDir()
	writeHygieneFile(t, root, "README.md", nil)
	writeHygieneFile(t, root, "cmd/mary-skelter-builder/pc_master_audit_native.go", []byte("package main\n// legitimate source integrity audit\n"))
	writeHygieneFile(t, root, "internal/privateassets/locator.go", []byte("package privateassets\nconst index = \".local/internal/private_asset_locations.json\"\n"))
	writeHygieneFile(t, root, "internal/maintainer/rebuild_safety_gate.go", []byte("package maintainer\nconst note = \"RECOVERY_NOTE.txt\"\n"))
	writeHygieneFile(t, root, "toolchain/python-runtime/linux-amd64/python/lib/python3.13/pickle.py", []byte("# PH"+"ASE123 and RECO\n"+"VERY123"))
	writeHygieneFile(t, root, "third_party/go/xsys/windows/svc/mgr/recovery.go", []byte("package mgr\n// PH"+"ASE123\n"))
	writeHygieneFile(t, root, "toolchain/python-runtime/linux-amd64/site-packages/auditwheel/audit.json", []byte("{}"))
	if err := CheckRepoHygieneNative(root); err != nil {
		t.Fatalf("valid owned source and upstream libraries refused: %v", err)
	}
	writeHygieneFile(t, root, "internal/maintainer/bad.go", []byte("package maintainer\n// "+"PH"+"ASE9000_DEVELOPMENT\n"))
	if err := CheckRepoHygieneNative(root); err == nil || !strings.Contains(err.Error(), "development-only names") {
		t.Fatalf("failed to detect real authored development marker: %v", err)
	}
}

// Exercising the actual source tree prevents the checker itself from
// introducing a new false positive that mock-only tests would miss.
// Private font bytes are excluded from source-only snapshots by design.
func TestRepoHygieneNativeActualAuthoredTree(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	err = CheckRepoHygieneNative(root)
	if err == nil {
		return
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) == 0 || lines[0] != "repo hygiene FAIL:" {
		t.Fatal(err)
	}
	allowedMissing := map[string]bool{"avionic": true, "avionic_regular": true, "chiaro": true, "neofara": true, "gebryna": true}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		index := strings.Index(line, ": public build font missing: ")
		if index < 0 || !allowedMissing[line[:index]] {
			t.Fatalf("unexpected real-tree repository hygiene finding: %s", line)
		}
	}
}

func TestRepoHygieneAcceptsUserAuthoredREADME(t *testing.T) {
	root := t.TempDir()
	writeHygieneFile(t, root, "README.md", []byte("# Custom public README\n\nAlready prepared for publication.\n"))
	if err := CheckRepoHygieneNative(root); err != nil {
		t.Fatalf("custom README rejected: %v", err)
	}
}
func TestRepoHygieneRejectsPublicPublishingMemo(t *testing.T) {
	root := t.TempDir()
	writeHygieneFile(t, root, "README.md", []byte("# Public project\n"))
	writeHygieneFile(t, root, "toolchain/PUBLISH.md", []byte("maintainer secrets and notes"))
	if err := CheckRepoHygieneNative(root); err == nil || !strings.Contains(err.Error(), "owner-only publishing instructions") {
		t.Fatalf("publishing memo accepted: %v", err)
	}
}
