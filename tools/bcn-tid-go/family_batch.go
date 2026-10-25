package main

// legacy: source-manifest-driven Go family batch for all proven renderers.
// Pixels travel directly as unassociated RGBA to the native GDAT encoder.
// No intermediate PNG color-model conversion or hidden-RGB loss.
// All image work remains offline,
// based on pinned fonts, immutable source TIDs and the canonical graphics
// manifest. The underlying GDAT batch commits all outputs transactionally.
// Unsupported renderers must fail closed: no approximate font substitutions.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"maryskelter-unburger/internal/mskit"
)

type familyBatchSpec struct {
	// Explicit opt-in for owner-reviewed namebanks not yet counted as
	// production-proven. Camp is never allowed through this route.
	EnableDeferredNameBanks bool             `json:"enable_deferred_name_banks,omitempty"`
	Version                 int              `json:"version"`
	ProjectRoot             string           `json:"project_root"`
	Jobs                    []familyBatchJob `json:"jobs"`
}
type familyBatchJob struct {
	Source       string   `json:"source"`
	SourceSHA256 string   `json:"source_sha256"`
	Output       string   `json:"output"`
	Families     []string `json:"families"`
	Quality      int      `json:"quality"`
	MaxAlpha     int      `json:"max_alpha"`
	MaxVisible   int      `json:"max_visible"`
}
type familyBatchAssetReport struct {
	Family             string `json:"family"`
	Archive            int    `json:"archive"`
	GDATIndex          int    `json:"gdat_index"`
	Inner              string `json:"inner"`
	Rows               int    `json:"rows"`
	SourceTIDSHA256    string `json:"source_tid_sha256"`
	FontSHA256         string `json:"font_sha256"`
	RenderedRGBASHA256 string `json:"rendered_rgba_sha256"`
}
type familyBatchReport struct {
	Status string                   `json:"status"`
	Assets []familyBatchAssetReport `json:"assets"`
	GDAT   GDATPNGResponse          `json:"gdat"`
}

// stageFamilyRGBA keeps straight-alpha bytes untouched, even when A=0.
// The output is a temporary private intermediate, never a public asset.
func stageFamilyRGBA(path string, rgba []byte, width, height int) (string, error) {
	if width <= 0 || height <= 0 || len(rgba) != width*height*4 {
		return "", errors.New("renderer produced wrong straight-RGBA dimensions")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err = f.Write(rgba); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	return digest(rgba), nil
}

type provenRenderer func(string, []byte) (familyAsset, []byte, []byte, error)

func renderProvenFamily(root string, fam string, tid []byte) (familyAsset, []byte, []byte, error) {
	switch fam {
	case pink24ID, pinkMiddleID, pinkSkillID:
		a, font, cfg, preset, err := loadPinkAtlas(root, fam)
		if err != nil {
			return a, nil, nil, err
		}
		rgba, err := renderPinkAtlas(tid, font, cfg, preset)
		return a, font, rgba, err
	case campTop2ID:
		a, font, p, err := loadCampTop2(root)
		if err != nil {
			return a, nil, nil, err
		}
		if digest(tid) != campTop2TIDSHA {
			return a, nil, nil, errors.New("stale Camp Top2 source TID")
		}
		rgba, err := renderCampTarget(tid, font, a, p)
		return a, font, rgba, err
	case ms2SelectID, msfSelectID, msfBonusID:
		a, font, preset, err := readWhiteFamily(root, fam)
		if err != nil {
			return a, nil, nil, err
		}
		rgba, err := renderWhiteFamily(tid, font, a, preset)
		return a, font, rgba, err
	case jobWhiteID:
		a, font, p, err := readJobWhite(root)
		if err != nil {
			return a, nil, nil, err
		}
		if digest(tid) != jobWhiteSHA {
			return a, nil, nil, errors.New("stale legacy original job menu TID")
		}
		rgba, err := renderJobWhite(tid, font, a, p)
		return a, font, rgba, err
	case bonusWhiteID:
		a, font, p, err := readBonusWhite(root)
		if err != nil {
			return a, nil, nil, err
		}
		if digest(tid) != bonusWhiteSHA {
			return a, nil, nil, errors.New("stale legacy original white TID")
		}
		rgba, err := renderBonusWhite(tid, font, a, p)
		return a, font, rgba, err
	case roomListID, myRoomID:
		a, font, p, err := loadRoom(root, fam)
		if err != nil {
			return a.familyAsset, nil, nil, err
		}
		rgba, err := renderRoom(tid, font, a, p)
		return a.familyAsset, font, rgba, err
	case finaleGridID:
		a, err := loadFinaleGrid(root)
		if err != nil {
			return familyAsset{}, nil, nil, err
		}
		rgba, err := renderFinaleGrid(tid, a)
		return familyAsset{ID: a.ID, Archive: a.Archive, Index: a.Index, Inner: a.Inner}, nil, rgba, err
	case renGrayID:
		a, font, preset, err := loadRenGray(root)
		if err != nil {
			return a, nil, nil, err
		}
		rgba, err := renderRenGray(tid, font, a, preset)
		return a, font, rgba, err
	case finaleCampID:
		a, font, cfg, err := loadFinaleCamp(root)
		if err != nil {
			return a, nil, nil, err
		}
		rgba, err := renderFinaleCamp(tid, font, a, cfg)
		return a, font, rgba, err
	case roomPinkID:
		a, font, p, err := loadRoomPink(root)
		if err != nil {
			return a, nil, nil, err
		}
		rgba, err := renderRoomPink(tid, font, a, p)
		return a, font, rgba, err
	case legacyID:
		a, font, p, err := loadLegacyFamily(root)
		if err != nil {
			return a, nil, nil, err
		}
		if digest(tid) != legacySHA {
			return a, nil, nil, errors.New("stale Camp Legacy source TID")
		}
		rgba, err := renderLegacyTarget(tid, font, a, p)
		return a, font, rgba, err
	default:
		return familyAsset{}, nil, nil, fmt.Errorf("family %q has no source-verified Go renderer; refuse fallback", fam)
	}
}

func parseFamilyBatch(data []byte) (familyBatchSpec, error) {
	var spec familyBatchSpec
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return spec, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return spec, errors.New("trailing or malformed family batch manifest")
	}
	if spec.Version != 1 || spec.ProjectRoot == "" || len(spec.Jobs) < 1 || len(spec.Jobs) > 32 {
		return spec, errors.New("requires version=1, project_root, jobs=1..32")
	}
	seenOut := make(map[string]bool)
	for j, job := range spec.Jobs {
		if job.Source == "" || job.Output == "" || len(job.SourceSHA256) != 64 || job.Quality != 6 || job.MaxAlpha < 0 || job.MaxAlpha > 255 || job.MaxVisible < 0 || job.MaxVisible > 255 || len(job.Families) < 1 || len(job.Families) > 31 {
			return spec, fmt.Errorf("job %d invalid paths, family count or Q6/quality guards", j)
		}
		if seenOut[job.Output] {
			return spec, errors.New("duplicate raw output path")
		}
		seenOut[job.Output] = true
		seen := make(map[string]bool)
		for _, id := range job.Families {
			if !(spec.EnableDeferredNameBanks && isDeferredNameBank(id)) && id != pink24ID && id != pinkMiddleID && id != pinkSkillID && id != finaleGridID && id != dlcJobLabelsID && id != campTop2ID && id != legacyID && id != bonusWhiteID && id != jobWhiteID && id != ms2SelectID && id != msfSelectID && id != msfBonusID && id != roomPinkID && id != finaleCampID && id != renGrayID && id != roomListID && id != myRoomID {
				return spec, fmt.Errorf("renderer for family %q not proven", id)
			}
			if isDeferredNameBank(id) && (job.MaxAlpha != 64 || job.MaxVisible != 64) {
				return spec, fmt.Errorf("deferred namebank %s requires strict 64/64 BC7 guards", id)
			}
			if seen[id] {
				return spec, fmt.Errorf("family %s repeated", id)
			}
			seen[id] = true
		}
	}
	return spec, nil
}

func RunSourceFamilyBatchWithHook(path string, beforePublish func(int) error) (familyBatchReport, error) {
	return runSourceFamilyBatchWithDeferred(path, beforePublish, renderDeferredNameBank)
}

// The injectable deferred renderer is used exclusively by source-locked offline
// integration QA. Public callers always use the pinned unchanged owner styles.
func runSourceFamilyBatchWithDeferred(path string, beforePublish func(int) error, deferred func(string, string, []byte) (deferredNameBankSource, string, []byte, error)) (familyBatchReport, error) {
	var result familyBatchReport
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	spec, err := parseFamilyBatch(raw)
	if err != nil {
		return result, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return result, err
	}
	base := filepath.Dir(abs)
	root, err := checkedPath(base, spec.ProjectRoot)
	if err != nil {
		return result, err
	}
	// Fail before any potentially expensive font rendering or BCn encoding.
	// A later conflicting destination must reject the *whole* batch, rather
	// than allowing earlier jobs to consume minutes of CPU before refusal.
	type ioPaths struct{ source, dest string }
	paths := make([]ioPaths, len(spec.Jobs))
	allSources := make(map[string]bool, len(spec.Jobs))
	allOutputs := make(map[string]bool, len(spec.Jobs))
	for j, job := range spec.Jobs {
		src, e := checkedPath(base, job.Source)
		if e != nil {
			return result, e
		}
		dst, e := checkedPath(base, job.Output)
		if e != nil {
			return result, e
		}
		if src == dst || allSources[src] || allOutputs[dst] {
			return result, fmt.Errorf("job %d duplicate or in-place source/output path", j)
		}
		allSources[src], allOutputs[dst] = true, true
		paths[j] = ioPaths{src, dst}
	}
	for j, p := range paths {
		if allSources[p.dest] {
			return result, fmt.Errorf("job %d destination aliases another input source", j)
		}
		if _, e := os.Lstat(p.dest); e == nil {
			return result, fmt.Errorf("job %d output already exists, no clobber: %s", j, p.dest)
		} else if !os.IsNotExist(e) {
			return result, e
		}
	}
	tmp, err := os.MkdirTemp("", "ms-family-batch-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(tmp)
	native := GDATRGBAManifest{Version: 1}
	assets := []familyBatchAssetReport{}
	for j, job := range spec.Jobs {
		src, dst := paths[j].source, paths[j].dest
		data, err := os.ReadFile(src)
		if err != nil {
			return result, err
		}
		if !checkedHash(data, job.SourceSHA256) {
			return result, fmt.Errorf("job %d source GDAT SHA mismatch", j)
		}
		gj := GDATRGBAJob{Source: src, SourceSHA256: job.SourceSHA256, Output: dst}
		for k, family := range job.Families {
			if isDeferredNameBank(family) {
				asset, fontSHA, rgba, e := deferred(root, family, data)
				if e != nil {
					return result, fmt.Errorf("deferred namebank %s: %w", family, e)
				}
				meta, e := mskit.ParseTIDMeta(asset.source)
				if e != nil {
					return result, e
				}
				outPath := filepath.Join(tmp, fmt.Sprintf("job%02d_namebank%02d.rgba", j, k))
				rgbaSHA, e := stageFamilyRGBA(outPath, rgba, meta.Width, meta.Height)
				if e != nil {
					return result, e
				}
				alpha, visible := 64, 64
				gj.Edits = append(gj.Edits, GDATRGBAEdit{GDATIndex: asset.identity.index, Inner: asset.identity.inner, SourceTIDSHA256: asset.identity.sha, TargetRGBA: outPath, TargetRGBASHA256: rgbaSHA, Quality: 6, MaxAlpha: &alpha, MaxVisible: &visible})
				assets = append(assets, familyBatchAssetReport{Family: family, Archive: 55, GDATIndex: asset.identity.index, Inner: asset.identity.inner, Rows: asset.rows, SourceTIDSHA256: asset.identity.sha, FontSHA256: fontSHA, RenderedRGBASHA256: rgbaSHA})
				continue
			}
			// An atlas family can contain multiple independently pinned TIDs.
			// Stage every DLC member in the same GDAT transaction: rejecting one
			// member must prevent publication of all six results.
			if family == dlcJobLabelsID {
				group, font, e := loadDLCJobLabels(root)
				if e != nil {
					return result, e
				}
				if len(group.Targets) != 6 {
					return result, errors.New("DLC family incomplete")
				}
				for member, target := range group.Targets {
					tid, _, e := mskit.ExtractGraphicsTID(data, target.Index, target.Inner)
					if e != nil {
						return result, fmt.Errorf("DLC member %d source: %w", member, e)
					}
					rgba, e := renderDLCJobLabel(tid, font, target)
					if e != nil {
						return result, fmt.Errorf("DLC member %d render: %w", member, e)
					}
					meta, e := mskit.ParseTIDMeta(tid)
					if e != nil {
						return result, e
					}
					if len(rgba) != meta.Width*meta.Height*4 {
						return result, errors.New("DLC wrong RGBA size")
					}
					// Write exactly the renderer's non-premultiplied RGBA, including
					// hidden RGB under alpha=0. No PNG normalization is permitted.
					path := filepath.Join(tmp, fmt.Sprintf("job%02d_group%02d_%02d.rgba", j, k, member))
					ph, e := stageFamilyRGBA(path, rgba, meta.Width, meta.Height)
					if e != nil {
						return result, e
					}
					alpha, visible := job.MaxAlpha, job.MaxVisible
					gj.Edits = append(gj.Edits, GDATRGBAEdit{GDATIndex: target.Index, Inner: target.Inner, SourceTIDSHA256: digest(tid), TargetRGBA: path, TargetRGBASHA256: ph, Quality: 6, MaxAlpha: &alpha, MaxVisible: &visible})
					assets = append(assets, familyBatchAssetReport{Family: family, Archive: group.Archive, GDATIndex: target.Index, Inner: target.Inner, Rows: 1, SourceTIDSHA256: digest(tid), FontSHA256: digest(font), RenderedRGBASHA256: ph})
				}
				continue
			}
			// Resolve authoritative target first. Never guess archive entries.
			var a familyAsset
			var font, rgba []byte
			if family == pink24ID || family == pinkMiddleID || family == pinkSkillID {
				a, font, _, _, err = loadPinkAtlas(root, family)
			} else if family == campTop2ID {
				a, font, _, err = loadCampTop2(root)
			} else if family == bonusWhiteID {
				a, font, _, err = readBonusWhite(root)
			} else if family == ms2SelectID || family == msfSelectID || family == msfBonusID {
				a, font, _, err = readWhiteFamily(root, family)
			} else if family == jobWhiteID {
				a, font, _, err = readJobWhite(root)
			} else if family == roomListID || family == myRoomID {
				v, f, _, e := loadRoom(root, family)
				a, font, err = v.familyAsset, f, e
			} else if family == finaleGridID {
				v, e := loadFinaleGrid(root)
				a, err = familyAsset{ID: v.ID, Archive: v.Archive, Index: v.Index, Inner: v.Inner}, e
			} else if family == renGrayID {
				a, font, _, err = loadRenGray(root)
			} else if family == finaleCampID {
				a, font, _, err = loadFinaleCamp(root)
			} else if family == roomPinkID {
				a, font, _, err = loadRoomPink(root)
			} else {
				a, font, _, err = loadLegacyFamily(root)
			}
			if err != nil {
				return result, err
			}
			tid, _, err := mskit.ExtractGraphicsTID(data, a.Index, a.Inner)
			if err != nil {
				return result, fmt.Errorf("family %s not present in source archive: %w", family, err)
			}
			a, font, rgba, err = renderProvenFamily(root, family, tid)
			if err != nil {
				return result, err
			}
			meta, err := mskit.ParseTIDMeta(tid)
			if err != nil {
				return result, err
			}
			if len(rgba) != meta.Width*meta.Height*4 {
				return result, errors.New("renderer produced wrong RGBA size")
			}
			// The family renderer already returns the authoritative straight
			// RGBA target. Preserve all four channels byte for byte.
			renderedPath := filepath.Join(tmp, fmt.Sprintf("job%02d_family%02d.rgba", j, k))
			rgbaHash, err := stageFamilyRGBA(renderedPath, rgba, meta.Width, meta.Height)
			if err != nil {
				return result, err
			}
			alpha, visible := job.MaxAlpha, job.MaxVisible
			if family == pink24ID || family == pinkMiddleID || family == pinkSkillID {
				// Owner-viewed exact targets, not a global encoder relaxation.
				// Alpha remains strictly bounded. All other families use the manifest guard.
				if alpha < 18 || visible != 64 {
					return result, errors.New("legacy reviewed-family expects default 64/64 job guard")
				}
				alpha, visible = 18, 120
			}
			gj.Edits = append(gj.Edits, GDATRGBAEdit{GDATIndex: a.Index, Inner: a.Inner, SourceTIDSHA256: digest(tid), TargetRGBA: renderedPath, TargetRGBASHA256: rgbaHash, Quality: 6, MaxAlpha: &alpha, MaxVisible: &visible})
			assets = append(assets, familyBatchAssetReport{Family: family, Archive: a.Archive, GDATIndex: a.Index, Inner: a.Inner, Rows: func() int {
				if family == myRoomID {
					return 1
				}
				if approval, ok := pinkApprovalFor(family); ok {
					return approval.labels
				}
				return len(a.Rows)
			}(), SourceTIDSHA256: digest(tid), FontSHA256: func() string {
				if len(font) == 0 {
					return ""
				}
				return digest(font)
			}(), RenderedRGBASHA256: rgbaHash})
		}
		native.Jobs = append(native.Jobs, gj)
	}
	payload, err := json.Marshal(native)
	if err != nil {
		return result, err
	}
	nativePath := filepath.Join(tmp, "pinned-native-rgba-gdat-batch.json")
	if err := os.WriteFile(nativePath, payload, 0600); err != nil {
		return result, err
	}
	report, err := RunGDATRGBABatchWithHook(nativePath, beforePublish)
	if err != nil {
		return result, err
	}
	return familyBatchReport{Status: "PASS__PINNED_GO_RGBA_FAMILY_BATCH_NO_GAME_WRITE", Assets: assets, GDAT: report}, nil
}
func RunSourceFamilyBatch(path string) (familyBatchReport, error) {
	return RunSourceFamilyBatchWithHook(path, nil)
}
