// mary-skelter-deps makes a Git clone self-contained without checking in
// thousands of expanded Python files or compiler-generated Go binaries.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"maryskelter-unburger/internal/graphicsruntime"
)

const partSize int64 = 80 * 1024 * 1024

type piece struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type bundle struct {
	Schema        int     `json:"schema"`
	Platform      string  `json:"platform"`
	ArchiveSHA256 string  `json:"archive_sha256"`
	Pieces        []piece `json:"pieces"`
}

func shaFile(name string) (string, error) {
	f, e := os.Open(name)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func allowed(p string) bool { return p == "linux-amd64" || p == "windows-amd64" }
func repoRoot() (string, error) {
	wd, e := os.Getwd()
	if e != nil {
		return "", e
	}
	for {
		if b, e := os.ReadFile(filepath.Join(wd, "go.mod")); e == nil && strings.Contains(string(b), "module maryskelter-unburger") {
			return wd, nil
		}
		d := filepath.Dir(wd)
		if d == wd {
			return "", errors.New("run from within the Mary Skelter source checkout")
		}
		wd = d
	}
}
func runtimeDir(root, platform string) string {
	return filepath.Join(root, "toolchain", "python-runtime", platform)
}
func bundleDir(root string) string {
	return filepath.Join(root, "toolchain", "python-runtime", "bundles")
}
func verifyRuntime(root, platform string) error {
	parts := strings.Split(platform, "-")
	if len(parts) != 2 || !allowed(platform) {
		return fmt.Errorf("unsupported platform %q", platform)
	}
	if e := graphicsruntime.VerifyPublishedRuntimeManifest(root, parts[0], parts[1]); e != nil {
		return e
	}
	_, e := graphicsruntime.ResolvePlatform(root, parts[0], parts[1])
	return e
}
func filePaths(dir string) ([]string, error) {
	var paths []string
	e := filepath.WalkDir(dir, func(name string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == dir {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in frozen Python: %s", name)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("nonregular frozen Python entry: %s", name)
		}
		rel, e := filepath.Rel(dir, name)
		if e != nil {
			return e
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(paths)
	return paths, e
}

type fontSpec struct {
	Fonts map[string]struct {
		Path string `json:"repo_path"`
		SHA  string `json:"reference_sha256"`
	} `json:"fonts"`
}

func verifyFonts(root string) error {
	raw, err := os.ReadFile(filepath.Join(root, "graphics", "ui_typography.json"))
	if err != nil {
		return err
	}
	var spec fontSpec
	if err = json.Unmarshal(raw, &spec); err != nil {
		return err
	}
	if len(spec.Fonts) != 5 {
		return fmt.Errorf("expected five pinned typography fonts, found %d", len(spec.Fonts))
	}
	for id, font := range spec.Fonts {
		if !strings.HasPrefix(font.Path, "third_party/fonts/") || !safeRel(font.Path) {
			return fmt.Errorf("unsafe font location %s", id)
		}
		file := filepath.Join(root, filepath.FromSlash(font.Path))
		fi, e := os.Lstat(file)
		if e != nil {
			return fmt.Errorf("font %s unavailable at %s: %w", id, font.Path, e)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("font %s is not a regular file", id)
		}
		got, e := shaFile(file)
		if e != nil {
			return e
		}
		if got != font.SHA {
			return fmt.Errorf("font %s SHA mismatch at %s", id, font.Path)
		}
	}
	fmt.Println("Required typography fonts: 5/5 SHA verified")
	return nil
}
func verifyParts(root, platform string) error {
	raw, e := os.ReadFile(filepath.Join(bundleDir(root), platform+".json"))
	if e != nil {
		return e
	}
	var b bundle
	if e = json.Unmarshal(raw, &b); e != nil {
		return e
	}
	if b.Schema != 1 || b.Platform != platform || len(b.Pieces) == 0 {
		return errors.New("invalid bundle index")
	}
	for i, p := range b.Pieces {
		if p.File != fmt.Sprintf("%s.part%03d", platform, i+1) || p.Size < 1 || p.Size > partSize {
			return errors.New("invalid bundle part name/size")
		}
		full := filepath.Join(bundleDir(root), p.File)
		st, x := os.Lstat(full)
		if x != nil {
			return x
		}
		if !st.Mode().IsRegular() || st.Size() != p.Size {
			return fmt.Errorf("invalid bundle part %s", p.File)
		}
		got, x := shaFile(full)
		if x != nil {
			return x
		}
		if got != p.SHA256 {
			return fmt.Errorf("bundle digest mismatch: %s", p.File)
		}
	}
	return nil
}
func pack(root, platform string) error {
	if !allowed(platform) {
		return fmt.Errorf("unsupported platform %q", platform)
	}
	fmt.Printf("Verifying complete SHA-pinned %s Python before bundling...\n", platform)
	if e := verifyRuntime(root, platform); e != nil {
		return e
	}
	outDir := bundleDir(root)
	if _, er := os.Lstat(filepath.Join(outDir, platform+".json")); er == nil {
		if er = verifyParts(root, platform); er != nil {
			return er
		}
		fmt.Printf("Existing %s frozen runtime bundle verified; no re-pack required.\n", platform)
		return nil
	} else if !os.IsNotExist(er) {
		return er
	}
	if e := os.MkdirAll(outDir, 0755); e != nil {
		return e
	}
	temp, e := os.CreateTemp(outDir, ".bundle-*.zip")
	if e != nil {
		return e
	}
	tmp := temp.Name()
	defer os.Remove(tmp)
	zw := zip.NewWriter(temp)
	dir := runtimeDir(root, platform)
	names, e := filePaths(dir)
	if e != nil {
		zw.Close()
		temp.Close()
		return e
	}
	for _, rel := range names {
		src, e := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
		if e != nil {
			zw.Close()
			temp.Close()
			return e
		}
		info, e := src.Stat()
		if e != nil {
			src.Close()
			zw.Close()
			temp.Close()
			return e
		}
		h, _ := zip.FileInfoHeader(info)
		h.Name = rel
		h.Method = zip.Deflate
		dst, e := zw.CreateHeader(h)
		if e == nil {
			_, e = io.Copy(dst, src)
		}
		src.Close()
		if e != nil {
			zw.Close()
			temp.Close()
			return e
		}
	}
	if e = zw.Close(); e != nil {
		temp.Close()
		return e
	}
	if e = temp.Close(); e != nil {
		return e
	}
	archSHA, e := shaFile(tmp)
	if e != nil {
		return e
	}
	in, e := os.Open(tmp)
	if e != nil {
		return e
	}
	defer in.Close()
	info, e := in.Stat()
	if e != nil {
		return e
	}
	b := bundle{Schema: 1, Platform: platform, ArchiveSHA256: archSHA}
	// Precheck all destinations before publishing any chunk.
	n := (info.Size() + partSize - 1) / partSize
	for i := int64(0); i < n; i++ {
		fn := fmt.Sprintf("%s.part%03d", platform, i+1)
		if _, e := os.Lstat(filepath.Join(outDir, fn)); e == nil {
			return fmt.Errorf("bundle chunk already exists; refusing overwrite: %s", fn)
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	if _, e := os.Lstat(filepath.Join(outDir, platform+".json")); e == nil {
		return errors.New("bundle already published; refusing overwrite")
	}
	var created []string
	defer func() {
		if e != nil {
			for _, f := range created {
				os.Remove(f)
			}
		}
	}()
	for i := int64(0); i < n; i++ {
		fn := fmt.Sprintf("%s.part%03d", platform, i+1)
		dstPath := filepath.Join(outDir, fn)
		dst, x := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if x != nil {
			e = x
			return e
		}
		created = append(created, dstPath)
		h := sha256.New()
		count, x := io.CopyN(io.MultiWriter(dst, h), in, partSize)
		if x != nil && x != io.EOF {
			dst.Close()
			e = x
			return e
		}
		if x = dst.Close(); x != nil {
			e = x
			return e
		}
		b.Pieces = append(b.Pieces, piece{fn, hex.EncodeToString(h.Sum(nil)), count})
	}
	raw, e := json.MarshalIndent(b, "", "  ")
	if e != nil {
		return e
	}
	raw = append(raw, '\n')
	e = os.WriteFile(filepath.Join(outDir, platform+".json"), raw, 0644)
	if e == nil {
		fmt.Printf("Committed-ready Python bundle: %d parts for %s (%d verified files)\n", len(b.Pieces), platform, len(names))
	}
	return e
}
func safeRel(name string) bool {
	return name != "" && !strings.HasPrefix(name, "/") && !strings.Contains(name, "\\") && path.Clean(name) == name && name != "." && name != ".." && !strings.HasPrefix(name, "../") && !strings.Contains(name, "/../")
}
func restore(root, platform string) error {
	if !allowed(platform) {
		return fmt.Errorf("unsupported platform %q", platform)
	}
	out := runtimeDir(root, platform)
	if _, e := os.Lstat(out); e == nil {
		return verifyRuntime(root, platform)
	} else if !os.IsNotExist(e) {
		return e
	}
	manifestFile := filepath.Join(bundleDir(root), platform+".json")
	raw, e := os.ReadFile(manifestFile)
	if e != nil {
		return fmt.Errorf("missing committed Python bundle %s: %w (run 'go run ./cmd/mary-skelter-deps pack --all' on the original working installation, then commit bundles/)", platform, e)
	}
	var m bundle
	if e = json.Unmarshal(raw, &m); e != nil {
		return e
	}
	if m.Schema != 1 || m.Platform != platform || len(m.Pieces) < 1 {
		return errors.New("invalid frozen runtime bundle manifest")
	}
	var readers []io.Reader
	var opened []*os.File
	defer func() {
		for _, f := range opened {
			f.Close()
		}
	}()
	var sum int64
	for i, p := range m.Pieces {
		if p.File != fmt.Sprintf("%s.part%03d", platform, i+1) || p.Size < 1 || p.Size > partSize {
			return errors.New("invalid bundle part descriptor")
		}
		name := filepath.Join(bundleDir(root), p.File)
		st, x := os.Lstat(name)
		if x != nil {
			return x
		}
		if !st.Mode().IsRegular() || st.Size() != p.Size {
			return fmt.Errorf("invalid Python chunk %s", name)
		}
		got, x := shaFile(name)
		if x != nil {
			return x
		}
		if got != p.SHA256 {
			return fmt.Errorf("Python bundle chunk digest mismatch %s", name)
		}
		f, x := os.Open(name)
		if x != nil {
			return x
		}
		opened = append(opened, f)
		readers = append(readers, f)
		sum += p.Size
	}
	temp, e := os.CreateTemp(bundleDir(root), ".verify-*.zip")
	if e != nil {
		return e
	}
	tmp := temp.Name()
	defer os.Remove(tmp)
	hash := sha256.New()
	_, e = io.Copy(io.MultiWriter(temp, hash), io.MultiReader(readers...))
	if x := temp.Close(); e == nil {
		e = x
	}
	if e != nil {
		return e
	}
	if hex.EncodeToString(hash.Sum(nil)) != m.ArchiveSHA256 {
		return errors.New("frozen Python bundle archive checksum mismatch")
	}
	z, e := zip.OpenReader(tmp)
	if e != nil {
		return e
	}
	defer z.Close()
	staging, e := os.MkdirTemp(filepath.Dir(out), ".runtime-stage-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(staging)
	seen := map[string]bool{}
	for _, f := range z.File {
		if !safeRel(f.Name) || seen[f.Name] || f.Mode()&os.ModeSymlink != 0 || f.FileInfo().IsDir() {
			return fmt.Errorf("unsafe Python bundle entry %q", f.Name)
		}
		seen[f.Name] = true
		dest := filepath.Join(staging, filepath.FromSlash(f.Name))
		if e := os.MkdirAll(filepath.Dir(dest), 0755); e != nil {
			return e
		}
		rd, e := f.Open()
		if e != nil {
			return e
		}
		wr, e := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, f.Mode().Perm())
		if e != nil {
			rd.Close()
			return e
		}
		_, e = io.Copy(wr, rd)
		a := wr.Close()
		b := rd.Close()
		if e == nil {
			e = a
		}
		if e == nil {
			e = b
		}
		if e != nil {
			return e
		}
	}
	// Verify staged runtime by temporarily using its final name, only when absent.
	if _, e := os.Lstat(out); e == nil {
		return errors.New("runtime appeared during restore; refusing to overwrite")
	}
	if e = os.Rename(staging, out); e != nil {
		return e
	}
	if e = verifyRuntime(root, platform); e != nil {
		os.RemoveAll(out)
		return fmt.Errorf("installed runtime verification failed: %w", e)
	}
	fmt.Printf("[bootstrap] Restored verified frozen Python: %s\n", platform)
	_ = sum
	return nil
}
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/mary-skelter-deps <pack|restore> [--all|--platform name]")
		os.Exit(2)
	}
	op := os.Args[1]
	fs := flag.NewFlagSet(op, flag.ExitOnError)
	platform := fs.String("platform", runtime.GOOS+"-"+runtime.GOARCH, "target runtime platform")
	all := fs.Bool("all", false, "package both Linux and Windows runtimes")
	fs.Parse(os.Args[2:])
	root, e := repoRoot()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	switch op {
	case "pack-go":
		e = goPack(root)
	case "restore-go":
		e = goRestore(root)
	case "verify-go":
		var m goBundle
		m, e = goReadManifest(root)
		if e == nil {
			e = goVerifyArchive(root, m)
		}
		if e == nil {
			e = goVerifyTree(root, m)
		}
	case "pack":
		if *all {
			for _, p := range []string{"linux-amd64", "windows-amd64"} {
				if e = pack(root, p); e != nil {
					break
				}
			}
		} else {
			e = pack(root, *platform)
		}
	case "fonts":
		e = verifyFonts(root)
	case "verify-bundles":
		for _, p := range []string{"linux-amd64", "windows-amd64"} {
			if e = verifyParts(root, p); e != nil {
				break
			}
		}
	case "restore":
		if *all {
			e = errors.New("restore requires a single platform")
		} else {
			e = restore(root, *platform)
		}
	default:
		e = fmt.Errorf("unsupported operation %q", op)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "ERROR:", e)
		os.Exit(1)
	}
}
