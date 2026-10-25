package main

// Native RGBA -> changed-block BC7/DXT5 -> CL3/GDAT transaction.
// Exact byte-preserving RGBA avoids the premultiplied-alpha/hidden-RGB
// ambiguity of PNG round trips and matches the Go renderer output directly.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type GDATRGBAEdit struct {
	GDATIndex        int    `json:"gdat_index"`
	Inner            string `json:"inner"`
	SourceTIDSHA256  string `json:"source_tid_sha256"`
	TargetRGBA       string `json:"target_rgba"`
	TargetRGBASHA256 string `json:"target_rgba_sha256"`
	Quality          int    `json:"quality,omitempty"`
	MaxAlpha         *int   `json:"max_alpha,omitempty"`
	MaxVisible       *int   `json:"max_visible,omitempty"`
}
type GDATRGBAJob struct {
	Source       string         `json:"source"`
	SourceSHA256 string         `json:"source_sha256"`
	Output       string         `json:"output"`
	Edits        []GDATRGBAEdit `json:"edits"`
}
type GDATRGBAManifest struct {
	Version int           `json:"version"`
	Jobs    []GDATRGBAJob `json:"jobs"`
}

func parseRGBAManifest(raw []byte) (GDATRGBAManifest, error) {
	var m GDATRGBAManifest
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, err
	}
	var tail any
	if err := d.Decode(&tail); err != io.EOF {
		return m, errors.New("trailing JSON after manifest")
	}
	if m.Version != 1 || len(m.Jobs) < 1 || len(m.Jobs) > 32 {
		return m, errors.New("RGBA manifest requires version 1, jobs 1..32")
	}
	for j, job := range m.Jobs {
		if len(job.Edits) < 1 || len(job.Edits) > 128 {
			return m, fmt.Errorf("job %d edits outside 1..128", j)
		}
		seen := map[string]bool{}
		for i, e := range job.Edits {
			if e.GDATIndex < 0 || e.Inner == "" || strings.TrimSpace(e.Inner) != e.Inner || !strings.HasSuffix(strings.ToLower(e.Inner), ".tid") || e.TargetRGBA == "" || len(e.SourceTIDSHA256) != 64 || len(e.TargetRGBASHA256) != 64 {
				return m, fmt.Errorf("job %d edit %d invalid TID/RGBA identity", j, i)
			}
			key := fmt.Sprintf("%d:%s", e.GDATIndex, strings.ToLower(e.Inner))
			if seen[key] {
				return m, fmt.Errorf("duplicate target %s", key)
			}
			seen[key] = true
			if e.Quality < 0 || e.Quality > 10 {
				return m, errors.New("quality must be 0 or 1..10")
			}
			for _, v := range []*int{e.MaxAlpha, e.MaxVisible} {
				if v != nil && (*v < 0 || *v > 255) {
					return m, errors.New("quality limit outside 0..255")
				}
			}
		}
	}
	return m, nil
}

// preflightRGBABatchPaths rejects conflicts across the *entire* batch before
// the first potentially expensive render/encode. In particular, an output of
// job 0 may never alias a later input or the raw RGBA file of another job.
// A dangling output symlink is still an existing owner path and is refused.
func preflightRGBABatchPaths(base string, m GDATRGBAManifest) ([][2]string, error) {
	paths := make([][2]string, len(m.Jobs))
	inputs := map[string]bool{}
	outputs := map[string]bool{}
	for j, job := range m.Jobs {
		src, err := checkedPath(base, job.Source)
		if err != nil {
			return nil, err
		}
		dst, err := checkedPath(base, job.Output)
		if err != nil {
			return nil, err
		}
		if src == dst || inputs[src] || outputs[dst] {
			return nil, fmt.Errorf("job %d duplicated/aliased GDAT paths", j)
		}
		if _, err = os.Lstat(dst); err == nil {
			return nil, fmt.Errorf("job %d output exists: %s", j, dst)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		inputs[src], outputs[dst] = true, true
		paths[j] = [2]string{src, dst}
	}
	for j, job := range m.Jobs {
		if inputs[paths[j][1]] || outputs[paths[j][0]] {
			return nil, fmt.Errorf("job %d output aliases batch input", j)
		}
		for k, edit := range job.Edits {
			rgba, err := checkedPath(base, edit.TargetRGBA)
			if err != nil {
				return nil, err
			}
			if outputs[rgba] {
				return nil, fmt.Errorf("job %d edit %d RGBA file aliases batch output", j, k)
			}
		}
	}
	return paths, nil
}

// RunGDATRGBABatchWithHook enables a deterministic failure immediately before
// any publish. Every output is precomputed/staged; a failure after a partial
// hardlink commit rolls back all files created by this call. Source files and
// existing destinations cannot be overwritten by this API.
func RunGDATRGBABatchWithHook(manifestPath string, beforePublish func(int) error) (_ GDATPNGResponse, err error) {
	var response GDATPNGResponse
	path, err := filepath.Abs(manifestPath)
	if err != nil {
		return response, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return response, err
	}
	manifest, err := parseRGBAManifest(raw)
	if err != nil {
		return response, err
	}
	base := filepath.Dir(path)
	paths, err := preflightRGBABatchPaths(base, manifest)
	if err != nil {
		return response, err
	}
	staged := []stagedGDAT{}
	published := []string{}
	defer func() {
		for _, v := range staged {
			if v.tmp != "" {
				_ = os.Remove(v.tmp)
			}
		}
		if err != nil {
			for i := len(published) - 1; i >= 0; i-- {
				_ = os.Remove(published[i])
			}
		}
	}()
	for n, job := range manifest.Jobs {
		src, dst := paths[n][0], paths[n][1]
		original, e := os.ReadFile(src)
		if e != nil {
			return response, e
		}
		if !checkedHash(original, job.SourceSHA256) {
			return response, fmt.Errorf("job %d GDAT source SHA drift", n)
		}
		before, e := mskit.ParseGDAT(original)
		if e != nil {
			return response, e
		}
		updated := original
		touched := map[int]bool{}
		r := GDATPNGResult{Source: src, Output: dst, SourceSHA256: digest(original)}
		for k, edit := range job.Edits {
			pristine, _, e := mskit.ExtractGraphicsTID(original, edit.GDATIndex, edit.Inner)
			if e != nil {
				return response, fmt.Errorf("job %d edit %d source extraction: %w", n, k, e)
			}
			if !checkedHash(pristine, edit.SourceTIDSHA256) {
				return response, fmt.Errorf("job %d edit %d TID source SHA drift", n, k)
			}
			meta, e := mskit.ParseTIDMeta(pristine)
			if e != nil {
				return response, e
			}
			srcRGBA, e := checkedPath(base, edit.TargetRGBA)
			if e != nil {
				return response, e
			}
			if srcRGBA == dst {
				return response, errors.New("RGBA input aliases output")
			}
			data, e := os.ReadFile(srcRGBA)
			if e != nil {
				return response, e
			}
			if len(data) != meta.Width*meta.Height*4 || !checkedHash(data, edit.TargetRGBASHA256) {
				return response, fmt.Errorf("job %d edit %d RGBA size/SHA drift", n, k)
			}
			quality := edit.Quality
			if quality == 0 {
				quality = 6
			}
			a, v := 64, 64
			if edit.MaxAlpha != nil {
				a = *edit.MaxAlpha
			}
			if edit.MaxVisible != nil {
				v = *edit.MaxVisible
			}
			newTID, rep, e := EncodeChangedTID(pristine, data, quality, a, v)
			if e != nil {
				return response, fmt.Errorf("job %d edit %d codec: %w", n, k, e)
			}
			updated, _, e = mskit.PatchGraphicsTIDGoSafe(updated, edit.GDATIndex, edit.Inner, newTID)
			if e != nil {
				return response, fmt.Errorf("job %d edit %d CL3/GDAT patch: %w", n, k, e)
			}
			actual, _, e := mskit.ExtractGraphicsTID(updated, edit.GDATIndex, edit.Inner)
			if e != nil || !bytes.Equal(actual, newTID) {
				return response, fmt.Errorf("job %d edit %d TID roundtrip: %v", n, k, e)
			}
			touched[edit.GDATIndex] = true
			r.TIDs = append(r.TIDs, rep)
		}
		after, e := mskit.ParseGDAT(updated)
		if e != nil {
			return response, e
		}
		if len(before.Entries) != len(after.Entries) {
			return response, errors.New("GDAT record count drift")
		}
		for i := range before.Entries {
			if !touched[i] && !bytes.Equal(before.Entries[i], after.Entries[i]) {
				return response, fmt.Errorf("GDAT unrelated record drift at %d", i)
			}
		}
		tmp, e := os.CreateTemp(filepath.Dir(dst), ".gdat-rgba-*.tmp")
		if e != nil {
			return response, e
		}
		staged = append(staged, stagedGDAT{tmp: tmp.Name(), dest: dst})
		if _, e = tmp.Write(updated); e != nil {
			tmp.Close()
			return response, e
		}
		if e = tmp.Sync(); e != nil {
			tmp.Close()
			return response, e
		}
		if e = tmp.Close(); e != nil {
			return response, e
		}
		r.OutputSHA256 = digest(updated)
		staged[len(staged)-1].result = r
	}
	for i, v := range staged {
		if beforePublish != nil {
			if e := beforePublish(i); e != nil {
				return response, e
			}
		}
		if e := os.Link(v.tmp, v.dest); e != nil {
			return response, e
		}
		published = append(published, v.dest)
		response.Jobs = append(response.Jobs, v.result)
	}
	response.Status = "PASS_NATIVE_GO_RGBA_TO_BC7_DXT5_GDAT_BATCH"
	return response, nil
}
func RunGDATRGBABatch(path string) (GDATPNGResponse, error) {
	return RunGDATRGBABatchWithHook(path, nil)
}
