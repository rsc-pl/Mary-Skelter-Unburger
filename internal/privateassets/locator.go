// Package privateassets resolves optional owner-local verification material
// by stable purpose keys. The private index is data, not a source-code constant.
package privateassets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const indexRelative = ".local/internal/private_asset_locations.json"

type entry struct {
	Path    string            `json:"path"`
	Kind    string            `json:"kind"`
	SHA256  string            `json:"sha256"`
	Members map[string]string `json:"members"`
}
type manifest struct {
	Schema  int              `json:"schema"`
	Entries map[string]entry `json:"entries"`
}

// Resolve requires a relative owner-private path from an on-disk index.
// Every path component must be real (not a symlink); regular files are
// verified against the index SHA-256 before they can be used.
func Resolve(projectRoot, key string) (string, error) {
	if !filepath.IsAbs(projectRoot) || key == "" || strings.ContainsAny(key, `/\\`) {
		return "", errors.New("absolute project root and a simple asset key required")
	}
	indexPath := filepath.Join(projectRoot, indexRelative)
	if err := checkPath(projectRoot, indexPath, false); err != nil {
		return "", fmt.Errorf("private asset index: %w", err)
	}
	f, err := os.Open(indexPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return "", err
	}
	if len(raw) > 1<<20 {
		return "", errors.New("private asset index too large")
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	if m.Schema != 1 {
		return "", errors.New("unsupported private asset index schema")
	}
	e, ok := m.Entries[key]
	if !ok {
		return "", fmt.Errorf("private asset key not found: %s", key)
	}
	if !filepath.IsLocal(e.Path) || !strings.HasPrefix(filepath.ToSlash(e.Path), ".local/") {
		return "", errors.New("private asset path must stay within .local")
	}
	path := filepath.Join(projectRoot, e.Path)
	if err := checkPath(projectRoot, path, e.Kind == "directory"); err != nil {
		return "", err
	}
	if e.Kind == "directory" {
		if e.SHA256 != "" || len(e.Members) == 0 {
			return "", errors.New("directory requires pinned members")
		}
		for name, expectedHex := range e.Members {
			if !filepath.IsLocal(name) || len(expectedHex) != 64 {
				return "", errors.New("invalid pinned directory member")
			}
			member := filepath.Join(path, name)
			if err := checkPath(projectRoot, member, false); err != nil {
				return "", err
			}
			expected, err := hex.DecodeString(expectedHex)
			if err != nil {
				return "", err
			}
			source, err := os.Open(member)
			if err != nil {
				return "", err
			}
			digest := sha256.New()
			_, copyErr := io.Copy(digest, source)
			closeErr := source.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
			if !equalDigest(digest.Sum(nil), expected) {
				return "", fmt.Errorf("private directory member digest differs: %s", key)
			}
		}
		return path, nil
	}
	if e.Kind != "file" || len(e.SHA256) != 64 {
		return "", errors.New("invalid private asset file declaration")
	}
	expected, err := hex.DecodeString(e.SHA256)
	if err != nil {
		return "", err
	}
	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer source.Close()
	h := sha256.New()
	if _, err := io.Copy(h, source); err != nil {
		return "", err
	}
	if !equalDigest(h.Sum(nil), expected) {
		return "", fmt.Errorf("private asset digest differs: %s", key)
	}
	return path, nil
}

func equalDigest(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func checkPath(root, path string, directory bool) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return errors.New("asset escapes project root")
	}
	at := root
	parts := strings.Split(rel, string(filepath.Separator))
	for i, part := range parts {
		at = filepath.Join(at, part)
		info, err := os.Lstat(at)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink refused: %s", at)
		}
		wantDir := i != len(parts)-1 || directory
		if wantDir && !info.IsDir() {
			return errors.New("expected private directory")
		}
		if !wantDir && !info.Mode().IsRegular() {
			return errors.New("expected regular private file")
		}
	}
	return nil
}
