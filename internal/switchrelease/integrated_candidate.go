package switchrelease

// Source-bound integration for latest Western Switch update candidates.
// The TUI and CLI both require an explicit candidate confirmation; combined
// gameplay and rendering still require final in-game QA.
// Every invocation stages fresh Switch edits from canonical PC translations,
// builds native Go text archives and merges them with approved graphics/IPS.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/switchtext"
)

type TextAsset struct {
	Path   string
	SHA256 string
}

type IntegratedOptions struct {
	Options
	MS2NativeRecords, FinaleNativeRecords string
	// Both must be supplied to reuse a previously generated canonical graphics
	// candidate without invoking Python. This does not approve its provenance.
	CanonicalGraphicsZIP, CanonicalGraphicsSHA256 string
}

type IntegratedResult struct {
	Releases               []Result
	TextReports            map[string]switchtext.Report
	NativeReports          map[string]switchtext.NativeWriteReport
	ReusableGraphicsZIP    string
	ReusableGraphicsSHA256 string
	GraphicsCacheWarning   string
}

var textArchiveNumbers = map[string][]int{
	"ms2":    {71, 72, 73},
	"finale": {1, 71, 72, 73, 77},
}

func expectedTextArchiveNames(game string) []string {
	numbers := textArchiveNumbers[game]
	out := make([]string, 0, len(numbers))
	for _, number := range numbers {
		out = append(out, fmt.Sprintf("GxArchivedFile%03d.dat", number))
	}
	return out
}

// validateTextAssets refuses partially-built, extra, modified, overlapping or
// linked payloads. No text file comes from a cache or from a second EN tree.
func validateTextAssets(root string, m Mapping, game, directory string, sr switchtext.Report, nr switchtext.NativeWriteReport) (map[string]TextAsset, error) {
	if sr.Game != game || nr.Game != game || nr.Status != "RESEARCH_CANDIDATE_NOT_RELEASE_READY" || nr.Schema != 1 || len(nr.Archives) != len(textArchiveNumbers[game]) {
		return nil, fmt.Errorf("%s: stage/native report game or expected archive set mismatch", game)
	}
	if sr.MappedFields == 0 || sr.EditedFields == 0 || sr.EditedFields+sr.UnchangedFields != sr.MappedFields {
		return nil, fmt.Errorf("%s: incomplete canonical accounting", game)
	}
	expected := map[string]bool{}
	for _, name := range expectedTextArchiveNames(game) {
		if _, graphic := m.Games[game].Archives[name]; graphic {
			return nil, fmt.Errorf("%s: text/graphics GDAT collision on %s", game, name)
		}
		expected[name] = true
	}
	edited := 0
	result := map[string]TextAsset{}
	for _, ar := range nr.Archives {
		name := fmt.Sprintf("GxArchivedFile%03d.dat", ar.Number)
		if !expected[name] {
			return nil, fmt.Errorf("%s: unexpected/duplicate native output %s", game, name)
		}
		delete(expected, name)
		if ar.EditedEntries == 0 || ar.EditedFields == 0 || ar.SourceSHA == ar.OutputSHA || len(ar.SourceSHA) != 64 || len(ar.OutputSHA) != 64 {
			return nil, fmt.Errorf("%s: invalid changed archive report %s", game, name)
		}
		edited += ar.EditedFields
		path := filepath.Join(directory, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: non-regular native archive %s", game, path)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		var magic [4]byte
		_, err = io.ReadFull(file, magic[:])
		file.Close()
		if err != nil || string(magic[:]) != "GDAT" {
			return nil, fmt.Errorf("%s: invalid GDAT %s", game, name)
		}
		sum, err := sumFile(path)
		if err != nil {
			return nil, err
		}
		if sum != ar.OutputSHA {
			return nil, fmt.Errorf("%s: output SHA mismatch %s", game, name)
		}
		result[game+"/"+name] = TextAsset{Path: path, SHA256: sum}
	}
	if len(expected) > 0 || edited != sr.EditedFields {
		return nil, fmt.Errorf("%s: missing expected archives or changed fields (%d vs %d)", game, edited, sr.EditedFields)
	}
	// Native output directory must contain only the complete exact archive set
	// plus its generated writer report (which is never included in the ZIP).
	members, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	if len(members) != len(nr.Archives)+1 {
		return nil, fmt.Errorf("%s: extraneous native output files", game)
	}
	for _, member := range members {
		if member.Name() == "native-write-report.json" {
			continue
		}
		if _, ok := result[game+"/"+member.Name()]; !ok {
			return nil, fmt.Errorf("%s: unexpected native output member %s", game, member.Name())
		}
	}
	return result, nil
}

// validatePinnedCanonicalGraphics verifies only the caller-supplied transport
// identity. packCanonical subsequently checks the complete report, accepted IPS,
// DAT content digests and release layout. The pin is NOT source/render approval.
func validatePinnedCanonicalGraphics(path, pin string) error {
	if path == "" || pin == "" {
		return errors.New("prebuilt graphics ZIP and SHA-256 must be supplied together")
	}
	if len(pin) != 64 {
		return errors.New("canonical graphics SHA-256 must contain exactly 64 hex digits")
	}
	for _, c := range pin {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return errors.New("invalid canonical graphics SHA-256")
		}
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("canonical graphics ZIP is not a regular file: %s", path)
	}
	actual, err := sumFile(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, pin) {
		return fmt.Errorf("canonical graphics ZIP SHA-256 mismatch: %s", path)
	}
	return nil
}

// BuildIntegratedCandidate is a research-only Go orchestration gate. It
// intentionally does NOT certify remaining unmapped/applicability fields,
// visual V18 acceptance or hardware runtime, and must not publish as approved.
func BuildIntegratedCandidate(o IntegratedOptions) (IntegratedResult, error) {
	var out IntegratedResult
	if !o.Candidate {
		return out, errors.New("combined Switch release is NOT runtime-approved: explicitly set Candidate")
	}
	// Original inputs are retained in a private SHA-pinned project cache.
	// Explicit source paths are one-time import inputs, not an obligation to
	// re-extract or keep an external master directory between builds.
	usingPinnedGraphics := o.CanonicalGraphicsZIP != "" || o.CanonicalGraphicsSHA256 != ""
	// A verified input-addressed cached image is preferred automatically. A
	// first build with incomplete metadata simply renders normally.
	if !usingPinnedGraphics {
		if cached, pinned, ok := findReusableGraphics(o.Root); ok {
			o.CanonicalGraphicsZIP, o.CanonicalGraphicsSHA256 = cached, pinned
			usingPinnedGraphics = true
		}
	}
	if usingPinnedGraphics {
		if err := validatePinnedCanonicalGraphics(o.CanonicalGraphicsZIP, o.CanonicalGraphicsSHA256); err != nil {
			return out, fmt.Errorf("explicit prebuilt graphics candidate rejected: %w", err)
		}
	}
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return out, err
	}
	m, err := LoadMapping(root)
	if err != nil {
		return out, err
	}
	v, err := version(root, o.Version)
	if err != nil {
		return out, err
	}
	pinned, err := version(root, "")
	if err != nil {
		return out, err
	}
	if v != pinned {
		return out, fmt.Errorf("version.txt %s conflicts with output version %s", pinned, v)
	}
	plat, err := platform(o.Platform)
	if err != nil {
		return out, err
	}
	if _, err := EnsureIntegratedSourceCache(root, o.MS2Master, o.FinaleMaster); err != nil {
		return out, err
	}
	sources := map[string]string{"ms2": cacheMaster(root, "ms2"), "finale": cacheMaster(root, "finale")}
	native := map[string]string{"ms2": o.MS2NativeRecords, "finale": o.FinaleNativeRecords}
	for _, game := range games {
		if err := ValidateSource(m, game, sources[game]); err != nil {
			return out, err
		}
		for _, name := range expectedTextArchiveNames(game) {
			if _, collision := m.Games[game].Archives[name]; collision {
				return out, fmt.Errorf("%s/%s: text/graphics archive overlap", game, name)
			}
			path := filepath.Join(sources[game], name)
			info, err := os.Lstat(path)
			if err != nil {
				return out, fmt.Errorf("%s: missing original native text %s: %w", game, path, err)
			}
			if !info.Mode().IsRegular() {
				return out, fmt.Errorf("%s: source archive is not regular: %s", game, path)
			}
		}
		if native[game] == "" {
			native[game] = filepath.Join(root, ".local", "switch_text_port", game, "western-native-records.jsonl")
		}
		if info, err := os.Lstat(native[game]); err != nil || !info.Mode().IsRegular() {
			return out, fmt.Errorf("%s: native record index missing/non-regular; required private source: %s", game, native[game])
		}
	}
	outDir := o.OutputDir
	if outDir == "" {
		outDir = filepath.Join(root, ".local", "switch_release")
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return out, err
	}
	stage, err := os.MkdirTemp(outDir, ".integrated-switch-go-")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(stage)

	out.TextReports = map[string]switchtext.Report{}
	out.NativeReports = map[string]switchtext.NativeWriteReport{}
	textAssets := map[string]TextAsset{}
	for _, game := range games {
		edit := filepath.Join(stage, game+"-current-pc-edits.jsonl")
		sr, err := switchtext.Stage(switchtext.Options{ProjectRoot: root, Game: game, NativeRecords: native[game], Output: edit})
		if err != nil {
			return IntegratedResult{}, fmt.Errorf("%s: canonical stage: %w", game, err)
		}
		nativeDir := filepath.Join(stage, game+"-native-masterNX64")
		nr, err := switchtext.WriteNativeCandidate(switchtext.NativeWriteOptions{Game: game, StagedEdits: edit, SourceMaster: sources[game], OutputDir: nativeDir})
		if err != nil {
			return IntegratedResult{}, fmt.Errorf("%s: native Go writer: %w", game, err)
		}
		assets, err := validateTextAssets(root, m, game, nativeDir, sr, nr)
		if err != nil {
			return IntegratedResult{}, err
		}
		for key, asset := range assets {
			if _, dup := textAssets[key]; dup {
				return IntegratedResult{}, fmt.Errorf("duplicate native GDAT %s", key)
			}
			textAssets[key] = asset
		}
		out.TextReports[game] = sr
		out.NativeReports[game] = nr
	}
	if len(textAssets) != 8 {
		return IntegratedResult{}, fmt.Errorf("expected 8 native text archives, got %d", len(textAssets))
	}
	// Reuse is explicit and source-pinned to the caller's entire ZIP bytes.
	// The normal candidate retains its existing canonical Python raster path.
	// Neither path is a release or runtime-approval claim.
	canonicalZIP := o.CanonicalGraphicsZIP
	if !usingPinnedGraphics {

		script := filepath.Join(builderDir(root), rendererFile)
		canonicalZIP = filepath.Join(stage, "graphics-candidate.zip")
		for _, args := range [][]string{
			{script, "--project-root", root, "--self-test"},
			{script, "--project-root", root, "--ms2-master", sources["ms2"], "--finale-master", sources["finale"], "--out", canonicalZIP},
		} {
			if err := runGraphicsPython(root, o.Python, args, o.Stdout, o.Stderr); err != nil {
				return IntegratedResult{}, fmt.Errorf("canonical graphics builder: %w", err)
			}
		}
	}
	// Reject a replaced prebuilt ZIP, including one changed during the native
	// text stage, before making any final output visible.
	if usingPinnedGraphics {
		if err := validatePinnedCanonicalGraphics(canonicalZIP, o.CanonicalGraphicsSHA256); err != nil {
			return IntegratedResult{}, fmt.Errorf("prebuilt graphics candidate changed while staging text: %w", err)
		}
	}
	// ZIP output paths are checked and published only after both games' text,
	// graphic report, accepted IPS, member hashes, and BOTH ZIPs are staged.
	releases, err := packagePairWithText(root, canonicalZIP, outDir, stage, v, plat, textAssets)
	if err != nil {
		return IntegratedResult{}, err
	}
	out.Releases = releases
	if !usingPinnedGraphics {
		path, sha, err := preserveCanonicalGraphics(root, canonicalZIP)
		if err != nil {
			out.GraphicsCacheWarning = err.Error()
		} else {
			out.ReusableGraphicsZIP, out.ReusableGraphicsSHA256 = path, sha
			if e := rememberRenderedGraphics(root, sha); e != nil {
				out.GraphicsCacheWarning = fmt.Sprintf("automatic graphics cache not pinned: %v", e)
			}
		}
	}
	return out, nil
}
