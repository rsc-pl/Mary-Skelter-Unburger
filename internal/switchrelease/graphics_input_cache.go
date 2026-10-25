package switchrelease

// An automatically reusable graphics image is accepted only when every source
// archive, raster implementation, recipe, font and pinned runtime description
// matches the inputs used to render it. A transport digest alone is insufficient.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type renderedGraphicsCache struct {
	Schema      int    `json:"schema"`
	InputSHA256 string `json:"input_sha256"`
	ZIPSHA256   string `json:"zip_sha256"`
}

type typographyConfig struct {
	Fonts map[string]struct {
		ReferenceSHA      string `json:"reference_sha256"`
		RepoPath          string `json:"repo_path"`
		PreferredBasename string `json:"preferred_basename"`
		EnvOverride       string `json:"env_override"`
	} `json:"fonts"`
}

// This deliberately fails closed when an input is unknown. The first run may
// render again; subsequent unchanged text-only builds reuse the verified image.
func graphicsInputIdentity(root string) (string, error) {
	h := sha256.New()
	add := func(label, path string) error {
		fi, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("graphics input is linked or nonregular: %s", path)
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		fmt.Fprintf(h, "%s\x00%d\x00", label, fi.Size())
		_, e = io.Copy(h, f)
		return e
	}
	dirs := []string{
		"graphics", "toolchain/python-src/scripts/graphics", "toolchain/python-src/toolchain/tools/mskit",
		builderRel,
	}
	var files []string
	for _, d := range dirs {
		e := filepath.WalkDir(filepath.Join(root, d), func(path string, ent os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if ent.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("linked graphics dependency: %s", path)
			}
			if ent.IsDir() {
				return nil
			}
			if !ent.Type().IsRegular() {
				return fmt.Errorf("nonregular graphics dependency: %s", path)
			}
			rel, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			files = append(files, filepath.ToSlash(rel))
			return nil
		})
		if e != nil {
			return "", e
		}
	}
	sort.Strings(files)
	for _, f := range files {
		if e := add(f, filepath.Join(root, f)); e != nil {
			return "", e
		}
	}
	cfgBytes, e := os.ReadFile(filepath.Join(root, "graphics/ui_typography.json"))
	if e != nil {
		return "", e
	}
	var fonts typographyConfig
	if e = json.Unmarshal(cfgBytes, &fonts); e != nil {
		return "", e
	}
	if len(fonts.Fonts) != 5 {
		return "", fmt.Errorf("unexpected number of pinned UI fonts: %d", len(fonts.Fonts))
	}
	names := make([]string, 0, len(fonts.Fonts))
	for n := range fonts.Fonts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		font := fonts.Fonts[n]
		paths := []string{}
		if font.EnvOverride != "" && os.Getenv(font.EnvOverride) != "" {
			paths = append(paths, os.Getenv(font.EnvOverride))
		} else {
			paths = []string{filepath.Join(root, font.RepoPath), filepath.Join(root, ".local/graphics_vendor/fonts", font.PreferredBasename)}
		}
		path := ""
		for _, candidate := range paths {
			if fi, er := os.Lstat(candidate); er == nil && fi.Mode().IsRegular() {
				path = candidate
				break
			}
		}
		if path == "" {
			return "", fmt.Errorf("private graphics font not found: %s", n)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return "", e
		}
		x := sha256.Sum256(b)
		if hex.EncodeToString(x[:]) != strings.ToLower(font.ReferenceSHA) {
			return "", fmt.Errorf("graphics font SHA mismatch: %s", n)
		}
		fmt.Fprintf(h, "font/%s\x00%s\x00", n, font.ReferenceSHA)
	}
	// Rendering depends on the pinned Python/Pillow runtime, too.
	for _, f := range []string{"toolchain/python-runtime/RUNTIME_MANIFEST.json", "toolchain/python-runtime/runtime-manifest.json", "toolchain/python-runtime/linux-amd64/manifest.json", "toolchain/python-runtime/windows-amd64/manifest.json"} {
		p := filepath.Join(root, f)
		if fi, e := os.Lstat(p); e == nil && fi.Mode().IsRegular() {
			if e = add(f, p); e != nil {
				return "", e
			}
		}
	}
	for _, game := range games {
		names := append([]string{}, expectedTextArchiveNames(game)...)
		_ = names // text archives are immaterial to rasterization
		m, e := LoadMapping(root)
		if e != nil {
			return "", e
		}
		archiveNames := make([]string, 0, len(m.Games[game].Archives))
		for n := range m.Games[game].Archives {
			archiveNames = append(archiveNames, n)
		}
		sort.Strings(archiveNames)
		for _, n := range archiveNames {
			path := filepath.Join(cacheMaster(root, game), n)
			if e = add(game+"/"+n, path); e != nil {
				return "", e
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func graphicsCacheManifestPath(root, inputSHA string) string {
	return filepath.Join(root, ".local/switch_release/graphics_cache", "inputs-"+inputSHA+".json")
}

// findReusableGraphics returns empty values if a verified, input-pinned image
// is absent; this is a normal cache miss, not an input-validation bypass.
func findReusableGraphics(root string) (string, string, bool) {
	digest, e := graphicsInputIdentity(root)
	if e != nil {
		return "", "", false
	}
	raw, e := os.ReadFile(graphicsCacheManifestPath(root, digest))
	if e != nil {
		return "", "", false
	}
	var rec renderedGraphicsCache
	if json.Unmarshal(raw, &rec) != nil || rec.Schema != 1 || rec.InputSHA256 != digest || len(rec.ZIPSHA256) != 64 {
		return "", "", false
	}
	path := filepath.Join(root, ".local/switch_release/graphics_cache", "canonical-"+rec.ZIPSHA256+".zip")
	if validatePinnedCanonicalGraphics(path, rec.ZIPSHA256) != nil {
		return "", "", false
	}
	return path, rec.ZIPSHA256, true
}

func rememberRenderedGraphics(root, zipSHA string) error {
	digest, e := graphicsInputIdentity(root)
	if e != nil {
		return e
	}
	path := graphicsCacheManifestPath(root, digest)
	data, e := json.MarshalIndent(renderedGraphicsCache{Schema: 1, InputSHA256: digest, ZIPSHA256: zipSHA}, "", "  ")
	if e != nil {
		return e
	}
	data = append(data, '\n')
	if prior, e := os.ReadFile(path); e == nil {
		if string(prior) == string(data) {
			return nil
		}
		return fmt.Errorf("conflicting immutable graphics input cache: %s", path)
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	if _, e = f.Write(data); e != nil {
		f.Close()
		os.Remove(path)
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		os.Remove(path)
		return e
	}
	return f.Close()
}

// GraphicsReadiness exposes only a friendly read-only result to the maintainer
// interface. A missing cache is never described as a missing original source.
func GraphicsReadiness(root string) (inputsOK, cached bool, reason string) {
	if _, err := graphicsInputIdentity(root); err != nil {
		return false, false, err.Error()
	}
	_, _, ok := findReusableGraphics(root)
	return true, ok, ""
}
