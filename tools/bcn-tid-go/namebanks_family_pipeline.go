package main

// Three source-locked name-bank compositors to native RGBA -> changed-block
// BC7/BC3 -> one GDAT transaction. Research-only until all full-font
// outputs pass the source-specific guard and owner pristine DAT is available.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maryskelter-unburger/internal/mskit"
	"os"
	"path/filepath"
	"strings"
)

type NameFamilyJob struct {
	Source       string   `json:"source"`
	SourceSHA256 string   `json:"source_sha256"`
	Output       string   `json:"output"`
	Families     []string `json:"families"`
}
type NameFamilyManifest struct {
	Version     int             `json:"version"`
	ProjectRoot string          `json:"project_root"`
	Jobs        []NameFamilyJob `json:"jobs"`
}
type nameFamilyIdentity struct {
	index              int
	inner, sha, format string
	width, height      int
}

var nameFamilyIdentities = map[string]nameFamilyIdentity{
	"MS2_RESULT_NAMES":         {1333, "result_2.tid", resultSHA, "BC7", 2048, 2048},
	"MS2_BATTLE_NAMES":         {1331, "battle_2.tid", battleSHA, "BC7", 2048, 4096},
	"MS2_CAMP_CHARACTER_NAMES": {1431, "Camp_common_CHARA.tid", campGoldSourceSHA, "DXT5", 1024, 1024},
}

func parseNameFamilyManifest(raw []byte) (NameFamilyManifest, error) {
	var m NameFamilyManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, err
	}
	var tail any
	if err := dec.Decode(&tail); err != io.EOF {
		return m, errors.New("trailing JSON in name-bank manifest")
	}
	if m.Version != 1 || strings.TrimSpace(m.ProjectRoot) == "" || len(m.Jobs) == 0 || len(m.Jobs) > 16 {
		return m, errors.New("expected version 1, project_root and 1..16 jobs")
	}
	for j, job := range m.Jobs {
		if job.Source == "" || job.Output == "" || len(job.Families) == 0 || len(job.Families) > 3 || len(job.SourceSHA256) != 64 {
			return m, fmt.Errorf("job %d family count/source hash", j)
		}
		seen := map[string]bool{}
		for _, id := range job.Families {
			if _, ok := nameFamilyIdentities[id]; !ok || seen[id] {
				return m, fmt.Errorf("job %d unsupported/duplicate family %q", j, id)
			}
			seen[id] = true
		}
	}
	return m, nil
}
func renderPinnedFamily(root, id string, source []byte) ([]byte, error) {
	switch id {
	case "MS2_RESULT_NAMES":
		return renderResultAtlas(root, source, resultAlignment{ExtrusionDY: -4, ShadowDY: -4, ExtrusionDX: 1, ShadowDX: 1, OutlineRadiusHi: 3, ExtrusionRadius: 2, FaceDY: 8, ExtrusionDepth: 3, HorizontalWiden: 1.12, NeutralShear: true, FaceGradientBBox: true, AmbientBlackAlpha: 126, AmbientBlackDX: 4, AmbientBlackDY: 7, AmbientBlackBlur: 2.1})
	case "MS2_BATTLE_NAMES":
		summary, out, err := battleRender(root, "", source)
		if err != nil {
			return nil, err
		}
		if !summary.AllRowsChanged || !summary.NoChangedOutside15Rows || len(summary.NoAlphaCrop) != 15 {
			return nil, errors.New("battle raster guard rejected")
		}
		for _, safe := range summary.NoAlphaCrop {
			if !safe {
				return nil, errors.New("battle text clipped")
			}
		}
		return out, nil
	case "MS2_CAMP_CHARACTER_NAMES":
		a, font, p, err := loadCamp(root)
		if err != nil {
			return nil, err
		}
		return renderCamp(source, font, a, p)
	}
	return nil, errors.New("unknown family")
}

// The renderer argument is injectable only for zero-font deterministic source
// transaction QA. Normal CLI entrypoints use renderPinnedFamily above.
func runNameFamilyBatch(manifestPath string, renderer func(root, id string, source []byte) ([]byte, error), beforePublish func(int) error) (GDATPNGResponse, error) {
	var empty GDATPNGResponse
	abs, err := filepath.Abs(manifestPath)
	if err != nil {
		return empty, err
	}
	blob, err := os.ReadFile(abs)
	if err != nil {
		return empty, err
	}
	config, err := parseNameFamilyManifest(blob)
	if err != nil {
		return empty, err
	}
	parent := filepath.Dir(abs)
	root, err := checkedPath(parent, config.ProjectRoot)
	if err != nil {
		return empty, err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return empty, errors.New("project_root is not an accessible directory")
	}
	// Validate ALL batch paths before the first font read or expensive raster.
	// This also rejects dangling destination symlinks and cross-job aliases.
	preflight := GDATRGBAManifest{Version: 1}
	for _, job := range config.Jobs {
		preflight.Jobs = append(preflight.Jobs, GDATRGBAJob{
			Source: job.Source, SourceSHA256: job.SourceSHA256, Output: job.Output,
			Edits: []GDATRGBAEdit{{GDATIndex: 0, Inner: "preflight.tid", SourceTIDSHA256: strings.Repeat("0", 64), TargetRGBA: job.Source, TargetRGBASHA256: strings.Repeat("0", 64)}},
		})
	}
	if _, err := preflightRGBABatchPaths(parent, preflight); err != nil {
		return empty, fmt.Errorf("name-bank preflight: %w", err)
	}
	tmp, err := os.MkdirTemp("", "ms-name-banks-go-*")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(tmp) // only this tool's private, unpredictable, self-created directory
	outManifest := GDATRGBAManifest{Version: 1}
	for j, job := range config.Jobs {
		input, err := checkedPath(parent, job.Source)
		if err != nil {
			return empty, err
		}
		output, err := checkedPath(parent, job.Output)
		if err != nil {
			return empty, err
		}
		raw, err := os.ReadFile(input)
		if err != nil {
			return empty, err
		}
		if !checkedHash(raw, job.SourceSHA256) {
			return empty, fmt.Errorf("job %d source GDAT SHA drift", j)
		}
		if _, err = mskit.ParseGDAT(raw); err != nil {
			return empty, err
		}
		encJob := GDATRGBAJob{Source: input, SourceSHA256: job.SourceSHA256, Output: output}
		for _, id := range job.Families {
			spec := nameFamilyIdentities[id]
			original, _, e := mskit.ExtractGraphicsTID(raw, spec.index, spec.inner)
			if e != nil {
				return empty, fmt.Errorf("%s source not in GDAT: %w", id, e)
			}
			if !checkedHash(original, spec.sha) {
				return empty, fmt.Errorf("%s source TID SHA drift", id)
			}
			meta, e := mskit.ParseTIDMeta(original)
			if e != nil {
				return empty, e
			}
			if meta.Width != spec.width || meta.Height != spec.height || meta.Format != spec.format {
				return empty, fmt.Errorf("%s wrong source format/size", id)
			}
			target, e := renderer(root, id, original)
			if e != nil {
				return empty, fmt.Errorf("%s render: %w", id, e)
			}
			if len(target) != meta.Width*meta.Height*4 {
				return empty, fmt.Errorf("%s wrong RGBA target length", id)
			}
			targetPath := filepath.Join(tmp, fmt.Sprintf("job%d-%s.rgba", j, id))
			if e = os.WriteFile(targetPath, target, 0600); e != nil {
				return empty, e
			}
			strict := 64
			encJob.Edits = append(encJob.Edits, GDATRGBAEdit{GDATIndex: spec.index, Inner: spec.inner, SourceTIDSHA256: spec.sha, TargetRGBA: targetPath, TargetRGBASHA256: digest(target), Quality: 6, MaxAlpha: &strict, MaxVisible: &strict})
		}
		outManifest.Jobs = append(outManifest.Jobs, encJob)
	}
	encoded, e := json.Marshal(outManifest)
	if e != nil {
		return empty, e
	}
	tempManifest := filepath.Join(tmp, "rgba-manifest.json")
	if e = os.WriteFile(tempManifest, encoded, 0600); e != nil {
		return empty, e
	}
	return RunGDATRGBABatchWithHook(tempManifest, beforePublish)
}
func RunNameFamilyBatch(path string) (GDATPNGResponse, error) {
	return runNameFamilyBatch(path, renderPinnedFamily, nil)
}
