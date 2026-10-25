package updater

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type ReleaseBuildResult struct {
	OutDir        string
	LinuxBinary   string
	WindowsBinary string
	Assets        string
	Checksums     string
	Version       string
}

func findModuleRoot(start string) (string, error) {
	p, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if st, e := os.Stat(filepath.Join(p, "go.mod")); e == nil && st.Mode().IsRegular() {
			return p, nil
		}
		next := filepath.Dir(p)
		if next == p {
			return "", fmt.Errorf("could not find go.mod above %s", start)
		}
		p = next
	}
}

func copyFileExact(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	cerr := out.Close()
	if err == nil {
		err = cerr
	}
	return err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func buildUpdaterBinary(root, out, goos, version, a, b, c, perm string) error {
	ld := strings.Join([]string{
		"-s", "-w",
		"-X", "maryskelter-unburger/internal/updater.EmbeddedAssetKeyShareA=" + a,
		"-X", "maryskelter-unburger/internal/updater.EmbeddedAssetKeyShareB=" + b,
		"-X", "maryskelter-unburger/internal/updater.EmbeddedAssetKeyShareC=" + c,
		"-X", "maryskelter-unburger/internal/updater.EmbeddedAssetKeyPerm=" + perm,
		"-X", "maryskelter-unburger/cmd/mary-skelter-updater.embeddedVersion=" + version,
	}, " ")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-trimpath", "-ldflags", ld, "-o", out, "./cmd/mary-skelter-updater")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH=amd64", "CGO_ENABLED=0")
	if raw, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s/amd64 updater build: %w: %s", goos, err, strings.TrimSpace(string(raw)))
	}
	return nil
}

// BuildUpdaterRelease creates the end-user directory. It consumes the private
// key only during the maintainer build, injects obfuscated shares into both
// binaries, copies the single sidecar container, and verifies the Linux binary
// can open that sidecar without a raw key file.
func BuildUpdaterRelease(assetsPath, keyPath, outDir, version string) (ReleaseBuildResult, error) {
	var res ReleaseBuildResult
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return res, err
	}
	if len(key) != 32 {
		return res, fmt.Errorf("%s must contain exactly 32 raw key bytes", keyPath)
	}
	if err = VerifyAssetContainerV3(assetsPath, key); err != nil {
		return res, fmt.Errorf("release container preflight: %w", err)
	}
	authenticated, err := OpenAssetContainerV3(assetsPath, key)
	if err != nil {
		return res, err
	}
	assetVersion := authenticated.Manifest.ReleaseVersion
	_ = authenticated.Close()
	if assetVersion == "" {
		return res, fmt.Errorf("asset container has no release_version: rebuild the container after staging current version.txt")
	}
	if err := ValidateReleaseVersion(assetVersion); err != nil {
		return res, err
	}
	if version != "" && version != assetVersion {
		return res, fmt.Errorf("requested version %s differs from authenticated asset container version %s", version, assetVersion)
	}
	version = assetVersion
	a, b, c, perm, err := GenerateAssetV3KeyShares(key)
	if err != nil {
		return res, err
	}
	root, err := findModuleRoot(".")
	if err != nil {
		return res, err
	}
	if err := ValidateReleaseVersion(version); err != nil {
		return res, err
	}
	absOut, err := filepath.Abs(outDir)
	if err != nil {
		return res, err
	}
	if err = os.MkdirAll(filepath.Dir(absOut), 0o755); err != nil {
		return res, err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(absOut), ".release-build-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(tmp)
	linux := filepath.Join(tmp, "MarySkelterUnburger")
	windows := filepath.Join(tmp, "MarySkelterUnburger.exe")
	sidecar := filepath.Join(tmp, "mary-skelter.unburger-assets")
	if err = buildUpdaterBinary(root, linux, "linux", version, a, b, c, perm); err != nil {
		return res, err
	}
	if err = buildUpdaterBinary(root, windows, "windows", version, a, b, c, perm); err != nil {
		return res, err
	}
	if err = copyFileExact(assetsPath, sidecar, 0o644); err != nil {
		return res, err
	}
	for _, bin := range []string{linux, windows} {
		raw, e := os.ReadFile(bin)
		if e != nil {
			return res, e
		}
		if bytes.Contains(raw, key) {
			return res, fmt.Errorf("raw asset key unexpectedly appears in %s", filepath.Base(bin))
		}
	}
	// Prove sidecar discovery + embedded shares from an otherwise clean cwd.
	verify := exec.Command(linux, "verify-assets-v3")
	verify.Dir = tmp
	verify.Env = append(os.Environ(), "MARY_SKELTER_ASSETS=", "MARY_SKELTER_ASSETS_KEY=")
	if raw, e := verify.CombinedOutput(); e != nil {
		return res, fmt.Errorf("embedded-key Linux release self-test: %w: %s", e, strings.TrimSpace(string(raw)))
	}
	for _, entry := range []struct {
		name, body string
		mode       os.FileMode
	}{
		{"run.sh", `#!/usr/bin/env sh
set -eu
DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec "$DIR/MarySkelterUnburger" "$@"
`, 0o755},
		{"run.bat", "@echo off\r\npushd \"%~dp0\"\r\n\"%~dp0MarySkelterUnburger.exe\" %*\r\nif errorlevel 1 pause\r\npopd\r\n", 0o644},
		{"README.txt", `MARY SKELTER 2 + FINALE UNBURGER - PC

Windows: run run.bat (or MarySkelterUnburger.exe).
Linux: run ./run.sh (or ./MarySkelterUnburger).
Choose an installed game and follow the on-screen instructions.
Keep mary-skelter.unburger-assets next to the updater.
`, 0o644},
	} {
		if err = os.WriteFile(filepath.Join(tmp, entry.name), []byte(entry.body), entry.mode); err != nil {
			return res, err
		}
	}
	files := []string{"MarySkelterUnburger", "MarySkelterUnburger.exe", "mary-skelter.unburger-assets", "run.sh", "run.bat", "README.txt"}
	sort.Strings(files)
	var sums strings.Builder
	for _, name := range files {
		h, e := fileSHA256(filepath.Join(tmp, name))
		if e != nil {
			return res, e
		}
		fmt.Fprintf(&sums, "%s  %s\n", h, name)
	}
	checksums := filepath.Join(tmp, "SHA256SUMS.txt")
	if err = os.WriteFile(checksums, []byte(sums.String()), 0o644); err != nil {
		return res, err
	}
	old := absOut + ".old"
	_ = os.RemoveAll(old)
	if _, e := os.Stat(absOut); e == nil {
		if err = os.Rename(absOut, old); err != nil {
			return res, err
		}
	}
	if err = os.Rename(tmp, absOut); err != nil {
		if _, e := os.Stat(old); e == nil {
			_ = os.Rename(old, absOut)
		}
		return res, err
	}
	_ = os.RemoveAll(old)
	res = ReleaseBuildResult{
		OutDir: absOut, LinuxBinary: filepath.Join(absOut, "MarySkelterUnburger"),
		WindowsBinary: filepath.Join(absOut, "MarySkelterUnburger.exe"),
		Assets:        filepath.Join(absOut, "mary-skelter.unburger-assets"),
		Checksums:     filepath.Join(absOut, "SHA256SUMS.txt"), Version: version,
	}
	return res, nil
}
