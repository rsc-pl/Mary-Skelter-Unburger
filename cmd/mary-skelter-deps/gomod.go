package main

// Offline Go dependencies use the same on-clone principle as the portable
// Python runtime: the Git checkout contains a compressed, verified archive,
// never the thousands of expanded dependency source files.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type goFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type goBundle struct {
	Schema        int      `json:"schema"`
	ArchiveSHA256 string   `json:"archive_sha256"`
	Files         []goFile `json:"files"`
}

func goSourceDir(root string) string      { return filepath.Join(root, "third_party", "go") }
func goBundleDir(root string) string      { return filepath.Join(root, "toolchain", "go-deps") }
func goBundleZip(root string) string      { return filepath.Join(goBundleDir(root), "modules.zip") }
func goBundleManifest(root string) string { return filepath.Join(goBundleDir(root), "manifest.json") }
func shaString(data []byte) string        { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func goPathSafe(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.HasPrefix(name, "/") && !strings.Contains(name, "\\") && path.Clean(name) == name && !strings.HasPrefix(name, "../") && !strings.Contains(name, "/../") && !strings.Contains(name, ":")
}

func goFiles(root string) ([]goFile, error) {
	dir := goSourceDir(root)
	var out []goFile
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if p == dir {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("linked Go dependency refused: %s", p)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("nonregular Go dependency: %s", p)
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if !goPathSafe(rel) {
			return fmt.Errorf("invalid Go dependency relative path %q", rel)
		}
		bytes, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		out = append(out, goFile{Name: rel, Size: int64(len(bytes)), SHA256: shaString(bytes)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("empty Go dependency source tree")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	for _, required := range []string{"bcn/go.mod", "xsys/go.mod"} {
		found := false
		for _, f := range out {
			if f.Name == required {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("required offline Go module absent: %s", required)
		}
	}
	return out, nil
}
func goReadManifest(root string) (goBundle, error) {
	var m goBundle
	b, e := os.ReadFile(goBundleManifest(root))
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if m.Schema != 1 || len(m.ArchiveSHA256) != 64 || len(m.Files) == 0 {
		return m, errors.New("invalid offline Go bundle manifest")
	}
	last := ""
	for _, f := range m.Files {
		if !goPathSafe(f.Name) || f.Name <= last || f.Size < 0 || len(f.SHA256) != 64 {
			return m, fmt.Errorf("unsafe offline Go manifest entry %q", f.Name)
		}
		last = f.Name
	}
	return m, nil
}
func goVerifyTree(root string, m goBundle) error {
	installed, e := goFiles(root)
	if e != nil {
		return e
	}
	if len(installed) != len(m.Files) {
		return fmt.Errorf("Go dependency inventory mismatch: installed=%d archived=%d", len(installed), len(m.Files))
	}
	for i, f := range installed {
		if f != m.Files[i] {
			return fmt.Errorf("Go dependency differs from verified Git bundle: %s", f.Name)
		}
	}
	return nil
}
func goVerifyArchive(root string, m goBundle) error {
	st, e := os.Lstat(goBundleZip(root))
	if e != nil {
		return e
	}
	if !st.Mode().IsRegular() {
		return errors.New("Go dependencies ZIP is not regular")
	}
	h, e := shaFile(goBundleZip(root))
	if e != nil {
		return e
	}
	if h != m.ArchiveSHA256 {
		return fmt.Errorf("offline Go ZIP integrity failure: got %s", h)
	}
	return nil
}
func goPack(root string) error {
	files, e := goFiles(root)
	if e != nil {
		return e
	}
	dir := goBundleDir(root)
	if e = os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	if _, e = os.Lstat(goBundleManifest(root)); e == nil {
		prior, e := goReadManifest(root)
		if e != nil {
			return e
		}
		if e = goVerifyArchive(root, prior); e != nil {
			return e
		}
		if e = goVerifyTree(root, prior); e != nil {
			return fmt.Errorf("current third_party/go changed since archived: %w; review changes before regenerating bundle", e)
		}
		fmt.Printf("Offline Go archive unchanged (%d verified files).\n", len(prior.Files))
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	if _, e = os.Lstat(goBundleZip(root)); e == nil {
		return errors.New("unindexed Go bundle ZIP already exists; refusing overwrite")
	} else if !os.IsNotExist(e) {
		return e
	}
	tmp, e := os.CreateTemp(dir, ".go-deps-*.zip")
	if e != nil {
		return e
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)
	for _, f := range files {
		fh := &zip.FileHeader{Name: f.Name, Method: zip.Deflate}
		fh.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
		fh.SetMode(0644)
		w, er := zw.CreateHeader(fh)
		if er != nil {
			zw.Close()
			tmp.Close()
			return er
		}
		data, er := os.ReadFile(filepath.Join(goSourceDir(root), filepath.FromSlash(f.Name)))
		if er != nil {
			zw.Close()
			tmp.Close()
			return er
		}
		if int64(len(data)) != f.Size || shaString(data) != f.SHA256 {
			zw.Close()
			tmp.Close()
			return fmt.Errorf("Go source changed during bundling: %s", f.Name)
		}
		if _, er = w.Write(data); er != nil {
			zw.Close()
			tmp.Close()
			return er
		}
	}
	if e = zw.Close(); e != nil {
		tmp.Close()
		return e
	}
	if e = tmp.Close(); e != nil {
		return e
	}
	info, er := os.Stat(tmp.Name())
	if er != nil {
		return er
	}
	if info.Size() > 95*1024*1024 {
		return fmt.Errorf("compressed Go bundle is %d bytes (>95MiB): cannot safely commit as one Git object", info.Size())
	}
	digest, e := shaFile(tmp.Name())
	if e != nil {
		return e
	}
	m := goBundle{Schema: 1, ArchiveSHA256: digest, Files: files}
	buf, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	if e = os.Rename(tmp.Name(), goBundleZip(root)); e != nil {
		return e
	}
	if e = os.WriteFile(goBundleManifest(root), append(buf, '\n'), 0644); e != nil {
		os.Remove(goBundleZip(root))
		return e
	}
	fmt.Printf("Offline Go dependencies packed for Git: %d source files, %s\n", len(files), goBundleZip(root))
	return nil
}
func goRestore(root string) error {
	m, e := goReadManifest(root)
	if os.IsNotExist(e) {
		// Existing maintainer checkout before the first archival pack:
		// preserve the working vendor sources and never require network.
		// A genuinely fresh clone has no expanded source tree and MUST have
		// the committed ZIP+manifest to proceed.
		if _, existing := goFiles(root); existing == nil {
			fmt.Println("Using installed Go source dependencies (offline). Run pack-go before first Git publication.")
			return nil
		}
	}
	if e != nil {
		return fmt.Errorf("missing Git-committed Go bundle; owner must run pack-go before publishing: %w", e)
	}
	if e = goVerifyArchive(root, m); e != nil {
		return e
	}
	dir := goSourceDir(root)
	if _, e = os.Lstat(dir); e == nil {
		return goVerifyTree(root, m)
	} else if !os.IsNotExist(e) {
		return e
	}
	z, e := zip.OpenReader(goBundleZip(root))
	if e != nil {
		return e
	}
	defer z.Close()
	if len(z.File) != len(m.Files) {
		return errors.New("offline Go bundle entry count mismatch")
	}
	parent := filepath.Dir(dir)
	if e = os.MkdirAll(parent, 0755); e != nil {
		return e
	}
	staging, e := os.MkdirTemp(parent, ".go-deps-restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(staging)
	for i, f := range z.File {
		want := m.Files[i]
		if f.Name != want.Name || !goPathSafe(f.Name) || f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() || f.UncompressedSize64 != uint64(want.Size) {
			return fmt.Errorf("unsafe/mismatched offline Go module entry %q", f.Name)
		}
		dest := filepath.Join(staging, filepath.FromSlash(f.Name))
		if e = os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return e
		}
		reader, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if e != nil {
			reader.Close()
			return e
		}
		h := sha256.New()
		count, e := io.Copy(io.MultiWriter(out, h), reader)
		c := out.Close()
		rc := reader.Close()
		if e == nil {
			e = c
		}
		if e == nil {
			e = rc
		}
		if e != nil {
			return e
		}
		if count != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
			return fmt.Errorf("Go dependency SHA mismatch for %s", want.Name)
		}
	}
	if e = goVerifyTreeAt(staging, m); e != nil {
		return e
	}
	if _, e = os.Lstat(dir); e == nil {
		return errors.New("Go dependencies appeared during restore; refusing overwrite")
	}
	if e = os.Rename(staging, dir); e != nil {
		return e
	}
	fmt.Printf("Restored %d pinned Go dependency files from Git archive (offline).\n", len(m.Files))
	return nil
}
func goVerifyTreeAt(dir string, m goBundle) error {
	// Reuse the same inventory verifier against a scratch source tree.
	var files []goFile
	e := filepath.WalkDir(dir, func(p string, d os.DirEntry, er error) error {
		if er != nil {
			return er
		}
		if p == dir || d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("nonregular restored Go file")
		}
		rel, er := filepath.Rel(dir, p)
		if er != nil {
			return er
		}
		b, er := os.ReadFile(p)
		if er != nil {
			return er
		}
		files = append(files, goFile{Name: filepath.ToSlash(rel), Size: int64(len(b)), SHA256: shaString(b)})
		return nil
	})
	if e != nil {
		return e
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	if len(files) != len(m.Files) {
		return errors.New("restored Go dependency file count mismatch")
	}
	for i, f := range files {
		if f != m.Files[i] {
			return fmt.Errorf("restored dependency differs from pinned archive: %s", f.Name)
		}
	}
	return nil
}
