// switch-source-migrate is a one-off, explicit-source private cache importer.
// It never searches for a user-specific workspace, modifies ROMs, or deletes sources.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type manifest struct {
	Games map[string]struct {
		NativeRecordsSHA string `json:"native_records_sha256"`
	} `json:"games"`
}
type graphicsMapping struct {
	Games map[string]struct {
		Archives map[string]struct {
			SHA string `json:"sha256"`
		} `json:"archives"`
	} `json:"games"`
}
type record struct {
	Archive int    `json:"archive_number"`
	SHA     string `json:"source_file_sha256"`
	Dataset string `json:"source_dataset"`
}
type source struct {
	game, name, sha, path, target string
	size                          int64
	exists                        bool
}

var nums = map[string][]int{"ms2": {71, 72, 73}, "finale": {1, 71, 72, 73, 77}}

func sumFile(p string) (string, error) {
	f, e := os.Open(p)
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
func readJSON(p string, v any) error {
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, v)
}
func hashesFromNative(p, expectedIndexSHA, game string) (map[string]string, error) {
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	h := sha256.New()
	scan := bufio.NewScanner(io.TeeReader(f, h))
	scan.Buffer(make([]byte, 128*1024), 16*1024*1024)
	out := map[string]string{}
	counts := map[string]int{}
	for scan.Scan() {
		if len(strings.TrimSpace(scan.Text())) == 0 {
			continue
		}
		var r record
		if e := json.Unmarshal(scan.Bytes(), &r); e != nil {
			return nil, fmt.Errorf("%s: invalid JSONL record: %w", game, e)
		}
		// The pinned Western index is intentionally a full corpus, not a
		// translation-only subset. Records from other GDAT archives (for
		// example MS2 052) remain legitimate but must never become import
		// requirements. The SHA of the COMPLETE index is still checked below.
		wanted := false
		for _, num := range nums[game] {
			if r.Archive == num {
				wanted = true
				break
			}
		}
		if !wanted {
			continue
		}
		name := fmt.Sprintf("GxArchivedFile%03d.dat", r.Archive)
		if len(r.SHA) != 64 || !validHex(r.SHA) {
			return nil, fmt.Errorf("%s: invalid active source archive identity %s", game, name)
		}
		if r.Dataset != "" && r.Dataset != map[string]string{"ms2": "MS2_SWITCH_WESTERN", "finale": "MSF_SWITCH_WESTERN"}[game] {
			return nil, fmt.Errorf("%s: unexpected dataset %s", game, r.Dataset)
		}
		if old, ok := out[name]; ok && old != r.SHA {
			return nil, fmt.Errorf("%s: contradictory source SHA for %s", game, name)
		}
		out[name] = strings.ToLower(r.SHA)
		counts[name]++
	}
	if e := scan.Err(); e != nil {
		return nil, e
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(expectedIndexSHA) {
		return nil, fmt.Errorf("%s: native index SHA mismatch; refuse import", game)
	}
	if len(out) != len(nums[game]) {
		return nil, fmt.Errorf("%s: expected %d text source identities; got %d", game, len(nums[game]), len(out))
	}
	for name, num := range counts {
		if num == 0 {
			return nil, fmt.Errorf("%s: empty index %s", game, name)
		}
	}
	return out, nil
}
func validHex(s string) bool { _, e := hex.DecodeString(s); return e == nil }
func regularFile(p string) (os.FileInfo, error) {
	st, e := os.Lstat(p)
	if e != nil {
		return nil, e
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %s", p)
	}
	return st, nil
}
func ensureNoSymlinkDir(p string) error {
	abs, e := filepath.Abs(p)
	if e != nil {
		return e
	}
	v := abs
	for {
		st, e := os.Lstat(v)
		if e == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink in private destination path: %s", v)
			}
			if !st.IsDir() {
				return fmt.Errorf("not a directory: %s", v)
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		next := filepath.Dir(v)
		if next == v {
			break
		}
		v = next
	}
	return nil
}
func collect(project, ms2, finale string) ([]source, error) {
	mappingPath, err := privateassets.Resolve(project, "switch-graphics-source-map")
	if err != nil {
		return nil, err
	}
	mapPath := filepath.Join(project, "toolchain", "switch_port", "maps", "manifest.json")
	var m graphicsMapping
	if e := readJSON(mappingPath, &m); e != nil {
		return nil, e
	}
	var manifest manifest
	if e := readJSON(mapPath, &manifest); e != nil {
		return nil, e
	}
	srcRoots := map[string]string{"ms2": ms2, "finale": finale}
	var result []source
	for _, game := range []string{"ms2", "finale"} {
		index := filepath.Join(project, ".local", "switch_text_port", game, "western-native-records.jsonl")
		expected := manifest.Games[game].NativeRecordsSHA
		if len(expected) != 64 {
			return nil, fmt.Errorf("%s: missing index source pin", game)
		}
		th, e := hashesFromNative(index, expected, game)
		if e != nil {
			return nil, e
		}
		all := th
		for name, x := range m.Games[game].Archives {
			if old, ok := all[name]; ok && old != x.SHA {
				return nil, fmt.Errorf("%s/%s: graphics/text source hash collision", game, name)
			}
			all[name] = strings.ToLower(x.SHA)
		}
		expectedCount := len(nums[game]) + len(m.Games[game].Archives)
		if len(all) != expectedCount {
			return nil, fmt.Errorf("%s: expected %d inputs, got %d", game, expectedCount, len(all))
		}
		master := srcRoots[game]
		if master == "" {
			return nil, fmt.Errorf("missing explicit source for %s", game)
		}
		for name, sha := range all {
			if !strings.HasPrefix(name, "GxArchivedFile") || !strings.HasSuffix(name, ".dat") || len(sha) != 64 || !validHex(sha) {
				return nil, errors.New("invalid source identity")
			}
			src := filepath.Join(master, name)
			st, e := regularFile(src)
			if e != nil {
				return nil, fmt.Errorf("%s/%s: %w", game, name, e)
			}
			dest := filepath.Join(project, ".local", "switch_sources", game, "latest_update", "masterNX64", name)
			result = append(result, source{game: game, name: name, sha: sha, path: src, target: dest, size: st.Size()})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].game != result[j].game {
			return result[i].game < result[j].game
		}
		return result[i].name < result[j].name
	})
	if len(result) != 11 {
		return nil, fmt.Errorf("expected exactly 11 original GDAT files; got %d", len(result))
	}
	return result, nil
}
func migrate(project, ms2, finale string, stdout io.Writer) error {
	items, e := collect(project, ms2, finale)
	if e != nil {
		return e
	}
	// First gate: all source AND all existing destination bytes must match
	// pinned hashes before writing even one file.
	for i := range items {
		item := &items[i]
		if e = ensureNoSymlinkDir(filepath.Dir(item.target)); e != nil {
			return e
		}
		sig, e := os.Open(item.path)
		if e != nil {
			return e
		}
		var buf [4]byte
		_, e = io.ReadFull(sig, buf[:])
		sig.Close()
		if e != nil || string(buf[:]) != "GDAT" {
			return fmt.Errorf("%s/%s: source is not GDAT", item.game, item.name)
		}
		hash, e := sumFile(item.path)
		if e != nil {
			return e
		}
		if hash != item.sha {
			return fmt.Errorf("%s/%s: input differs from pinned Western latest update (SHA mismatch)", item.game, item.name)
		}
		st, e := regularFile(item.target)
		if e == nil {
			old, e2 := sumFile(item.target)
			if e2 != nil {
				return e2
			}
			if old != item.sha {
				return fmt.Errorf("%s/%s: existing cache differs; refusing overwrite", item.game, item.name)
			}
			item.exists = true
			_ = st
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	for _, item := range items {
		if item.exists {
			fmt.Fprintf(stdout, "KEEP  %s/%s (%d bytes)\n", item.game, item.name, item.size)
			continue
		}
		dir := filepath.Dir(item.target)
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		f, e := os.Open(item.path)
		if e != nil {
			return e
		}
		temp, e := os.CreateTemp(dir, ".verified-import-*")
		if e != nil {
			f.Close()
			return e
		}
		_, e = io.Copy(temp, f)
		f.Close()
		if e == nil {
			e = temp.Sync()
		}
		if ce := temp.Close(); e == nil {
			e = ce
		}
		if e != nil {
			os.Remove(temp.Name())
			return e
		}
		hash, e := sumFile(temp.Name())
		if e != nil || hash != item.sha {
			os.Remove(temp.Name())
			return fmt.Errorf("%s/%s: source changed during import", item.game, item.name)
		}
		// Atomic no-clobber publish on the destination filesystem. A hardlink
		// is made only from our freshly copied private temporary file, never
		// from the original external input; concurrent writers cannot replace
		// an existing cached archive.
		if e = os.Link(temp.Name(), item.target); e != nil {
			os.Remove(temp.Name())
			return fmt.Errorf("cache already exists or no-clobber publish failed for %s: %w", item.target, e)
		}
		if e = os.Remove(temp.Name()); e != nil {
			return e
		}
		fmt.Fprintf(stdout, "COPY  %s/%s (%d bytes)\n", item.game, item.name, item.size)
	}
	fmt.Fprintln(stdout, "PASS: 11/11 pinned pristine Switch sources cached; originals unchanged; zero external path references stored")
	return nil
}
func main() {
	var project, ms2, finale string
	flag.StringVar(&project, "project-root", "", "main project checkout containing canonical recipes")
	flag.StringVar(&ms2, "ms2-master", "", "explicitly selected extracted latest-update MS2 masterNX64")
	flag.StringVar(&finale, "finale-master", "", "explicitly selected extracted latest-update Finale masterNX64")
	flag.Parse()
	if project == "" || ms2 == "" || finale == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: switch-source-migrate --project-root DIR --ms2-master DIR --finale-master DIR")
		os.Exit(2)
	}
	if err := migrate(project, ms2, finale, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "IMPORT FAILED:", err)
		os.Exit(1)
	}
}
