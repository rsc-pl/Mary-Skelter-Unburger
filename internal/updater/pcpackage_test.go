package updater

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPCZipIncludesLaunchersVersionChecksums(t *testing.T) {
	root := t.TempDir()
	input := filepath.Join(root, "source")
	output := filepath.Join(root, "packages")
	if err := os.MkdirAll(input, 0755); err != nil {
		t.Fatal(err)
	}
	files := []string{"MarySkelterUnburger", "MarySkelterUnburger.exe", "mary-skelter.unburger-assets", "run.sh", "run.bat", "README.txt", "SHA256SUMS.txt"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(input, f), []byte("fixture-"+f), 0644); err != nil {
			t.Fatal(err)
		}
	}
	filename, sha, err := PackagePCRelease(input, output, "0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filename, "-PC-v0.1.0.zip") || len(sha) != 64 {
		t.Fatal(filename, sha)
	}
	z, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	if len(z.File) != len(files) {
		t.Fatalf("%d != %d", len(z.File), len(files))
	}
	saw := map[string]bool{}
	for _, member := range z.File {
		saw[member.Name] = true
		if member.Name == "run.sh" && member.Mode().Perm()&0111 == 0 {
			t.Fatal("launcher not executable")
		}
	}
	for _, f := range files {
		if !saw[f] {
			t.Fatal("missing", f)
		}
	}
	if _, _, err := PackagePCRelease(input, output, "0.1.0"); err == nil {
		t.Fatal("clobber accepted")
	}
	if err := os.WriteFile(filepath.Join(input, "run.sh"), []byte("updated"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PackagePCRelease(input, output, "0.1.0"); err == nil {
		t.Fatal("changed release overwrote existing output")
	}
}
func TestAssetCompressionChoice(t *testing.T) {
	t.Setenv("MARY_SKELTER_ASSETS_ZSTD_LEVEL", "")
	s, e := assetZstdFlags()
	if e != nil || len(s) != 1 || s[0] != "-19" {
		t.Fatal(s, e)
	}
	t.Setenv("MARY_SKELTER_ASSETS_ZSTD_LEVEL", "22")
	s, e = assetZstdFlags()
	if e != nil || len(s) != 2 || s[0] != "--ultra" {
		t.Fatal(s, e)
	}
	t.Setenv("MARY_SKELTER_ASSETS_ZSTD_LEVEL", "23")
	if _, e = assetZstdFlags(); e == nil {
		t.Fatal("invalid level allowed")
	}
}
func TestReleaseVersionValidation(t *testing.T) {
	for _, v := range []string{"0.1.0", "1.2.3-rc1", "2026.10+dev"} {
		if err := ValidateReleaseVersion(v); err != nil {
			t.Fatal(v, err)
		}
	}
	for _, v := range []string{"", "../foo", "a b", "one/two", "one\\two"} {
		if err := ValidateReleaseVersion(v); err == nil {
			t.Fatal("invalid version allowed", v)
		}
	}
}
