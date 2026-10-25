package switchrelease

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IntegratedCacheStatus describes the exact, source-pinned input set for the
// combined text and graphics build. It never discovers external game files.
type IntegratedCacheStatus struct {
	Game     string
	CacheDir string
	Verified []string
	Missing  []string
}

func (s IntegratedCacheStatus) Ready() bool { return len(s.Missing) == 0 }

type nativeInputRecord struct {
	Archive int    `json:"archive_number"`
	SHA     string `json:"source_file_sha256"`
	Dataset string `json:"source_dataset"`
}

func integratedInputPins(root string) (map[string]map[string]string, error) {
	mapping, err := LoadMapping(root)
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Games map[string]struct {
			NativeRecordsSHA string `json:"native_records_sha256"`
		} `json:"games"`
	}
	contents, err := os.ReadFile(filepath.Join(root, "toolchain", "switch_port", "maps", "manifest.json"))
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(contents, &manifest); err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string)
	for _, game := range games {
		pin := manifest.Games[game].NativeRecordsSHA
		if len(pin) != 64 {
			return nil, fmt.Errorf("%s: missing pinned native record index", game)
		}
		filename := filepath.Join(root, ".local", "switch_text_port", game, "western-native-records.jsonl")
		f, e := os.Open(filename)
		if e != nil {
			return nil, fmt.Errorf("%s: private native record index missing: %w", game, e)
		}
		h := sha256.New()
		scan := bufio.NewScanner(io.TeeReader(f, h))
		scan.Buffer(make([]byte, 128*1024), 16*1024*1024)
		allowed := make(map[int]bool)
		for _, num := range textArchiveNumbers[game] {
			allowed[num] = true
		}
		merged := make(map[string]string)
		for scan.Scan() {
			if len(strings.TrimSpace(scan.Text())) == 0 {
				continue
			}
			var entry nativeInputRecord
			if e = json.Unmarshal(scan.Bytes(), &entry); e != nil {
				break
			}
			if !allowed[entry.Archive] {
				continue
			}
			name := fmt.Sprintf("GxArchivedFile%03d.dat", entry.Archive)
			if entry.Dataset != "" && entry.Dataset != map[string]string{"ms2": "MS2_SWITCH_WESTERN", "finale": "MSF_SWITCH_WESTERN"}[game] {
				e = fmt.Errorf("%s: incorrect native dataset", game)
				break
			}
			if len(entry.SHA) != 64 {
				e = fmt.Errorf("%s: invalid original text SHA for %s", game, name)
				break
			}
			if _, de := hex.DecodeString(entry.SHA); de != nil {
				e = de
				break
			}
			sha := strings.ToLower(entry.SHA)
			if old, ok := merged[name]; ok && old != sha {
				e = fmt.Errorf("%s: contradictory text input SHA for %s", game, name)
				break
			}
			merged[name] = sha
		}
		if e == nil {
			e = scan.Err()
		}
		closeErr := f.Close()
		if e != nil {
			return nil, fmt.Errorf("%s: native input index: %w", game, e)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), pin) {
			return nil, fmt.Errorf("%s: native input index SHA mismatch", game)
		}
		if len(merged) != len(allowed) {
			return nil, fmt.Errorf("%s: native index covers %d/%d required text archives", game, len(merged), len(allowed))
		}
		for name, entry := range mapping.Games[game].Archives {
			if _, duplicate := merged[name]; duplicate {
				return nil, fmt.Errorf("%s: text/graphics overlap %s", game, name)
			}
			merged[name] = strings.ToLower(entry.SHA256)
		}
		out[game] = merged
	}
	return out, nil
}

func inspectIntegratedCacheWithPins(root string, pins map[string]map[string]string) ([]IntegratedCacheStatus, error) {
	result := make([]IntegratedCacheStatus, 0, len(games))
	for _, game := range games {
		status := IntegratedCacheStatus{Game: game, CacheDir: cacheMaster(root, game)}
		if err := safeCacheDirectory(status.CacheDir); err != nil {
			return nil, err
		}
		for name, expected := range pins[game] {
			path := filepath.Join(status.CacheDir, name)
			st, err := os.Lstat(path)
			if errors.Is(err, os.ErrNotExist) {
				status.Missing = append(status.Missing, name)
				continue
			}
			if err != nil {
				return nil, err
			}
			if !st.Mode().IsRegular() {
				return nil, fmt.Errorf("%s: unsafe non-regular cached input %s", game, path)
			}
			got, err := sumFile(path)
			if err != nil {
				return nil, err
			}
			if got != expected {
				return nil, fmt.Errorf("%s: existing private cached original has wrong SHA: %s (not replacing)", game, path)
			}
			status.Verified = append(status.Verified, name)
		}
		sort.Strings(status.Missing)
		sort.Strings(status.Verified)
		result = append(result, status)
	}
	return result, nil
}

// InspectIntegratedCache validates *all* 11 originals (8 text + 3 graphics),
// using the private native-record index and the public graphics mapping.
func InspectIntegratedCache(root string) ([]IntegratedCacheStatus, error) {
	pins, err := integratedInputPins(root)
	if err != nil {
		return nil, err
	}
	return inspectIntegratedCacheWithPins(root, pins)
}

func safeCacheDirectory(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		st, e := os.Lstat(dir)
		if e == nil {
			if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe source cache parent: %s", dir)
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return nil
}

func legacyProjectSourceDirectories(root, game string) []string {
	// Known project-local migration locations only. No scanning drives,
	// home directories or historical workspaces outside this checkout.
	base := filepath.Join(root, ".local", "switch_recon", game, "update")
	return []string{filepath.Join(base, "romfs_full", "masterNX64"), filepath.Join(base, "romfs", "masterNX64"), filepath.Join(base, "masterNX64")}
}

type pendingIntegratedImport struct{ Game, Name, Target, Original, SHA string }

// ensureIntegratedCacheWithPins imports only missing, SHA-matched originals.
// Complete preflight occurs before any write. An imported file is immutable
// and published with an atomic no-clobber hardlink; interruption cannot leave
// partially written GDATs in the cache.
func ensureIntegratedCacheWithPins(root string, pins map[string]map[string]string, selected map[string]string) ([]IntegratedCacheStatus, error) {
	statuses, err := inspectIntegratedCacheWithPins(root, pins)
	if err != nil {
		return nil, err
	}
	pending := []pendingIntegratedImport{}
	missing := []string{}
	for _, status := range statuses {
		candidates := []string{}
		if path := strings.TrimSpace(selected[status.Game]); path != "" {
			candidates = append(candidates, normalizeMaster(path))
		}
		candidates = append(candidates, legacyProjectSourceDirectories(root, status.Game)...)
		for _, name := range status.Missing {
			expected := pins[status.Game][name]
			located := ""
			for _, candidate := range candidates {
				if candidate == "" {
					continue
				}
				file := filepath.Join(candidate, name)
				st, e := os.Lstat(file)
				if os.IsNotExist(e) {
					continue
				}
				if e != nil {
					return nil, e
				}
				if !st.Mode().IsRegular() {
					return nil, fmt.Errorf("%s: unsafe source file %s", status.Game, file)
				}
				got, e := sumFile(file)
				if e != nil {
					return nil, e
				}
				if got != expected {
					return nil, fmt.Errorf("%s: wrong original/update SHA for %s", status.Game, file)
				}
				located = file
				break
			}
			if located == "" {
				missing = append(missing, status.Game+"/"+name)
				continue
			}
			pending = append(pending, pendingIntegratedImport{status.Game, name, filepath.Join(status.CacheDir, name), located, expected})
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return statuses, fmt.Errorf("Switch source cache incomplete: missing %d pinned original GDAT(s): %s. Existing cached originals remain untouched. Select an ALREADY extracted source directory with these files; NSP decryption is not implemented in this importer", len(missing), strings.Join(missing, ", "))
	}
	for _, p := range pending {
		if err = safeCacheDirectory(filepath.Dir(p.Target)); err != nil {
			return nil, err
		}
	}
	// Never silently overwrite a cached file even if another process created
	// it after the initial inspection. publishPinnedPrivateCache rechecks SHA.
	for _, p := range pending {
		dir := filepath.Dir(p.Target)
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		input, e := os.Open(p.Original)
		if e != nil {
			return nil, e
		}
		tmp, e := os.CreateTemp(dir, ".verified-source-*")
		if e != nil {
			input.Close()
			return nil, e
		}
		th := sha256.New()
		_, e = io.Copy(io.MultiWriter(tmp, th), input)
		input.Close()
		if e == nil {
			e = tmp.Sync()
		}
		closeErr := tmp.Close()
		if e == nil {
			e = closeErr
		}
		if e == nil && hex.EncodeToString(th.Sum(nil)) != p.SHA {
			e = fmt.Errorf("%s: source changed during copy", p.Original)
		}
		if e == nil {
			e = publishPinnedPrivateCache(tmp.Name(), p.Target, p.SHA)
		}
		_ = os.Remove(tmp.Name())
		if e != nil {
			return nil, e
		}
	}
	return inspectIntegratedCacheWithPins(root, pins)
}

// EnsureIntegratedSourceCache uses the immutable local cache first and can
// supplement only missing sources from explicitly selected extracted folders
// or the known project-local migration directories. NSP is not extracted.
func EnsureIntegratedSourceCache(root, ms2, finale string) ([]IntegratedCacheStatus, error) {
	pins, err := integratedInputPins(root)
	if err != nil {
		return nil, err
	}
	return ensureIntegratedCacheWithPins(root, pins, map[string]string{"ms2": ms2, "finale": finale})
}
