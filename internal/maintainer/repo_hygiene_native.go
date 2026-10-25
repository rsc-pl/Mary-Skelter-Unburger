package maintainer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// CheckRepoHygieneNative validates project-authored files and release-facing
// configurations. Upstream code and the pinned runtime are not authored here.
// This read-only check never visits owner-private .local or Git metadata.
func CheckRepoHygieneNative(root string) error {
	var issues []string
	report := func(format string, args ...any) { issues = append(issues, fmt.Sprintf(format, args...)) }
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	// README.md is the project's public documentation, authored and edited by
	// the publisher. Never require empty bytes or any particular digest.
	if info, er := os.Lstat(filepath.Join(root, "README.md")); er == nil {
		if !info.Mode().IsRegular() {
			report("README.md must be a regular file")
		}
	} else if !os.IsNotExist(er) {
		return er
	}
	privateRefs := []string{}
	for _, p := range []string{"review", "manifests", filepath.Join("rebuild", "raw_targets"), "docs"} {
		privateRefs = append(privateRefs, filepath.ToSlash(filepath.Join(".local", p)))
	}
	// Recovery is legitimate safety terminology, not a phase/checkpoint label.
	internalName := regexp.MustCompile(`(?i)\b(?:G[0-9]+R[0-9]+[A-Z0-9_]*|PHASE[0-9]+[A-Z0-9_]*)\b`)
	assetName := regexp.MustCompile(`_G[0-9]`)
	allowedFonts := map[string]bool{}
	fontExtensions := map[string]bool{".otf": true, ".ttf": true, ".woff": true, ".woff2": true}
	var publicFiles []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".local" || d.Name() == ".git" ||
				relIs(root, path, "third_party/go") || relIs(root, path, "toolchain/python-runtime")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		publicFiles = append(publicFiles, path)
		rel, er := filepath.Rel(root, path)
		if er != nil {
			return er
		}
		rel = filepath.ToSlash(rel)
		// No obsolete installer, overlay or historical root scripts may
		// re-enter the public-facing tree during a source update.
		if strings.HasPrefix(rel, "scripts/") || strings.HasPrefix(rel, "install-Mary-Skelter-UNBURGER-") && strings.HasSuffix(rel, "-Cumulative-RECOVERY.sh") ||
			rel == "OVERLAY_SHA256SUMS.txt" || rel == "README__RESEARCH_OVERLAY_NOT_FULL.md" ||
			rel == "MIGRATE_SWITCH_SOURCES.sh" || rel == "third_party.zip" {
			report("private recovery artifact in public root: %s", rel)
		}
		up := strings.ToUpper(d.Name())
		// Audit is a valid name for a production verification component.
		for _, needle := range []string{"FINAL_CONTEXT", "TRANSFER_BUNDLE"} {
			if strings.Contains(up, needle) {
				report("internal workflow file in public tree: %s", rel)
				break
			}
		}
		if rel == "toolchain/PUBLISH.md" {
			report("owner-only publishing instructions must not be tracked: %s", rel)
		}
		if strings.HasSuffix(d.Name(), "_install.sh") || strings.HasPrefix(d.Name(), "install_") {
			report("obsolete installer must not be stored in public project tree: %s", rel)
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if filepath.ToSlash(filepath.Dir(rel)) == "graphics" && ext == ".md" {
			report("graphics documentation must be local-only: %s", rel)
		}
		if ext == ".py" || ext == ".sh" || ext == ".go" {
			raw, er := os.ReadFile(path)
			if er != nil {
				return er
			}
			if utf8.Valid(raw) {
				txt := string(raw)
				for _, needle := range privateRefs {
					if strings.Contains(txt, needle) {
						report("public implementation depends on private project state %s: %s", needle, rel)
					}
				}
				if internalName.MatchString(txt) {
					report("development-only names in public implementation: %s", rel)
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	gfx := filepath.Join(root, "graphics")
	manifest := filepath.Join(gfx, "manifest.json")
	typography := filepath.Join(gfx, "ui_typography.json")
	canon := filepath.Join(gfx, "canon_replacements.json")
	if entries, er := os.ReadDir(gfx); er == nil {
		for _, entry := range entries {
			if entry.Type().IsRegular() && entry.Name() != "manifest.json" && entry.Name() != "ui_typography.json" && entry.Name() != "canon_replacements.json" {
				report("unexpected public graphics file: graphics/%s", entry.Name())
			}
		}
	} else if !os.IsNotExist(er) {
		return er
	}
	_ = canon
	type generic = map[string]json.RawMessage
	readObj := func(path string) (generic, bool, error) {
		raw, er := os.ReadFile(path)
		if os.IsNotExist(er) {
			return nil, false, nil
		}
		if er != nil {
			return nil, false, er
		}
		var obj generic
		if er = json.Unmarshal(raw, &obj); er != nil {
			return nil, false, fmt.Errorf("%s: %w", path, er)
		}
		if obj == nil {
			return nil, false, fmt.Errorf("%s: expected JSON object", path)
		}
		return obj, true, nil
	}
	keysEqual := func(obj generic, names ...string) bool {
		if len(obj) != len(names) {
			return false
		}
		for _, n := range names {
			if _, ok := obj[n]; !ok {
				return false
			}
		}
		return true
	}
	if m, ok, er := readObj(manifest); er != nil {
		return er
	} else if ok {
		if !keysEqual(m, "schema_version", "assets") {
			report("graphics manifest contains non-build top-level metadata")
		}
		var assets []generic
		if er = json.Unmarshal(m["assets"], &assets); er != nil {
			return fmt.Errorf("graphics manifest assets: %w", er)
		}
		forbidden := map[string]bool{"state": true, "capture": true, "notes": true, "runtime_evidence": true, "runtime_capture_evidence": true, "pristine_source_evidence": true, "activation_policy": true, "candidate_visual_status": true, "pending_source_policy": true}
		for _, asset := range assets {
			var id string
			_ = json.Unmarshal(asset["id"], &id)
			bad := []string{}
			for k := range asset {
				if forbidden[k] {
					bad = append(bad, k)
				}
			}
			sort.Strings(bad)
			if len(bad) > 0 {
				report("%s: non-build manifest fields: %v", id, bad)
			}
			if assetName.MatchString(id) {
				report("workflow suffix in asset id: %s", id)
			}
		}
	}
	if t, ok, er := readObj(typography); er != nil {
		return er
	} else if ok {
		if !keysEqual(t, "schema_version", "fonts", "presets") {
			report("typography config contains non-build metadata")
		}
		var fonts map[string]generic
		if er = json.Unmarshal(t["fonts"], &fonts); er != nil {
			return fmt.Errorf("typography fonts: %w", er)
		}
		names := make([]string, 0, len(fonts))
		for n := range fonts {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			cfg := fonts[name]
			if _, has := cfg["note"]; has {
				report("%s: non-build font metadata", name)
			}
			var rel, expect string
			_ = json.Unmarshal(cfg["repo_path"], &rel)
			_ = json.Unmarshal(cfg["reference_sha256"], &expect)
			if rel == "" {
				report("%s: public build font path missing", name)
				continue
			}
			path := filepath.Join(root, filepath.FromSlash(rel))
			resolved, er := filepath.EvalSymlinks(path)
			if er == nil {
				allowedFonts[resolved] = true
			}
			raw, er := os.ReadFile(path)
			if os.IsNotExist(er) {
				report("%s: public build font missing: %s", name, rel)
			} else if er != nil {
				return er
			} else {
				sum := sha256.Sum256(raw)
				if hex.EncodeToString(sum[:]) != expect {
					report("%s: public build font hash mismatch: %s", name, rel)
				}
			}
		}
		for _, path := range publicFiles {
			if !fontExtensions[strings.ToLower(filepath.Ext(path))] {
				continue
			}
			resolved, er := filepath.EvalSymlinks(path)
			if er != nil {
				return er
			}
			if !allowedFonts[resolved] {
				rel, _ := filepath.Rel(root, path)
				report("unused/untracked public font binary: %s", filepath.ToSlash(rel))
			}
		}
		var presets map[string]generic
		if er = json.Unmarshal(t["presets"], &presets); er != nil {
			return fmt.Errorf("typography presets: %w", er)
		}
		names = names[:0]
		for n := range presets {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			if _, has := presets[name]["note"]; has {
				report("%s: non-build preset metadata", name)
			}
		}
	}
	for _, name := range []string{"capture_name_assets.py", "probe_ms2_archive055_residuals.py"} {
		path := filepath.Join(gfx, "..", "scripts", "graphics", name)
		if _, er := os.Lstat(path); er == nil {
			report("research helper must be local-only: scripts/graphics/%s", name)
		} else if !os.IsNotExist(er) {
			return er
		}
	}
	if len(issues) > 0 {
		return fmt.Errorf("repo hygiene FAIL:\n  %s", strings.Join(issues, "\n  "))
	}
	return nil
}

// relIs compares directory identities using the project root, not basenames.
func relIs(root, path, rel string) bool {
	return path == filepath.Join(root, filepath.FromSlash(rel))
}
