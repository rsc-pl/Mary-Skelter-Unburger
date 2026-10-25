package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// BuildAssetPlan is the canonical end-user planner. All non-algorithmic game
// payload comes from one authenticated .unburger-assets v3 container; pristine
// full DAT bundles and raw release staging are not runtime inputs.
func BuildAssetPlan(assets *ReleaseAssets, roots map[string]string) (Plan, error) {
	return BuildAssetPlanWithProgress(assets, roots, nil)
}

// BuildAssetPlanWithProgress is identical to BuildAssetPlan but reports
// deterministic scan progress to interactive callers. One unit is emitted for
// each selected executable, managed GDAT archive, and Finale movie candidate.
func BuildAssetPlanWithProgress(assets *ReleaseAssets, roots map[string]string, progress ProgressFunc) (Plan, error) {
	if assets == nil {
		return Plan{}, fmt.Errorf("release assets are required")
	}
	var plan Plan
	keys := make([]string, 0, len(roots))
	for key := range roots {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	totalWork := len(keys)
	for _, key := range keys {
		totalWork += len(assets.GDATArchives(key))
		if key == "finale" {
			if movies, err := assets.FinaleRemuxAssets(); err == nil {
				totalWork += len(movies)
			}
		}
	}
	doneWork := 0
	emitProgress(progress, ProgressEvent{Phase: "preflight", Detail: "Starting managed-file scan", Done: doneWork, Total: totalWork})

	for _, key := range keys {
		p, ok := Profiles[key]
		if !ok {
			return Plan{}, fmt.Errorf("unknown game profile %q", key)
		}
		st, err := InspectGame(roots[key], p)
		if err != nil {
			return Plan{}, err
		}
		if st.ExeState == "unknown" {
			return Plan{}, fmt.Errorf("%s executable is unsupported: %s", p.DisplayName, st.ExeSHA)
		}
		plan.Games = append(plan.Games, st)
		plan.Warnings = append(plan.Warnings, st.LegacyWarnings...)

		exePath := filepath.Join(st.Root, p.ExeName)
		exeBytes, err := os.ReadFile(exePath)
		if err != nil {
			return Plan{}, err
		}
		patched, _, err := PatchExecutable(exeBytes, p)
		if err != nil {
			return Plan{}, err
		}
		if st.ExeState != "final" {
			plan.Operations = append(plan.Operations, Operation{GameKey: key, RelativePath: p.ExeName, Action: "PATCH", Generated: patched, SourceSHA: sha256Bytes(patched)})
		}
		doneWork++
		emitProgress(progress, ProgressEvent{Phase: "preflight", Action: "EXE", GameKey: key, RelativePath: p.ExeName, Detail: "Executable state verified", Done: doneWork, Total: totalWork})

		for _, marker := range p.RequiredMarkers {
			if filepath.Base(marker) != marker {
				return Plan{}, fmt.Errorf("unsafe required marker %q", marker)
			}
			dst := filepath.Join(st.Root, marker)
			info, err := os.Stat(dst)
			switch {
			case err == nil && info.Mode().IsRegular():
			case err == nil:
				return Plan{}, fmt.Errorf("required marker is not a regular file: %s", dst)
			case os.IsNotExist(err):
				plan.Operations = append(plan.Operations, Operation{GameKey: key, RelativePath: marker, Action: "CREATE", Generated: []byte{}, SourceSHA: emptySHA256})
			default:
				return Plan{}, err
			}
		}

		patches := assets.GDATArchives(key)
		if len(patches) == 0 {
			return Plan{}, fmt.Errorf("release container has no GDAT patches for %s", p.DisplayName)
		}
		for i := range patches {
			patch := patches[i]
			dst := filepath.Join(st.Root, filepath.FromSlash(patch.RelativePath))
			needs, err := InspectGDATPatchState(dst, patch)
			if err != nil {
				return Plan{}, fmt.Errorf("%s structural preflight: %w", patch.RelativePath, err)
			}
			// Critical payload objects are authenticated before any install write.
			for _, e := range patch.ChangedEntries {
				logical := filepath.ToSlash(filepath.Join("gdat", filepath.FromSlash(e.BlobFile)))
				packed, err := assets.ReadObject(logical)
				if err != nil {
					return Plan{}, fmt.Errorf("preflight %s: %w", logical, err)
				}
				decoded, err := decodeGDATObject(packed, e.TargetSize)
				if err != nil || bytesSHA(decoded) != e.TargetSHA256 {
					return Plan{}, fmt.Errorf("preflight target mismatch %s entry %d", patch.RelativePath, e.Index)
				}
			}
			if needs {
				pCopy := patch
				plan.Operations = append(plan.Operations, Operation{GameKey: key, RelativePath: patch.RelativePath, Action: "PATCH-GDAT", GDATPatch: &pCopy})
			}
			doneWork++
			emitProgress(progress, ProgressEvent{Phase: "preflight", Action: "GDAT", GameKey: key, RelativePath: patch.RelativePath, Detail: "Managed archive verified", Done: doneWork, Total: totalWork})
		}

		if key == "finale" {
			movies, err := assets.FinaleRemuxAssets()
			if err != nil {
				plan.SoftFailures = append(plan.SoftFailures, OperationFailure{GameKey: key, RelativePath: "masterWindows/MOVIE_EN", Action: "REMUX", Error: err.Error()})
				continue
			}
			for _, a := range movies {
				func() {
					dst := filepath.Join(st.Root, filepath.FromSlash(a.RelativePath))
					current, err := SHA256File(dst)
					if err != nil {
						plan.SoftFailures = append(plan.SoftFailures, OperationFailure{GameKey: key, RelativePath: a.RelativePath, Action: "REMUX", Error: err.Error()})
						return
					}
					if current == a.TargetSHA256 {
						return
					}
					compatible := current == a.SourceSHA256
					if !compatible && a.SourceVideoSHA256 != "" {
						videoSHA, videoErr := FinaleMovieVideoSHA256(dst)
						if videoErr == nil && videoSHA == a.SourceVideoSHA256 {
							compatible = true
						} else if videoErr != nil {
							plan.SoftFailures = append(plan.SoftFailures, OperationFailure{GameKey: key, RelativePath: a.RelativePath, Action: "REMUX", Error: "cannot validate movie video identity: " + videoErr.Error()})
							return
						}
					}
					if !compatible {
						plan.SoftFailures = append(plan.SoftFailures, OperationFailure{GameKey: key, RelativePath: a.RelativePath, Action: "REMUX", Error: fmt.Sprintf("unsupported movie state %s", current)})
						return
					}
					plan.Operations = append(plan.Operations, Operation{GameKey: key, RelativePath: a.RelativePath, Action: "REMUX", BestEffort: true, PayloadObject: a.PayloadObject, PayloadSHA: a.PayloadSHA256, ExpectedBeforeSHA: current, SourceSHA: a.TargetSHA256})
				}()
				doneWork++
				emitProgress(progress, ProgressEvent{Phase: "preflight", Action: "MOVIE", GameKey: key, RelativePath: a.RelativePath, Detail: "Movie state verified", Done: doneWork, Total: totalWork})
			}
		}
	}
	return plan, nil
}
