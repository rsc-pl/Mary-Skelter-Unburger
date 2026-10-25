// Package switchrelease orchestrates the accepted latest-update Switch graphics
// builder and produces clean, versioned Atmosphere/Eden release ZIPs. No source
// ROM, extracted cache, proprietary keys, or maintainer previews are packed.
package switchrelease

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const builderRel = "toolchain/switch_port"
const mappingFile = "switch_graphics_mapping_closed.json"
const rendererFile = "render_graphics.py"

var versionPattern = regexp.MustCompile(`^[0-9][A-Za-z0-9._-]*$`)
var gdatPattern = regexp.MustCompile(`^GxArchivedFile[0-9]{3}\.dat$`)
var games = []string{"ms2", "finale"}

// Source map values are the accepted, source-bound R31C1 fingerprints.
type Mapping struct {
	Phase           string `json:"phase"`
	ApplicableCount int    `json:"applicable_source_target_count"`
	Games           map[string]struct {
		Archives map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"archives"`
	} `json:"games"`
}

type Options struct {
	Root, Version, MS2Master, FinaleMaster, OutputDir, Python, Platform string
	Candidate                                                           bool
	Stdout, Stderr                                                      io.Writer
}
type Result struct {
	Platform, ZIP, SHA256 string
	Files                 int
	Bytes                 int64
}

type SourceStatus struct {
	Game, Root string
	Files      []string
	Error      string
}

func builderDir(root string) string { return filepath.Join(root, builderRel) }
func LoadMapping(root string) (Mapping, error) {
	var m Mapping
	raw, err := os.ReadFile(filepath.Join(builderDir(root), mappingFile))
	if err != nil {
		return m, err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	if m.Phase != "switch_graphics_mapping_v1" || m.ApplicableCount != 40 {
		return m, fmt.Errorf("Switch graphics mapping schema/count mismatch: phase=%q count=%d", m.Phase, m.ApplicableCount)
	}
	if len(m.Games) != 2 {
		return m, fmt.Errorf("Switch mapping game set drift")
	}
	for _, game := range games {
		if len(m.Games[game].Archives) == 0 {
			return m, fmt.Errorf("%s: empty source archives", game)
		}
		for name, entry := range m.Games[game].Archives {
			if !gdatPattern.MatchString(name) || len(entry.SHA256) != 64 {
				return m, fmt.Errorf("%s: invalid mapped archive %q", game, name)
			}
		}
	}
	return m, nil
}
func required(m Mapping, game string) []string {
	var files []string
	for n := range m.Games[game].Archives {
		files = append(files, n)
	}
	sort.Strings(files)
	return files
}
func isDir(path string) bool { st, err := os.Stat(path); return err == nil && st.IsDir() }
func normalizeMaster(path string) string {
	if path == "" {
		return ""
	}
	for _, p := range []string{path, filepath.Join(path, "masterNX64"), filepath.Join(path, "romfs", "masterNX64"), filepath.Join(path, "romfs_full", "masterNX64")} {
		if isDir(p) {
			matches, _ := filepath.Glob(filepath.Join(p, "GxArchivedFile*.dat"))
			if len(matches) > 0 {
				return p
			}
		}
	}
	return path
}
func cacheMaster(root, game string) string {
	return filepath.Join(root, ".local", "switch_sources", game, "latest_update", "masterNX64")
}
func ResolveMaster(root, game, override string) string {
	if override != "" {
		return normalizeMaster(override)
	}
	if isDir(cacheMaster(root, game)) {
		return cacheMaster(root, game)
	}
	// Never probe paths outside the selected project cache. Historical
	// workspaces are not an implicit fallback; an explicit import is required.

	return ""
}
func sumFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func ValidateSource(m Mapping, game, master string) error {
	if master == "" {
		return fmt.Errorf("%s: source missing; supply --%s-master or import to .local/switch_sources", game, game)
	}
	for _, name := range required(m, game) {
		file := filepath.Join(master, name)
		f, err := os.Open(file)
		if err != nil {
			return fmt.Errorf("%s: %s: %w", game, file, err)
		}
		var sig [8]byte
		_, err = io.ReadFull(f, sig[:])
		f.Close()
		if err != nil {
			return fmt.Errorf("%s: short GDAT %s: %w", game, name, err)
		}
		if string(sig[:4]) != "GDAT" {
			return fmt.Errorf("%s: non-GDAT %s", game, name)
		}
		got, err := sumFile(file)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, m.Games[game].Archives[name].SHA256) {
			return fmt.Errorf("%s: %s source SHA-256 mismatch (wrong game/update/archive)", game, name)
		}
	}
	return nil
}
func Status(root, ms2, finale string) ([]SourceStatus, error) {
	m, err := LoadMapping(root)
	if err != nil {
		return nil, err
	}
	out := []SourceStatus{}
	for _, game := range games {
		override := ms2
		if game == "finale" {
			override = finale
		}
		path := ResolveMaster(root, game, override)
		e := ValidateSource(m, game, path)
		st := SourceStatus{Game: game, Root: path, Files: required(m, game)}
		if e != nil {
			st.Error = e.Error()
		}
		out = append(out, st)
	}
	return out, nil
}

// publishPinnedPrivateCache refuses to overwrite a cache entry, including an
// entry created after the import preflight. An existing regular file is accepted
// only when it exactly matches its source pin (idempotent re-import). A linked,
// corrupt or different entry is never silently replaced.
func publishPinnedPrivateCache(staged, target, expectedSHA string) error {
	if err := os.Link(staged, target); err == nil {
		return nil
	} else if !os.IsExist(err) {
		return err
	}
	fi, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("unsafe existing private cache entry (not regular): %s", target)
	}
	actual, err := sumFile(target)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expectedSHA) {
		return fmt.Errorf("private source cache collision with differing SHA-256: %s", target)
	}
	return nil
}

func writePinnedPrivateCacheManifest(path string, payload []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".cache-manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(payload)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return publishPinnedPrivateCache(f.Name(), path, checksumBytes(payload))
}

func checksumBytes(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// ImportSources copies ONLY mapped GDAT files, verified by pinned SHA-256, to
// private .local cache. All publications are atomic no-clobber hardlinks and
// repeated imports accept only identical verified regular files.
func ImportSources(root, ms2, finale string, stdout io.Writer) error {
	m, err := LoadMapping(root)
	if err != nil {
		return err
	}
	for _, game := range games {
		path := ms2
		if game == "finale" {
			path = finale
		}
		if path == "" {
			continue
		}
		path = normalizeMaster(path)
		if err = ValidateSource(m, game, path); err != nil {
			return err
		}
		dst := cacheMaster(root, game)
		if err = os.MkdirAll(dst, 0700); err != nil {
			return err
		}
		for _, name := range required(m, game) {
			src := filepath.Join(path, name)
			target := filepath.Join(dst, name)
			if filepath.Clean(src) == filepath.Clean(target) {
				continue
			}
			in, e := os.Open(src)
			if e != nil {
				return e
			}
			tmp, e := os.CreateTemp(dst, ".import-*")
			if e != nil {
				in.Close()
				return e
			}
			th := sha256.New()
			_, e = io.Copy(io.MultiWriter(tmp, th), in)
			in.Close()
			if e == nil {
				e = tmp.Sync()
			}
			if e == nil {
				e = tmp.Close()
			} else {
				tmp.Close()
			}
			if e != nil {
				os.Remove(tmp.Name())
				return e
			}
			if !strings.EqualFold(hex.EncodeToString(th.Sum(nil)), m.Games[game].Archives[name].SHA256) {
				os.Remove(tmp.Name())
				return fmt.Errorf("source changed during import: %s", src)
			}
			e = publishPinnedPrivateCache(tmp.Name(), target, m.Games[game].Archives[name].SHA256)
			_ = os.Remove(tmp.Name())
			if e != nil {
				return e
			}
		}
		// Private metadata contains hashes only; no absolute paths or keys.
		rec := struct {
			SchemaVersion int      `json:"schema_version"`
			Game          string   `json:"game"`
			Files         []string `json:"files"`
		}{1, game, required(m, game)}
		payload, _ := json.MarshalIndent(rec, "", "  ")
		if err = writePinnedPrivateCacheManifest(filepath.Dir(dst)+"/manifest.json", append(payload, '\n')); err != nil {
			return err
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "Imported %s: %d verified archives -> %s\n", game, len(required(m, game)), dst)
		}
	}
	return nil
}
func version(root, v string) (string, error) {
	if v == "" {
		raw, err := os.ReadFile(filepath.Join(root, "version.txt"))
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(string(raw))
	}
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if !versionPattern.MatchString(v) {
		return "", fmt.Errorf("unsafe release version %q", v)
	}
	return v, nil
}
func platform(p string) (string, error) {
	if p == "" {
		p = "both"
	}
	if p != "both" && p != "atmosphere" && p != "eden" {
		return "", fmt.Errorf("unsupported platform %q", p)
	}
	return p, nil
}

func releaseName(v, consumer string) string {
	suffix := "Atmosphere"
	if consumer == "eden" {
		suffix = "Eden"
	}
	return "Mary-Skelter-2-Finale-UNBURGER-Switch-" + suffix + "-v" + v + ".zip"
}

func edenFolder(game, v string) string {
	if game == "ms2" {
		return "Mary-Skelter-2-UNBURGER-v" + v + "/"
	}
	return "Mary-Skelter-Finale-UNBURGER-v" + v + "/"
}

// Build invokes the canonical Python renderer/archive engine. Go handles source
// identity, staging, release naming, ZIP validation, and clean distribution.
func Build(o Options) ([]Result, error) {
	var zero []Result
	m, err := LoadMapping(o.Root)
	if err != nil {
		return zero, err
	}
	v, err := version(o.Root, o.Version)
	if err != nil {
		return zero, err
	}
	// The upstream raster builder stamps version.txt into the rendered footer.
	// A differently named release would misrepresent what is inside the ZIP.
	projectVersion, err := version(o.Root, "")
	if err != nil {
		return zero, err
	}
	if v != projectVersion {
		return zero, fmt.Errorf("requested Switch release version %q differs from project graphics version %q; update version.txt and rebuild recipes first", v, projectVersion)
	}
	plat, err := platform(o.Platform)
	if err != nil {
		return zero, err
	}
	if !o.Candidate {
		return zero, errors.New("R32C3 V18 visual/runtime QA is pending: pass --candidate to explicitly build a test release")
	}
	ms2 := ResolveMaster(o.Root, "ms2", o.MS2Master)
	finale := ResolveMaster(o.Root, "finale", o.FinaleMaster)
	for _, pair := range []struct{ Game, Dir string }{{"ms2", ms2}, {"finale", finale}} {
		if err = ValidateSource(m, pair.Game, pair.Dir); err != nil {
			return zero, err
		}
	}

	if o.OutputDir == "" {
		o.OutputDir = filepath.Join(o.Root, ".local", "switch_release")
	}
	if err = os.MkdirAll(o.OutputDir, 0755); err != nil {
		return zero, err
	}
	stage, err := os.MkdirTemp(o.OutputDir, ".switch-build-")
	if err != nil {
		return zero, err
	}
	defer os.RemoveAll(stage)
	script := filepath.Join(builderDir(o.Root), rendererFile)
	for _, args := range [][]string{{script, "--project-root", o.Root, "--self-test"}, {script, "--project-root", o.Root, "--ms2-master", ms2, "--finale-master", finale, "--out", filepath.Join(stage, "canonical.zip")}} {
		if err = runGraphicsPython(o.Root, o.Python, args, o.Stdout, o.Stderr); err != nil {
			return zero, fmt.Errorf("canonical Switch build failed: %w", err)
		}
	}
	return packagePair(o.Root, filepath.Join(stage, "canonical.zip"), o.OutputDir, stage, v, plat)
}

func packagePair(root, canonical, outDir, stage, v, plat string) ([]Result, error) {
	return packagePairWithText(root, canonical, outDir, stage, v, plat, nil)
}

func packagePairWithText(root, canonical, outDir, stage, v, plat string, textAssets map[string]TextAsset) ([]Result, error) {
	return packagePairWithTextHook(root, canonical, outDir, stage, v, plat, textAssets, nil)
}

// packagePairWithTextHook exposes only a test fault-injection seam. All release
// files are rendered and verified before any final pathname becomes visible.
// On Linux os.Rename would overwrite a file created after the initial existence
// check. Hardlinks provide atomic no-clobber publication on the same filesystem.
func packagePairWithTextHook(root, canonical, outDir, stage, v, plat string, textAssets map[string]TextAsset, beforePublish func(int, string) error) ([]Result, error) {
	var zero []Result
	var err error
	// One canonical archive build feeds TWO independent consumer layouts.
	// Fail before publishing anything if either requested destination exists.
	platforms := []string{"atmosphere", "eden"}
	if plat != "both" {
		platforms = []string{plat}
	}
	type stagedRelease struct {
		result                  Result
		staged, target, sumTemp string
	}
	prepared := make([]stagedRelease, 0, len(platforms))
	for _, consumer := range platforms {
		name := releaseName(v, consumer)
		target := filepath.Join(outDir, name)
		if _, e := os.Stat(target); e == nil {
			return zero, fmt.Errorf("release exists; refusing to overwrite: %s", target)
		} else if !os.IsNotExist(e) {
			return zero, e
		}
		if _, e := os.Stat(target + ".sha256"); e == nil {
			return zero, fmt.Errorf("release checksum exists; refusing to overwrite: %s.sha256", target)
		} else if !os.IsNotExist(e) {
			return zero, e
		}
		prepared = append(prepared, stagedRelease{target: target, staged: filepath.Join(stage, name), sumTemp: filepath.Join(stage, name+".sha256")})
	}
	for i, consumer := range platforms {
		item := &prepared[i]
		result, e := packCanonical(root, canonical, item.staged, v, consumer, textAssets)
		if e != nil {
			return zero, e
		}
		result.Platform = consumer
		result.ZIP = item.target
		item.result = result
		if e = os.WriteFile(item.sumTemp, []byte(result.SHA256+"  "+filepath.Base(item.target)+"\n"), 0644); e != nil {
			return zero, e
		}
	}
	// Stage and final destinations share outDir's filesystem. os.Link refuses
	// *any* existing destination, including a last-moment symlink; os.Rename
	// could overwrite one after the preflight. Roll back ONLY links created here.
	var created []string
	for i, item := range prepared {
		if beforePublish != nil {
			if err = beforePublish(i, "zip"); err != nil {
				break
			}
		}
		if err = os.Link(item.staged, item.target); err != nil {
			break
		}
		created = append(created, item.target)
		if beforePublish != nil {
			if err = beforePublish(i, "sha256"); err != nil {
				break
			}
		}
		if err = os.Link(item.sumTemp, item.target+".sha256"); err != nil {
			break
		}
		created = append(created, item.target+".sha256")
	}
	if err != nil {
		for i := len(created) - 1; i >= 0; i-- {
			_ = os.Remove(created[i])
		}
		return zero, fmt.Errorf("publishing Switch release (no-clobber; own files rolled back): %w", err)
	}
	results := make([]Result, 0, len(prepared))
	for _, item := range prepared {
		results = append(results, item.result)
	}
	return results, nil
}

type layout struct{ TitleID, BuildID string }

var accepted = map[string]layout{
	"ms2":    {"01003DE00C95E000", "C16E154E683D7EB814B0AB840CBFBE36"},
	"finale": {"0100530014438000", "B1AFBB02475AD7E35618B4C0DAEEA1B1027AF62F"},
}

type archiveInfo struct{ Game, Name, SHA256 string }
type buildReport struct {
	SourceTargets   int           `json:"source_target_count"`
	ModifiedTargets int           `json:"modified_target_count"`
	NonTargetGDAT   bool          `json:"non_target_gdat_payloads_byte_identical"`
	Archives        []archiveInfo `json:"-"`
}

type releaseFile struct {
	path       string
	source     *zip.File
	sourcePath string
	sha256     string
	data       []byte
}

func addEntry(entries map[string]releaseFile, path string, source *zip.File, data []byte) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.Contains(path, "..") {
		return fmt.Errorf("invalid release path %q", path)
	}
	if _, ok := entries[path]; ok {
		return fmt.Errorf("duplicate release path %q", path)
	}
	entries[path] = releaseFile{path: path, source: source, data: data}
	return nil
}
func copyStream(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }
func hashZipFile(z *zip.File) (string, error) {
	r, e := z.Open()
	if e != nil {
		return "", e
	}
	defer r.Close()
	h := sha256.New()
	_, e = io.Copy(h, r)
	return hex.EncodeToString(h.Sum(nil)), e
}
func fileData(z *zip.File) ([]byte, error) {
	r, e := z.Open()
	if e != nil {
		return nil, e
	}
	defer r.Close()
	return io.ReadAll(io.LimitReader(r, 1024*1024))
}
func acceptedIPS(root, game string) ([]byte, error) {
	name := "accepted_ms2_latest_update.ips"
	if game == "finale" {
		name = "accepted_finale_latest_update.ips"
	}
	return os.ReadFile(filepath.Join(builderDir(root), name))
}

// PackCanonical validates the private canonical candidate ZIP and emits ONLY
// approved DAT/IPS payloads plus a versioned install README. No previews,
// absolute source paths, or maintainer reports may enter the release.
func PackCanonical(root, canonical, dest, version, plat string) (Result, error) {
	return packCanonical(root, canonical, dest, version, plat, nil)
}

// packCanonical merges the verified native-Go text outputs into exactly the
// approved graphics/IPS candidate layout. nil textAssets retains the historic
// graphics-only contract; it must never be relabelled as a complete release.
func packCanonical(root, canonical, dest, version, plat string, textAssets map[string]TextAsset) (Result, error) {
	var zero Result
	m, err := LoadMapping(root)
	if err != nil {
		return zero, err
	}
	if _, err = platform(plat); err != nil {
		return zero, err
	}
	if plat == "both" {
		return zero, errors.New("PackCanonical requires one platform per ZIP; use Build for both")
	}
	z, err := zip.OpenReader(canonical)
	if err != nil {
		return zero, err
	}
	defer z.Close()
	var report struct {
		SourceTargets    int `json:"source_target_count"`
		ModifiedTargets  int `json:"modified_target_count"`
		PreservedTargets int `json:"preserved_target_count"`
		CanonicalTargets int `json:"canonical_target_count"`
		CanonicalRecords []struct {
			Game           string `json:"game"`
			AssetID        string `json:"asset_id"`
			Archive        string `json:"archive"`
			GDATIndex      int    `json:"gdat_index"`
			InnerName      string `json:"inner_name"`
			Policy         string `json:"policy"`
			ChangedNames   int    `json:"changed_names"`
			UntouchedNames int    `json:"untouched_names"`
			CapHeight      int    `json:"canonical_cap_height"`
			SourceTIDSHA   string `json:"original_source_tid_sha256"`
			FontSHA        string `json:"font_sha256"`
		} `json:"canonical_targets"`
		PreservedRecords []struct {
			Game           string `json:"game"`
			AssetID        string `json:"asset_id"`
			Archive        string `json:"archive"`
			GDATIndex      int    `json:"gdat_index"`
			InnerName      string `json:"inner_name"`
			Policy         string `json:"policy"`
			SourceEntrySHA string `json:"source_gdat_entry_sha256"`
			SourceCL3SHA   string `json:"source_decoded_cl3_sha256"`
		} `json:"preserved_targets"`
		NonTargetGDAT bool `json:"non_target_gdat_payloads_byte_identical"`
		NonTargetCL3  bool `json:"non_target_cl3_members_byte_identical"`
		Archives      []struct {
			Game      string `json:"game"`
			Archive   string `json:"archive"`
			OutputSHA string `json:"output_sha256"`
		} `json:"archives"`
	}
	index := map[string]*zip.File{}
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if _, ok := index[f.Name]; ok {
			return zero, fmt.Errorf("duplicate canonical entry %q", f.Name)
		}
		index[f.Name] = f
	}
	rep, ok := index["R32C3_BUILD_REPORT.json"]
	if !ok {
		return zero, errors.New("canonical report missing")
	}
	r, e := rep.Open()
	if e != nil {
		return zero, e
	}
	e = json.NewDecoder(io.LimitReader(r, 4<<20)).Decode(&report)
	r.Close()
	if e != nil {
		return zero, e
	}
	// all 40 mapped textures are rewritten, including exactly one
	// runtime-approved whole-word canonical V2 texture (MS2 074/g133).
	if report.SourceTargets != 40 || report.ModifiedTargets != 40 ||
		report.PreservedTargets != 0 || len(report.PreservedRecords) != 0 ||
		report.CanonicalTargets != 1 || !report.NonTargetGDAT || !report.NonTargetCL3 {
		return zero, fmt.Errorf("canonical QA report invalid: source=%d modified=%d canonical=%d preserved=%d",
			report.SourceTargets, report.ModifiedTargets, report.CanonicalTargets, report.PreservedTargets)
	}
	if len(report.CanonicalRecords) != 1 ||
		report.CanonicalRecords[0].Game != "ms2" ||
		report.CanonicalRecords[0].AssetID != "MS2_CAMP_TOP2_CHARACTER_NAMES" ||
		report.CanonicalRecords[0].Archive != "GxArchivedFile074.dat" ||
		report.CanonicalRecords[0].GDATIndex != 133 ||
		report.CanonicalRecords[0].InnerName != "camp_top_2.tid" ||
		report.CanonicalRecords[0].Policy != "canonical_whole_words_v2" ||
		report.CanonicalRecords[0].ChangedNames != 8 ||
		report.CanonicalRecords[0].UntouchedNames != 6 ||
		report.CanonicalRecords[0].CapHeight != 25 ||
		report.CanonicalRecords[0].SourceTIDSHA != "6cac6e1c439db75ac43084e3452fb959c11984030b178f7b6a23c2ba445d8f39" ||
		len(report.CanonicalRecords[0].FontSHA) != 64 {
		return zero, errors.New("approved MS2 074/g133 whole-word V2 source/label proof missing or invalid")
	}
	expected := map[string]string{}
	for _, g := range games {
		for _, n := range required(m, g) {
			expected[g+"/"+n] = ""
		}
	}
	for _, ar := range report.Archives {
		key := ar.Game + "/" + ar.Archive
		if _, ok := expected[key]; !ok || expected[key] != "" || len(ar.OutputSHA) != 64 {
			return zero, fmt.Errorf("invalid canonical archive QA record %s", key)
		}
		expected[key] = strings.ToLower(ar.OutputSHA)
	}
	for key, value := range expected {
		if value == "" {
			return zero, fmt.Errorf("canonical report omitted %s", key)
		}
	}
	entries := map[string]releaseFile{}
	for _, game := range games {
		l := accepted[game]
		ips, err := acceptedIPS(root, game)
		if err != nil {
			return zero, err
		}
		ipsName := l.BuildID + ".ips"
		ipsCanonical := findCanonical(index, l.TitleID, "exefs/"+ipsName)
		if ipsCanonical == nil {
			return zero, fmt.Errorf("%s: missing canonical IPS", game)
		}
		contents, err := fileData(ipsCanonical)
		if err != nil {
			return zero, err
		}
		if string(contents) != string(ips) {
			return zero, fmt.Errorf("%s: accepted IPS differs from canonical", game)
		}
		eRoot := edenFolder(game, version)
		if plat == "eden" {
			if err = addEntry(entries, eRoot+"exefs/"+ipsName, nil, ips); err != nil {
				return zero, err
			}
		}
		if plat == "atmosphere" {
			if err = addEntry(entries, "atmosphere/exefs_patches/MarySkelter_UNBURGER/"+ipsName, nil, ips); err != nil {
				return zero, err
			}
		}
		for _, name := range required(m, game) {
			f := findCanonical(index, l.TitleID, "romfs/masterNX64/"+name)
			if f == nil {
				return zero, fmt.Errorf("%s: canonical archive missing %s", game, name)
			}
			sum, err := hashZipFile(f)
			if err != nil {
				return zero, err
			}
			if sum != expected[game+"/"+name] {
				return zero, fmt.Errorf("%s: canonical archive checksum mismatch: %s", game, name)
			}
			fh, err := f.Open()
			if err != nil {
				return zero, err
			}
			var sig [8]byte
			_, err = io.ReadFull(fh, sig[:])
			fh.Close()
			if err != nil || string(sig[:4]) != "GDAT" {
				return zero, fmt.Errorf("invalid rebuilt GDAT: %s", name)
			}
			if plat == "eden" {
				if err = addEntry(entries, eRoot+"romfs/masterNX64/"+name, f, nil); err != nil {
					return zero, err
				}
			}
			if plat == "atmosphere" {
				if err = addEntry(entries, "atmosphere/contents/"+l.TitleID+"/romfs/masterNX64/"+name, f, nil); err != nil {
					return zero, err
				}
			}
		}
		if textAssets != nil {
			for _, name := range expectedTextArchiveNames(game) {
				id := game + "/" + name
				asset, ok := textAssets[id]
				if !ok {
					return zero, fmt.Errorf("missing native-Go text archive %s", id)
				}
				output := eRoot + "romfs/masterNX64/" + name
				if plat == "atmosphere" {
					output = "atmosphere/contents/" + l.TitleID + "/romfs/masterNX64/" + name
				}
				if err = addEntry(entries, output, nil, nil); err != nil {
					return zero, err
				}
				ent := entries[output]
				ent.sourcePath = asset.Path
				ent.sha256 = asset.SHA256
				entries[output] = ent
			}
		}
	}
	// Exact consumer layout only: Eden archive contains two mod folders;
	// Atmosphere archive contains only the 'atmosphere/' tree. User-facing
	// instructions live in the project documentation, not in the ZIP root.
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	if err = os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return zero, err
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return zero, err
	}
	cleanup := true
	defer func() {
		out.Close()
		if cleanup {
			os.Remove(dest)
		}
	}()
	zw := zip.NewWriter(out)
	var bytes int64
	for _, name := range names {
		entry := entries[name]
		head := &zip.FileHeader{Name: name, Method: zip.Deflate}
		head.SetModTime(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC))
		head.SetMode(0644)
		writer, err := zw.CreateHeader(head)
		if err != nil {
			return zero, err
		}
		if entry.sourcePath != "" {
			f, e := os.Open(entry.sourcePath)
			if e != nil {
				return zero, e
			}
			h := sha256.New()
			n, e := io.Copy(io.MultiWriter(writer, h), f)
			closeErr := f.Close()
			if e != nil {
				return zero, e
			}
			if closeErr != nil {
				return zero, closeErr
			}
			if hex.EncodeToString(h.Sum(nil)) != entry.sha256 {
				return zero, fmt.Errorf("native-Go text archive changed during ZIP build: %s", entry.path)
			}
			bytes += n
		} else if entry.source != nil {
			r, e := entry.source.Open()
			if e != nil {
				return zero, e
			}
			n, e := copyStream(writer, r)
			r.Close()
			if e != nil {
				return zero, e
			}
			bytes += n
		} else {
			n, e := writer.Write(entry.data)
			if e != nil {
				return zero, e
			}
			bytes += int64(n)
		}
	}
	if err = zw.Close(); err != nil {
		return zero, err
	}
	if err = out.Sync(); err != nil {
		return zero, err
	}
	if err = out.Close(); err != nil {
		return zero, err
	}
	sum, err := sumFile(dest)
	if err != nil {
		return zero, err
	}
	verify, err := zip.OpenReader(dest)
	if err != nil {
		return zero, err
	}
	if len(verify.File) != len(entries) {
		verify.Close()
		return zero, errors.New("release ZIP entry count drift")
	}
	verify.Close()
	cleanup = false
	return Result{ZIP: dest, SHA256: sum, Files: len(entries), Bytes: bytes}, nil
}
func findCanonical(index map[string]*zip.File, titleID, suffix string) *zip.File {
	var found *zip.File
	prefix := "2_EMULATOR_EDEN/load/" + titleID + "/"
	for name, f := range index {
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, "/"+suffix) {
			if found != nil {
				return nil
			}
			found = f
		}
	}
	return found
}
