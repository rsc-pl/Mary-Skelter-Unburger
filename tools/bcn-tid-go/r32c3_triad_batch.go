package main

// legacy private research-only all-three-family batch, with SHA-locked source
// TIDs, original private fonts and atomic no-clobber multi-output publication.
// Not used by the production graphics dispatcher or allowlist.
import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type triadBatch struct {
	Version     int    `json:"version"`
	ProjectRoot string `json:"project_root"`
	Jobs        []struct {
		Family       string `json:"family"`
		SourceTID    string `json:"source_tid"`
		SourceSHA256 string `json:"source_sha256"`
		OutputRGBA   string `json:"output_rgba"`
	} `json:"jobs"`
}
type triadStage struct{ temp, out string }

// publishTriad stages all candidate bytes first; injected late failure or
// filesystem collision removes only links created by this transaction.
func publishTriad(stages []triadStage, beforePublish func(int) error) ([]string, error) {
	made := []string{}
	for i, s := range stages {
		if beforePublish != nil {
			if err := beforePublish(i); err != nil {
				for _, p := range made {
					_ = os.Remove(p)
				}
				return nil, err
			}
		}
		if err := os.Link(s.temp, s.out); err != nil {
			for _, p := range made {
				_ = os.Remove(p)
			}
			return nil, err
		}
		made = append(made, s.out)
	}
	return made, nil
}

type triadBatchReport struct {
	Status  string `json:"status"`
	Outputs []struct {
		Family string `json:"family"`
		SHA256 string `json:"rgba_sha256"`
		Bytes  int    `json:"rgba_bytes"`
	} `json:"outputs"`
}

func runTriadBatch(path string) (triadBatchReport, error) {
	return runTriadBatchWithHook(path, nil)
}
func runTriadBatchWithHook(path string, beforePublish func(int) error) (rep triadBatchReport, err error) {
	rep.Status = "RESEARCH_NOT_PROVEN"
	b, err := os.ReadFile(path)
	if err != nil {
		return rep, err
	}
	var spec triadBatch
	if err = json.Unmarshal(b, &spec); err != nil {
		return rep, err
	}
	if spec.Version != 1 || len(spec.Jobs) != 3 || !filepath.IsAbs(spec.ProjectRoot) {
		return rep, errors.New("legacy requires absolute project_root and exactly three families")
	}
	root := filepath.Clean(spec.ProjectRoot)
	type job struct {
		fam, src, out string
		sha           string
	}
	jobs := make([]job, 0, 3)
	seen := map[string]bool{}
	destinations := map[string]bool{}
	sources := map[string]bool{}
	// Preflight all three before any font raster/large decode or output staging.
	for _, j := range spec.Jobs {
		expected, ok := pendingR32C3[j.Family]
		if !ok || seen[j.Family] || j.SourceSHA256 != expected.SHA {
			return rep, fmt.Errorf("duplicate/unapproved family or wrong source pin: %q", j.Family)
		}
		seen[j.Family] = true
		if !filepath.IsAbs(j.SourceTID) || !filepath.IsAbs(j.OutputRGBA) {
			return rep, errors.New("source and output must be absolute")
		}
		src, out := filepath.Clean(j.SourceTID), filepath.Clean(j.OutputRGBA)
		if src == out || destinations[out] || sources[src] || sources[out] || destinations[src] {
			return rep, errors.New("source/output paths collide")
		}
		sources[src] = true
		destinations[out] = true
		info, e := os.Lstat(src)
		if e != nil {
			return rep, e
		}
		if !info.Mode().IsRegular() {
			return rep, fmt.Errorf("nonregular source %s", src)
		}
		if info.Size() != int64(128+expected.Width*expected.Height) {
			return rep, fmt.Errorf("unexpected source TID size %s", src)
		}
		if _, e = os.Lstat(out); e == nil {
			return rep, fmt.Errorf("output exists %s", out)
		} else if !os.IsNotExist(e) {
			return rep, e
		}
		parentInfo, e := os.Lstat(filepath.Dir(out))
		if e != nil {
			return rep, e
		}
		if !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
			return rep, fmt.Errorf("nonregular output directory %s", out)
		}
		if _, e = loadR32C3Triad(root, j.Family, true); e != nil {
			return rep, e
		}
		jobs = append(jobs, job{j.Family, src, out, j.SourceSHA256})
	}
	var temps []triadStage
	defer func() {
		for _, s := range temps {
			_ = os.Remove(s.temp)
		}

	}()
	for _, j := range jobs {
		f, e := os.Open(j.src)
		if e != nil {
			err = e
			return
		}
		data, e := io.ReadAll(f)
		ce := f.Close()
		if e != nil {
			err = e
			return
		}
		if ce != nil {
			err = ce
			return
		}
		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != j.sha {
			err = fmt.Errorf("source SHA mismatch: %s", j.fam)
			return
		}
		rgba, e := renderR32C3Triad(root, j.fam, data)
		if e != nil {
			err = e
			return
		}
		tmp, e := os.CreateTemp(filepath.Dir(j.out), ".r32c3-batch-*.rgba.tmp")
		if e != nil {
			err = e
			return
		}
		temps = append(temps, triadStage{tmp.Name(), j.out})
		if e = tmp.Chmod(0600); e == nil {
			_, e = tmp.Write(rgba)
		}
		if e == nil {
			e = tmp.Sync()
		}
		ce = tmp.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			err = e
			return
		}
		sum := sha256.Sum256(rgba)
		rep.Outputs = append(rep.Outputs, struct {
			Family string `json:"family"`
			SHA256 string `json:"rgba_sha256"`
			Bytes  int    `json:"rgba_bytes"`
		}{j.fam, hex.EncodeToString(sum[:]), len(rgba)})
	}
	if _, err = publishTriad(temps, beforePublish); err != nil {
		return rep, err
	}
	return rep, nil
}
