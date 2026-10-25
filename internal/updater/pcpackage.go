package updater

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const pcArchivePrefix = "Mary-Skelter-2-Finale-UNBURGER-PC-v"

// PackagePCRelease publishes a single ZIP beside the Switch ZIPs. The ZIP
// contains the Linux and Windows launchers and the authenticated asset sidecar.
// Existing published archives are not overwritten, including symlinks.
func PackagePCRelease(sourceDir, packageDir, version string) (string, string, error) {
	if err := ValidateReleaseVersion(version); err != nil {
		return "", "", err
	}
	files := []string{"MarySkelterUnburger", "MarySkelterUnburger.exe", "mary-skelter.unburger-assets", "run.sh", "run.bat", "README.txt", "SHA256SUMS.txt"}
	for _, name := range files {
		st, err := os.Lstat(filepath.Join(sourceDir, name))
		if err != nil {
			return "", "", err
		}
		if !st.Mode().IsRegular() {
			return "", "", fmt.Errorf("invalid release member %q", name)
		}
	}
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		return "", "", err
	}
	name := pcArchivePrefix + version + ".zip"
	final := filepath.Join(packageDir, name)
	for _, path := range []string{final, final + ".sha256"} {
		if _, err := os.Lstat(path); err == nil {
			return "", "", fmt.Errorf("release already exists: %s (use a new version or move the old package)", path)
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
	}
	tmp, err := os.CreateTemp(packageDir, ".pc-package-*.zip")
	if err != nil {
		return "", "", err
	}
	temp := tmp.Name()
	defer os.Remove(temp)
	zw := zip.NewWriter(tmp)
	for _, name := range files {
		path := filepath.Join(sourceDir, name)
		st, err := os.Stat(path)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return "", "", err
		}
		hdr, err := zip.FileInfoHeader(st)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return "", "", err
		}
		hdr.Name = name
		hdr.Method = zip.Deflate
		if name == "mary-skelter.unburger-assets" {
			hdr.Method = zip.Store
		}
		if name == "run.sh" || name == "MarySkelterUnburger" {
			hdr.SetMode(0755)
		} else {
			hdr.SetMode(0644)
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return "", "", err
		}
		src, err := os.Open(path)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return "", "", err
		}
		_, err = io.Copy(w, src)
		cerr := src.Close()
		if err == nil {
			err = cerr
		}
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return "", "", err
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	sha, err := fileSHA256(temp)
	if err != nil {
		return "", "", err
	}
	// Hardlink publication never replaces existing files. If publishing the SHA
	// fails, roll back only our ZIP, not unrelated owner files.
	if err := os.Link(temp, final); err != nil {
		return "", "", err
	}
	shatmp, err := os.CreateTemp(packageDir, ".pc-checksum-*")
	if err != nil {
		_ = os.Remove(final)
		return "", "", err
	}
	shaPath := shatmp.Name()
	defer os.Remove(shaPath)
	_, err = io.WriteString(shatmp, sha+"  "+name+"\n")
	if err == nil {
		err = shatmp.Sync()
	}
	if ce := shatmp.Close(); err == nil {
		err = ce
	}
	if err == nil {
		err = os.Link(shaPath, final+".sha256")
	}
	if err != nil {
		_ = os.Remove(final)
		return "", "", fmt.Errorf("publish PC checksum: %w", err)
	}
	return final, strings.ToLower(sha), nil
}
