package maintainer

// NativeProvenPCGraphics bridges allowlisted original-source-verified Go font
// families to real, user-supplied PC masterWindows archives. It is explicitly
// opt-in until a real full-master/game QA is available. No Python or Go toolchain
// is invoked at runtime: a separately compiled bcn-tid-go binary is required.
// Original archives, accepted production output and canonical translations are
// never modified by this operation.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type NativeProvenPCGraphicsOptions struct {
	MS2Master                string
	FinaleMaster             string
	OutputDir                string
	RendererBinary           string
	ValidateOnly             bool
	IncludeDeferredNameBanks bool   // explicit research opt-in, never production allowlist
	StagedMS2Master          string // optional already rebuilt text archive directory
	StagedFinaleMaster       string // both required together; pristine still mandatory
}

type NativeProvenPCArchive struct {
	Dataset        string   `json:"dataset"`
	Archive        int      `json:"archive"`
	Source         string   `json:"source"`
	PristineSource string   `json:"pristine_source,omitempty"`
	PristineSHA256 string   `json:"pristine_sha256,omitempty"`
	SHA256         string   `json:"sha256"`
	Output         string   `json:"output,omitempty"`
	Families       []string `json:"families"`
}

type NativeProvenPCGraphicsReport struct {
	Status         string                  `json:"status"`
	Scope          string                  `json:"scope"`
	InputMode      string                  `json:"input_mode"`
	Families       int                     `json:"families"`
	Archives       []NativeProvenPCArchive `json:"archives"`
	RendererReport json.RawMessage         `json:"renderer_report,omitempty"`
}

// This allowlist is intentionally independent of the renderer. An untested
// family must never become writable merely because it appears in manifest.json.
var nativeProvenPCFamilyIDs = []string{
	"MS2_CAMP_TOP2_CHARACTER_NAMES", "MS2_CAMP_TOP_LEGACY_NAMES",
	"MS2_BONUS_CHARACTER_NAMES", "MS2_JOB_CHARACTER_NAMES",
	"MSF_EN_BONUS_CHARACTER_NAMES", "MS2_BONUS_CHARACTER_SELECT_NAMES",
	"MSF_EN_BONUS_CHARACTER_SELECT_NAMES", "MSF_EN_HEROINE_ROOM_NAMES",
	"MSF_EN_CAMP_CHARACTER_NAMES", "MSF_EN_REN_BONUS_CHARACTER_NAMES",
	"MS2_HEROINE_ROOM_LIST_BLUE", "MS2_MYROOM_TITLE",
	"MS2_DLC_JOB_LABELS", "MSF_EN_FINALE_CHARACTER_GRID",
	"MS2_JOB_CLASS_24PX", "MS2_JOB_CLASS_MIDDLE", "MS2_KESSIKI_SKILL_LABELS",
}

// These two source-pinned banks are visually accepted/provisional but not yet
// counted among the 17 production-proven families. Never enable by default.
var nativeDeferredPCFamilyIDs = []string{"MS2_RESULT_NAMES", "MS2_BATTLE_NAMES"}

func nativePCRequestedFamilies(includeDeferred bool) []string {
	ids := append([]string(nil), nativeProvenPCFamilyIDs...)
	if includeDeferred {
		ids = append(ids, nativeDeferredPCFamilyIDs...)
	}
	return ids
}

type nativeProvenGraphicsMember struct {
	Index int
	Inner string
	SHA   string
}

type nativeProvenGraphicsTarget struct {
	Index int    `json:"gdat_index"`
	Inner string `json:"inner_name"`
	SHA   string `json:"tid_sha256"`
}

type nativeProvenFamilyManifest struct {
	SchemaVersion int `json:"schema_version"`
	Assets        []struct {
		ID             string `json:"id"`
		Enabled        bool   `json:"enabled"`
		Dataset        string `json:"dataset"`
		Archive        int    `json:"archive"`
		GDATIndex      int    `json:"gdat_index"`
		Inner          string `json:"cl3_inner_name"`
		SourceVariants []struct {
			SHA string `json:"tid_sha256"`
		} `json:"source_variants"`
		Targets []nativeProvenGraphicsTarget `json:"targets"`
	} `json:"assets"`
}

type nativeProvenRendererManifest struct {
	EnableDeferredNameBanks bool   `json:"enable_deferred_name_banks,omitempty"`
	Version                 int    `json:"version"`
	ProjectRoot             string `json:"project_root"`
	Jobs                    []struct {
		Source       string   `json:"source"`
		SourceSHA256 string   `json:"source_sha256"`
		Output       string   `json:"output"`
		Families     []string `json:"families"`
		Quality      int      `json:"quality"`
		MaxAlpha     int      `json:"max_alpha"`
		MaxVisible   int      `json:"max_visible"`
	} `json:"jobs"`
}

func nativeProvenReadRegular(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular source archive: %s", path)
	}
	return os.ReadFile(path)
}

// checkTextStagedGraphicsBase checks a text-edited DAT against a genuinely
// pristine DAT. Every source-authored target TID must still be byte-identical,
// while unrelated GDAT records may carry legitimate PC text changes. This
// is not a decoded canonical-text equivalence check.
func checkTextStagedGraphicsBase(pristine, staged []byte, targets map[int]map[string]string) error {
	original, err := mskit.ParseGDAT(pristine)
	if err != nil {
		return fmt.Errorf("pristine GDAT: %w", err)
	}
	candidate, err := mskit.ParseGDAT(staged)
	if err != nil {
		return fmt.Errorf("staged GDAT: %w", err)
	}
	if len(original.Entries) != len(candidate.Entries) {
		return fmt.Errorf("staged GDAT entry count differs from pristine: %d != %d", len(candidate.Entries), len(original.Entries))
	}
	if len(targets) == 0 {
		return errors.New("empty graphics source target set")
	}
	for index, members := range targets {
		if index < 0 || index >= len(original.Entries) || len(members) == 0 {
			return fmt.Errorf("missing source index %d", index)
		}
		for inner, expectedSHA := range members {
			if inner == "" || len(expectedSHA) != 64 {
				return fmt.Errorf("invalid target %d/%q", index, inner)
			}
			originalTID, _, err := mskit.ExtractGraphicsTID(pristine, index, inner)
			if err != nil {
				return fmt.Errorf("pristine target %d/%s: %w", index, inner, err)
			}
			actual := sha256.Sum256(originalTID)
			if !strings.EqualFold(hex.EncodeToString(actual[:]), expectedSHA) {
				return fmt.Errorf("pristine target %d/%s hash mismatch", index, inner)
			}
			stagedTID, _, err := mskit.ExtractGraphicsTID(staged, index, inner)
			if err != nil {
				return fmt.Errorf("staged target %d/%s: %w", index, inner, err)
			}
			if !bytes.Equal(originalTID, stagedTID) {
				return fmt.Errorf("staged graphics target %d/%s differs from pristine TID", index, inner)
			}
		}
	}
	return nil
}

// NativeProvenPCGraphics performs a read-only full-master audit when
// ValidateOnly=true. On a render run, it validates EVERY source TID and output
// destination before invoking the compiled Go encoder. The encoder performs
// final source SHA and per-family rechecks, stages outputs and publishes with
// no-clobber and rollback semantics.
func NativeProvenPCGraphics(root string, opt NativeProvenPCGraphicsOptions) (NativeProvenPCGraphicsReport, error) {
	report := NativeProvenPCGraphicsReport{Status: "BLOCKED", Scope: "allowlisted-source-proven-Go-families-not-production", InputMode: "pristine"}
	if opt.IncludeDeferredNameBanks {
		report.Scope = "opt-in-deferred-namebanks-candidate-not-production"
	}
	families := nativePCRequestedFamilies(opt.IncludeDeferredNameBanks)
	root, err := filepath.Abs(root)
	if err != nil {
		return report, err
	}
	raw, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return report, err
	}
	var sourceManifest nativeProvenFamilyManifest
	if err = json.Unmarshal(raw, &sourceManifest); err != nil {
		return report, err
	}
	if sourceManifest.SchemaVersion != 2 {
		return report, errors.New("unsupported canonical graphics manifest")
	}
	byID := map[string]struct {
		Dataset string
		Archive int
		Members []nativeProvenGraphicsMember
	}{}
	for _, a := range sourceManifest.Assets {
		for _, id := range families {
			if id != a.ID {
				continue
			}
			if _, exists := byID[id]; exists {
				return report, fmt.Errorf("duplicated family %s", id)
			}
			if !a.Enabled {
				return report, fmt.Errorf("disabled family %s", id)
			}
			if id == "MS2_RESULT_NAMES" || id == "MS2_BATTLE_NAMES" {
				wantIndex, wantInner, wantSHA := 1333, "result_2.tid", "3af42766364633da20762a59c3c3054f52179548ae0604a8265d773a0a980a14"
				if id == "MS2_BATTLE_NAMES" {
					wantIndex, wantInner, wantSHA = 1331, "battle_2.tid", "3dd5e08c9e7311acbc7cda6f5a77e65cb2f862aedc075bae3657fed83bf77379"
				}
				if a.Dataset != "MS2_PC_LOCALIZED" || a.Archive != 55 || a.GDATIndex != wantIndex || a.Inner != wantInner || len(a.SourceVariants) != 1 || !strings.EqualFold(a.SourceVariants[0].SHA, wantSHA) {
					return report, fmt.Errorf("deferred namebank %s source metadata drift", id)
				}
			}
			members := []nativeProvenGraphicsMember{}
			if id == "MS2_DLC_JOB_LABELS" {
				if a.Dataset != "MS2_PC_LOCALIZED" || a.Archive != 55 || len(a.Targets) != 6 || len(a.SourceVariants) != 0 {
					return report, fmt.Errorf("DLC family grouped manifest drift")
				}
				for i, v := range a.Targets {
					if v.Index != 1541+i || v.Inner == "" || len(v.SHA) != 64 {
						return report, fmt.Errorf("DLC member %d missing exact pinned metadata", i)
					}
					members = append(members, nativeProvenGraphicsMember{v.Index, v.Inner, v.SHA})
				}
			} else {
				if len(a.Targets) != 0 || len(a.SourceVariants) != 1 || len(a.SourceVariants[0].SHA) != 64 || a.Inner == "" {
					return report, fmt.Errorf("family %s missing exact pinned source metadata", id)
				}
				members = append(members, nativeProvenGraphicsMember{a.GDATIndex, a.Inner, a.SourceVariants[0].SHA})
			}
			byID[id] = struct {
				Dataset string
				Archive int
				Members []nativeProvenGraphicsMember
			}{a.Dataset, a.Archive, members}
		}
	}
	if len(byID) != len(families) {
		return report, fmt.Errorf("pinned family set incomplete: %d/%d", len(byID), len(families))
	}
	expectedTIDs := 0
	for _, members := range byID {
		expectedTIDs += len(members.Members)
	}

	if opt.MS2Master == "" || opt.FinaleMaster == "" {
		return report, errors.New("both explicit pristine MS2 and Finale masterWindows directories are required")
	}
	if !opt.ValidateOnly && (opt.RendererBinary == "" || opt.OutputDir == "") {
		return report, errors.New("renderer binary and output directory required to write candidates")
	}
	masters := map[string]string{"MS2_PC_LOCALIZED": opt.MS2Master, "MSF_PC_LOCALIZED": opt.FinaleMaster}
	stagedMode := opt.StagedMS2Master != "" || opt.StagedFinaleMaster != ""
	if stagedMode && (opt.StagedMS2Master == "" || opt.StagedFinaleMaster == "") {
		return report, errors.New("staged mode requires both staged PC masterWindows directories")
	}
	stagedMasters := map[string]string{"MS2_PC_LOCALIZED": opt.StagedMS2Master, "MSF_PC_LOCALIZED": opt.StagedFinaleMaster}
	if stagedMode {
		report.InputMode = "text-staged-TIDs-pristine-verified"
	}
	// Full pristine-DAT SHA pins are mandatory in staged mode. A staged DAT's
	// SHA is deliberately NOT the original: it may already contain canonical translation edits.
	pinned := map[string]sourceHashRow{}
	if stagedMode {
		rows, err := readSourceHashManifest(filepath.Join(root, "toolchain", "manifests", "expected_pc_archive_sha256.tsv"))
		if err != nil {
			return report, err
		}
		for _, row := range rows {
			k := row.Dataset + "/" + row.File
			if _, ok := pinned[k]; ok {
				return report, fmt.Errorf("duplicate pinned pristine manifest entry %s", k)
			}
			pinned[k] = row
		}
	}
	archives := map[string]*NativeProvenPCArchive{}
	for _, id := range families {
		a := byID[id]
		master, ok := masters[a.Dataset]
		if !ok {
			return report, fmt.Errorf("%s unexpected dataset %s", id, a.Dataset)
		}
		path := filepath.Join(master, fmt.Sprintf("GxArchivedFile%03d.dat", a.Archive))
		path, err = filepath.Abs(path)
		if err != nil {
			return report, err
		}
		k := a.Dataset + "/" + fmt.Sprint(a.Archive)
		if archives[k] == nil {
			archives[k] = &NativeProvenPCArchive{Dataset: a.Dataset, Archive: a.Archive, Source: path}
		}
		if archives[k].Source != path {
			return report, fmt.Errorf("source path collision for %s", id)
		}
		archives[k].Families = append(archives[k].Families, id)
	}
	keys := make([]string, 0, len(archives))
	for k := range archives {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	outDir := ""
	if !opt.ValidateOnly {
		outDir, err = filepath.Abs(opt.OutputDir)
		if err != nil {
			return report, err
		}
		bin, er := filepath.Abs(opt.RendererBinary)
		if er != nil {
			return report, er
		}
		fi, er := os.Lstat(bin)
		if er != nil || !fi.Mode().IsRegular() || fi.Mode()&0111 == 0 {
			return report, fmt.Errorf("renderer binary is not an executable regular file: %s", bin)
		}
		opt.RendererBinary = bin
	}
	for _, k := range keys {
		a := archives[k]
		pristineBytes, er := nativeProvenReadRegular(a.Source)
		if er != nil {
			return report, fmt.Errorf("%s pristine source missing/invalid: %w", k, er)
		}
		data := pristineBytes
		pristineHash := sha256.Sum256(pristineBytes)
		if stagedMode {
			name := fmt.Sprintf("GxArchivedFile%03d.dat", a.Archive)
			pin, ok := pinned[a.Dataset+"/"+name]
			if !ok || int64(len(pristineBytes)) != pin.Bytes || !strings.EqualFold(hex.EncodeToString(pristineHash[:]), pin.SHA256) {
				return report, fmt.Errorf("%s pristine whole DAT size/SHA does not match canonical manifest", k)
			}
			a.PristineSource = a.Source
			a.PristineSHA256 = hex.EncodeToString(pristineHash[:])
			stagedPath, err := filepath.Abs(filepath.Join(stagedMasters[a.Dataset], name))
			if err != nil {
				return report, err
			}
			if stagedPath == a.PristineSource {
				return report, fmt.Errorf("%s staged path aliases pristine source", k)
			}
			stagedBytes, err := nativeProvenReadRegular(stagedPath)
			if err != nil {
				return report, fmt.Errorf("%s text-staged archive absent/invalid: %w", k, err)
			}
			targets := map[int]map[string]string{}
			for _, id := range a.Families {
				f := byID[id]
				for _, member := range f.Members {
					if targets[member.Index] == nil {
						targets[member.Index] = map[string]string{}
					}
					if prev, exists := targets[member.Index][member.Inner]; exists && prev != member.SHA {
						return report, fmt.Errorf("%s source member identity conflict", id)
					}
					targets[member.Index][member.Inner] = member.SHA
				}
			}
			if err = checkTextStagedGraphicsBase(pristineBytes, stagedBytes, targets); err != nil {
				return report, fmt.Errorf("%s invalid staged graphics base: %w", k, err)
			}
			a.Source = stagedPath
			data = stagedBytes
		}
		h := sha256.Sum256(data)
		a.SHA256 = hex.EncodeToString(h[:])
		gd, er := mskit.ParseGDAT(data)
		if er != nil {
			return report, fmt.Errorf("%s invalid full GDAT: %w", k, er)
		}
		for _, id := range a.Families {
			f := byID[id]
			for _, member := range f.Members {
				if member.Index < 0 || member.Index >= len(gd.Entries) {
					return report, fmt.Errorf("%s index %d missing in original full DAT", id, member.Index)
				}
				tid, _, er := mskit.ExtractGraphicsTID(data, member.Index, member.Inner)
				if er != nil {
					return report, fmt.Errorf("%s original TID not found: %w", id, er)
				}
				hash := sha256.Sum256(tid)
				if !strings.EqualFold(hex.EncodeToString(hash[:]), member.SHA) {
					return report, fmt.Errorf("%s stale/different original source TID SHA", id)
				}
			}
		}
		if !opt.ValidateOnly {
			a.Output = filepath.Join(outDir, a.Dataset, "masterWindows", fmt.Sprintf("GxArchivedFile%03d.dat", a.Archive))
			if abs, er := filepath.Abs(a.Output); er != nil {
				return report, er
			} else if abs == a.Source {
				return report, fmt.Errorf("refuse in-place write %s", a.Source)
			}
			if _, er := os.Lstat(a.Output); er == nil {
				return report, fmt.Errorf("output exists, no clobber: %s", a.Output)
			} else if !os.IsNotExist(er) {
				return report, er
			}
		}
		report.Archives = append(report.Archives, *a)
		report.Families += len(a.Families)
	}
	if report.Families != len(families) || len(report.Archives) != 2 {
		return report, fmt.Errorf("unexpected native family grouping %d/%d", report.Families, len(report.Archives))
	}
	if opt.ValidateOnly {
		if stagedMode {
			report.Status = "PASS_READ_ONLY_STAGED_GRAPHICS_BASE"
		} else {
			report.Status = "PASS_READ_ONLY_ORIGINAL_SOURCE"
		}
		return report, nil
	}
	// Prevent writing into any pristine master input directory, including when
	// paths are presented with different spelling.
	for _, a := range report.Archives {
		protected := []string{a.Source}
		if a.PristineSource != "" {
			protected = append(protected, a.PristineSource)
		}
		for _, src := range protected {
			parent := filepath.Dir(filepath.Dir(src))
			rel, er := filepath.Rel(parent, outDir)
			if er == nil && (rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != "..")) {
				return report, errors.New("output directory is inside a source tree")
			}
		}
	}
	// Render inside a disposable staging directory, not at the final paths.
	// The *Go maintainer* independently checks all original and rebuilt archive
	// members before atomically publishing any output.
	if err = os.MkdirAll(outDir, 0755); err != nil {
		return report, err
	}
	staging, err := os.MkdirTemp(outDir, ".ms-proven-stage-*")
	if err != nil {
		return report, err
	}
	defer os.RemoveAll(staging)
	payload := nativeProvenRendererManifest{Version: 1, ProjectRoot: root, EnableDeferredNameBanks: opt.IncludeDeferredNameBanks}
	for i, a := range report.Archives {
		job := struct {
			Source       string   `json:"source"`
			SourceSHA256 string   `json:"source_sha256"`
			Output       string   `json:"output"`
			Families     []string `json:"families"`
			Quality      int      `json:"quality"`
			MaxAlpha     int      `json:"max_alpha"`
			MaxVisible   int      `json:"max_visible"`
		}{a.Source, a.SHA256, filepath.Join(staging, fmt.Sprintf("candidate_%02d.dat", i)), a.Families, 6, 64, 64}
		payload.Jobs = append(payload.Jobs, job)
	}
	manifestBytes, err := json.Marshal(payload)
	if err != nil {
		return report, err
	}
	manifestPath := filepath.Join(staging, "manifest.json")
	if err = os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		return report, err
	}
	cmd := exec.Command(opt.RendererBinary, "--family-batch-manifest", manifestPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return report, fmt.Errorf("native bcn-tid-go candidate refused: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var child struct {
		Status string `json:"status"`
		Assets []struct {
			Family string `json:"family"`
		} `json:"assets"`
		GDAT struct {
			Jobs []struct {
				Source       string            `json:"source"`
				Output       string            `json:"output"`
				SourceSHA256 string            `json:"source_sha256"`
				OutputSHA256 string            `json:"output_sha256"`
				TIDs         []json.RawMessage `json:"tids"`
			} `json:"jobs"`
		} `json:"gdat"`
	}
	if err = json.Unmarshal(stdout.Bytes(), &child); err != nil {
		return report, fmt.Errorf("renderer report not JSON: %w", err)
	}
	if !strings.HasPrefix(child.Status, "PASS_") || len(child.Assets) != expectedTIDs || len(child.GDAT.Jobs) != len(report.Archives) {
		return report, fmt.Errorf("renderer result incomplete: %s %d/%d", child.Status, len(child.Assets), len(child.GDAT.Jobs))
	}
	familyCounts := map[string]int{}
	for _, a := range child.Assets {
		familyCounts[a.Family]++
	}
	for _, id := range families {
		if familyCounts[id] != len(byID[id].Members) {
			return report, fmt.Errorf("renderer omitted/duplicated family members %s", id)
		}
	}
	for i, expected := range report.Archives {
		job := child.GDAT.Jobs[i]
		if job.Source != expected.Source || job.Output != payload.Jobs[i].Output || job.SourceSHA256 != expected.SHA256 || len(job.TIDs) != func() int {
			n := 0
			for _, family := range expected.Families {
				n += len(byID[family].Members)
			}
			return n
		}() {
			return report, fmt.Errorf("renderer result scope mismatch in archive %s", expected.Dataset)
		}
		source, er := nativeProvenReadRegular(expected.Source)
		if er != nil {
			return report, er
		}
		candidate, er := nativeProvenReadRegular(job.Output)
		if er != nil {
			return report, fmt.Errorf("renderer output missing: %w", er)
		}
		h := sha256.Sum256(candidate)
		if hex.EncodeToString(h[:]) != job.OutputSHA256 {
			return report, fmt.Errorf("renderer output archive SHA mismatch: %s", expected.Dataset)
		}
		g0, er := mskit.ParseGDAT(source)
		if er != nil {
			return report, er
		}
		g1, er := mskit.ParseGDAT(candidate)
		if er != nil {
			return report, er
		}
		if len(g0.Entries) != len(g1.Entries) {
			return report, fmt.Errorf("GDAT entry count drift: %s", expected.Dataset)
		}
		touched := map[int]bool{}
		for _, id := range expected.Families {
			asset := byID[id]
			for _, member := range asset.Members {
				oldTID, _, er := mskit.ExtractGraphicsTID(source, member.Index, member.Inner)
				if er != nil {
					return report, er
				}
				newTID, _, er := mskit.ExtractGraphicsTID(candidate, member.Index, member.Inner)
				if er != nil {
					return report, er
				}
				if bytes.Equal(oldTID, newTID) {
					return report, fmt.Errorf("no rendered TID change for %s / %s", id, member.Inner)
				}
				if len(oldTID) != len(newTID) || !bytes.Equal(oldTID[:min(len(oldTID), 128)], newTID[:min(len(newTID), 128)]) {
					return report, fmt.Errorf("TID size/header changed for %s / %s", id, member.Inner)
				}
				touched[member.Index] = true
			}
		}
		for n := range g0.Entries {
			if touched[n] {
				if bytes.Equal(g0.Entries[n], g1.Entries[n]) {
					return report, fmt.Errorf("missing edited GDAT entry %s:%d", expected.Dataset, n)
				}
			} else if !bytes.Equal(g0.Entries[n], g1.Entries[n]) {
				return report, fmt.Errorf("unrelated GDAT entry modified %s:%d", expected.Dataset, n)
			}
		}
	}
	// Staging is independently accepted. Final-byte no-clobber publication
	// uses hard links; any returned error rolls back only our new links.
	published := []string{}
	var publishErr error
	for i, a := range report.Archives {
		if publishErr = os.MkdirAll(filepath.Dir(a.Output), 0755); publishErr != nil {
			break
		}
		publishErr = os.Link(payload.Jobs[i].Output, a.Output)
		if publishErr != nil {
			break
		}
		published = append(published, a.Output)
	}
	if publishErr != nil {
		for i := len(published) - 1; i >= 0; i-- {
			_ = os.Remove(published[i])
		}
		return report, fmt.Errorf("candidate publication failed, rolled back: %w", publishErr)
	}
	report.RendererReport = json.RawMessage(bytes.TrimSpace(stdout.Bytes()))
	if stagedMode {
		report.Status = "PASS_STAGED_CANDIDATE_NOT_TEXT_OR_GAME_RUNTIME"
	} else {
		report.Status = "PASS_CANDIDATE_NOT_GAME_RUNTIME"
	}
	return report, nil
}

func WriteNativeProvenPCGraphicsReport(w io.Writer, r NativeProvenPCGraphicsReport) error {
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(r)
}
