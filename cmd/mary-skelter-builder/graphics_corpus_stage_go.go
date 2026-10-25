package main

// Batch source-material staging for the existing 31-family canonical graphics
// manifest. This is a source integrity gate, NOT a font renderer or an in-game
// QA substitute. Only SHA-pinned original textures are ever staged.

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type graphicsSourcePin struct {
	SHA    string `json:"tid_sha256"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Format string `json:"format"`
	GDAT   int    `json:"gdat_index"`
	Inner  string `json:"inner_name"`
}
type graphicsSourceFamily struct {
	ID       string              `json:"id"`
	Archive  int                 `json:"archive"`
	GDAT     int                 `json:"gdat_index"`
	Inner    string              `json:"cl3_inner_name"`
	Variants []graphicsSourcePin `json:"source_variants"`
	Targets  []graphicsSourcePin `json:"targets"`
}
type graphicsSourceManifest struct {
	Assets []graphicsSourceFamily `json:"assets"`
}
type graphicsSourceTarget struct {
	Family  string `json:"family"`
	Archive int    `json:"archive"`
	GDAT    int    `json:"gdat_index"`
	Inner   string `json:"inner_name"`
	SHA     string `json:"sha256"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Format  string `json:"format"`
	Output  string `json:"output"`
}
type graphicsStageReport struct {
	Status             string                 `json:"status"`
	CaptureSHA         string                 `json:"capture_sha256"`
	CanonicalFamilies  int                    `json:"canonical_families"`
	SourceTargets      int                    `json:"verified_sources"`
	FamiliesCovered    []string               `json:"families_covered"`
	FamiliesNotCovered []string               `json:"families_not_covered"`
	RuntimeQA          string                 `json:"runtime_qa"`
	Entries            []graphicsSourceTarget `json:"entries"`
}

var graphicsG02Filename = regexp.MustCompile(`^([A-Za-z0-9_]+)__a([0-9]{3})__g([0-9]{5})__(.+\.tid)$`)

func graphicsKey(archive, gdat int, inner string) string {
	return fmt.Sprintf("%d/%d/%s", archive, gdat, inner)
}
func graphicsCaptureAlias(family string) string {
	switch family {
	case "MS2_JOB_CLASS_24PX", "MS2_JOB_CLASS_MIDDLE", "MS2_DLC_JOB_LABELS":
		return "MS2_JOB_CLASS_LABELS"
	default:
		return family
	}
}
func graphicsStagePins(root string) (map[string]struct {
	family string
	pin    graphicsSourcePin
}, []string, error) {
	b, e := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if e != nil {
		return nil, nil, e
	}
	var m graphicsSourceManifest
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, nil, e
	}
	if len(m.Assets) != 31 {
		return nil, nil, fmt.Errorf("graphics manifest family count drift: %d", len(m.Assets))
	}
	pins := make(map[string]struct {
		family string
		pin    graphicsSourcePin
	})
	families := make([]string, 0, len(m.Assets))
	for _, f := range m.Assets {
		families = append(families, f.ID)
		for _, p := range append(append([]graphicsSourcePin{}, f.Variants...), f.Targets...) {
			if p.SHA == "" {
				continue
			}
			gd, inner := f.GDAT, f.Inner
			if p.GDAT != 0 {
				gd = p.GDAT
			}
			if p.Inner != "" {
				inner = p.Inner
			}
			// 0 is a valid source index in Finale, so only override if the
			// variant's explicit inner name signals an independent target.
			if p.Inner != "" {
				gd = p.GDAT
			}
			if inner == "" {
				return nil, nil, fmt.Errorf("missing inner for %s", f.ID)
			}
			k := graphicsKey(f.Archive, gd, inner)
			if old, exists := pins[k]; exists {
				return nil, nil, fmt.Errorf("duplicate pin %s: %s / %s", k, old.family, f.ID)
			}
			pins[k] = struct {
				family string
				pin    graphicsSourcePin
			}{f.ID, p}
		}
	}
	return pins, families, nil
}
func graphicsSourceArchiveStage(root, capture, extraDir, dest string) (graphicsStageReport, error) {
	r := graphicsStageReport{Status: "NOT_PUBLISHED", RuntimeQA: "DEFERRED_UNTIL_FINAL_CONSOLIDATED_GAME_TEST"}
	pins, families, err := graphicsStagePins(root)
	if err != nil {
		return r, err
	}
	r.CanonicalFamilies = len(families)
	if _, err = os.Lstat(dest); err == nil {
		return r, fmt.Errorf("destination exists (no-clobber): %s", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	pdir := filepath.Dir(dest)
	if err = os.MkdirAll(pdir, 0755); err != nil {
		return r, err
	}
	stage, err := os.MkdirTemp(pdir, ".graphics-source-stage-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(stage)
	raw, err := os.Open(capture)
	if err != nil {
		return r, err
	}
	defer raw.Close()
	digest := sha256.New()
	if _, err = io.Copy(digest, raw); err != nil {
		return r, err
	}
	r.CaptureSHA = hex.EncodeToString(digest.Sum(nil))
	// G02 is a private legacy .tar.zst; the external zstd binary is only a
	// decompressor, never Python/Pillow or a production renderer.
	cmd := exec.Command("zstd", "-dc", "--", capture)
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return r, err
	}
	var errorText strings.Builder
	cmd.Stderr = &errorText
	if err = cmd.Start(); err != nil {
		return r, err
	}
	reader := tar.NewReader(pipe)
	seen := make(map[string]bool)
	covered := make(map[string]bool)
	var readErr error
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			readErr = e
			break
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			readErr = fmt.Errorf("unsafe tar entry type: %s", h.Name)
			break
		}
		name := filepath.Base(h.Name)
		if !strings.HasSuffix(strings.ToLower(name), ".tid") {
			continue
		}
		m := graphicsG02Filename.FindStringSubmatch(name)
		if m == nil {
			readErr = fmt.Errorf("unrecognized source filename %q", name)
			break
		}
		a, e1 := strconv.Atoi(m[2])
		g, e2 := strconv.Atoi(m[3])
		if e1 != nil || e2 != nil {
			readErr = fmt.Errorf("invalid source locator %s", name)
			break
		}
		key := graphicsKey(a, g, m[4])
		pin, ok := pins[key]
		if !ok {
			readErr = fmt.Errorf("capture contains unpinned source: %s", key)
			break
		}
		if graphicsCaptureAlias(pin.family) != m[1] {
			readErr = fmt.Errorf("family mismatch for %s: %s / %s", key, m[1], pin.family)
			break
		}
		if seen[key] {
			readErr = fmt.Errorf("duplicate capture source: %s", key)
			break
		}
		seen[key] = true
		if h.Size < 128 || h.Size > 64<<20 {
			readErr = fmt.Errorf("unexpected TID size %d: %s", h.Size, key)
			break
		}
		data := make([]byte, h.Size)
		if _, e = io.ReadFull(reader, data); e != nil {
			readErr = e
			break
		}
		sh := sha256.Sum256(data)
		hexhash := hex.EncodeToString(sh[:])
		if !strings.EqualFold(hexhash, pin.pin.SHA) {
			readErr = fmt.Errorf("source SHA mismatch: %s", key)
			break
		}
		meta, e := mskit.ParseTIDMeta(data)
		if e != nil {
			readErr = e
			break
		}
		if meta.Width != pin.pin.Width || meta.Height != pin.pin.Height || meta.Format != pin.pin.Format {
			readErr = fmt.Errorf("source TID metadata mismatch: %s", key)
			break
		}
		// Paths are generated exclusively from manifest locators, never tar paths.
		outRel := fmt.Sprintf("sources/%s/a%03d_g%05d_%s", pin.family, a, g, m[4])
		out := filepath.Join(stage, outRel)
		if e = os.MkdirAll(filepath.Dir(out), 0755); e != nil {
			readErr = e
			break
		}
		if e = os.WriteFile(out, data, 0600); e != nil {
			readErr = e
			break
		}
		covered[pin.family] = true
		r.Entries = append(r.Entries, graphicsSourceTarget{Family: pin.family, Archive: a, GDAT: g, Inner: m[4], SHA: hexhash, Width: meta.Width, Height: meta.Height, Format: meta.Format, Output: outRel})
	}
	// Always reap the decompressor even when parsing aborts; prevent a stuck
	// producer writing to an unread pipe.
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return r, readErr
	}
	if waitErr != nil {
		return r, fmt.Errorf("zstd decode failed: %w (%s)", waitErr, errorText.String())
	}
	if len(r.Entries) != 37 {
		return r, fmt.Errorf("incomplete G02 corpus: got %d expected 37", len(r.Entries))
	}
	// Additional source-proven original TIDs from the private source corpus
	// private capture. These four real assets are absent from G02. This step
	// is optional: it never guesses the missing sources of other families.
	if extraDir != "" {
		ents, e := os.ReadDir(extraDir)
		if e != nil {
			return r, e
		}
		for _, ent := range ents {
			if !strings.HasSuffix(strings.ToLower(ent.Name()), ".tid") {
				continue
			}
			if !ent.Type().IsRegular() {
				return r, fmt.Errorf("extra source is not a regular file: %s", ent.Name())
			}
			data, e := os.ReadFile(filepath.Join(extraDir, ent.Name()))
			if e != nil {
				return r, e
			}
			if len(data) < 128 || len(data) > 64<<20 {
				return r, fmt.Errorf("extra source size invalid: %s", ent.Name())
			}
			h := sha256.Sum256(data)
			hash := hex.EncodeToString(h[:])
			var matchingKey string
			var matching struct {
				family string
				pin    graphicsSourcePin
			}
			for key, expected := range pins {
				if strings.EqualFold(hash, expected.pin.SHA) {
					if matchingKey != "" {
						return r, fmt.Errorf("ambiguous extra source SHA: %s", ent.Name())
					}
					matchingKey = key
					matching = expected
				}
			}
			if matchingKey == "" {
				return r, fmt.Errorf("extra source not SHA-pinned in manifest: %s", ent.Name())
			}
			if seen[matchingKey] {
				return r, fmt.Errorf("duplicate verified source in capture and additional directory: %s", matchingKey)
			}
			meta, e := mskit.ParseTIDMeta(data)
			if e != nil {
				return r, e
			}
			if meta.Width != matching.pin.Width || meta.Height != matching.pin.Height || meta.Format != matching.pin.Format {
				return r, fmt.Errorf("extra source metadata mismatch: %s", matchingKey)
			}
			// No filenames from untrusted tar/data can be used for output paths.
			// The original corpus locator is from the canonical manifest.
			for key, expected := range pins {
				if key == matchingKey {
					var a, g int
					var inner string
					if _, e = fmt.Sscanf(key, "%d/%d/%s", &a, &g, &inner); e != nil {
						return r, e
					}
					outRel := fmt.Sprintf("sources/%s/a%03d_g%05d_%s", expected.family, a, g, inner)
					out := filepath.Join(stage, outRel)
					if e = os.MkdirAll(filepath.Dir(out), 0755); e != nil {
						return r, e
					}
					if e = os.WriteFile(out, data, 0600); e != nil {
						return r, e
					}
					seen[key] = true
					covered[expected.family] = true
					r.Entries = append(r.Entries, graphicsSourceTarget{Family: expected.family, Archive: a, GDAT: g, Inner: inner, SHA: hash, Width: meta.Width, Height: meta.Height, Format: meta.Format, Output: outRel})
				}
			}
		}
	}
	sort.Slice(r.Entries, func(i, j int) bool { return r.Entries[i].Output < r.Entries[j].Output })
	for _, f := range families {
		if covered[f] {
			r.FamiliesCovered = append(r.FamiliesCovered, f)
		} else {
			r.FamiliesNotCovered = append(r.FamiliesNotCovered, f)
		}
	}
	sort.Strings(r.FamiliesCovered)
	sort.Strings(r.FamiliesNotCovered)
	r.SourceTargets = len(r.Entries)
	r.Status = "PASS_PINNED_SOURCE_CORPUS_STAGED_NO_GAME_CHANGE"
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return r, err
	}
	if err = os.WriteFile(filepath.Join(stage, "SOURCE_CORPUS_REPORT.json"), append(b, '\n'), 0600); err != nil {
		return r, err
	}
	if err = os.Rename(stage, dest); err != nil {
		return r, err
	}
	return r, nil
}
func executeGraphicsStageGo(root string, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("graphics-stage-sources-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	capture := fs.String("capture", "", "private SHA-pinned G02 .tar.zst capture")
	dest := fs.String("out-dir", "", "new private staging directory (must not already exist)")
	extra := fs.String("additional-dir", "", "optional additional private originals from a separate original-source capture")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *capture == "" || *dest == "" {
		return errors.New("usage: graphics-stage-sources-go --capture PRIVATE_G02.tar.zst --out-dir NEW_PRIVATE_DIRECTORY")
	}
	if filepath.Clean(*capture) == filepath.Clean(*dest) {
		return errors.New("source/destination collision")
	}
	r, err := graphicsSourceArchiveStage(root, *capture, *extra, *dest)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(r)
}
