package main

// Explicit opt-in, Go-only graphics container batch writer. Unlike the
// production graphics pipeline this accepts already rendered TID assets;
// it does not run Python, Pillow or modify original game archives.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type graphicsBatchEdit struct {
	GDATIndex         int    `json:"gdat_index"`
	Inner             string `json:"inner"`
	Replacement       string `json:"replacement"`
	SourceTIDSHA256   string `json:"source_tid_sha256"`
	ReplacementSHA256 string `json:"replacement_sha256"`
}
type graphicsBatchJob struct {
	Source       string              `json:"source"`
	Output       string              `json:"output"`
	SourceSHA256 string              `json:"source_sha256"`
	Edits        []graphicsBatchEdit `json:"edits"`
}
type graphicsBatchManifest struct {
	Version int                `json:"version"`
	Jobs    []graphicsBatchJob `json:"jobs"`
}
type graphicsBatchResult struct {
	Source       string `json:"source"`
	Output       string `json:"output"`
	SourceSHA256 string `json:"source_sha256"`
	OutputSHA256 string `json:"output_sha256"`
	PatchedTIDs  int    `json:"patched_tids"`
}
type graphicsBatchResponse struct {
	Status string                `json:"status"`
	Files  []graphicsBatchResult `json:"files"`
}
type graphicsBatchPrepared struct {
	path   string
	data   []byte
	tmp    string
	report graphicsBatchResult
}

func hashMatches(data []byte, expected string) bool {
	if len(expected) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(expected)
	if err != nil || len(decoded) != sha256.Size {
		return false
	}
	sum := sha256.Sum256(data)
	return bytes.Equal(sum[:], decoded)
}
func graphicsBatchValidate(raw []byte) (graphicsBatchManifest, error) {
	var m graphicsBatchManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, err
	}
	var more any
	if err := dec.Decode(&more); err != io.EOF {
		return m, fmt.Errorf("invalid trailing JSON: %v", err)
	}
	if m.Version != 1 || len(m.Jobs) == 0 || len(m.Jobs) > 128 {
		return m, errors.New("graphics manifest requires version 1 and 1..128 jobs")
	}
	for i, job := range m.Jobs {
		if len(job.Edits) == 0 || len(job.Edits) > 2048 {
			return m, fmt.Errorf("job %d must contain 1..2048 TID edits", i)
		}
		if len(job.SourceSHA256) != 64 {
			return m, fmt.Errorf("job %d missing source SHA", i)
		}
		if _, err := hex.DecodeString(job.SourceSHA256); err != nil {
			return m, fmt.Errorf("job %d invalid source SHA: %w", i, err)
		}
		seen := map[string]bool{}
		for k, e := range job.Edits {
			if e.GDATIndex < 0 || e.Inner == "" || strings.TrimSpace(e.Inner) != e.Inner || !strings.HasSuffix(strings.ToLower(e.Inner), ".tid") {
				return m, fmt.Errorf("job %d edit %d invalid GDAT/member", i, k)
			}
			key := fmt.Sprintf("%d/%s", e.GDATIndex, strings.ToLower(e.Inner))
			if seen[key] {
				return m, fmt.Errorf("job %d repeated TID target %s", i, key)
			}
			seen[key] = true
			if len(e.SourceTIDSHA256) != 64 || len(e.ReplacementSHA256) != 64 {
				return m, fmt.Errorf("job %d edit %d missing TID SHA", i, k)
			}
			if _, err := hex.DecodeString(e.SourceTIDSHA256); err != nil {
				return m, fmt.Errorf("invalid old TID SHA at job %d edit %d", i, k)
			}
			if _, err := hex.DecodeString(e.ReplacementSHA256); err != nil {
				return m, fmt.Errorf("invalid new TID SHA at job %d edit %d", i, k)
			}
		}
	}
	return m, nil
}

// All validation, native Go rewrites and temporary staging finish before any
// public output appears. Existing destinations are never overwritten. Only
// links published by this invocation are removed on an error. beforePublish
// is a test-only fault injection hook.
func executeNativeGraphicsBatchWithHook(args []string, out io.Writer, beforePublish func(int) error) (err error) {
	fs := flag.NewFlagSet("graphics-batch-tid-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifestArg := fs.String("manifest", "", "version-1 graphics batch manifest")
	if er := fs.Parse(args); er != nil {
		return er
	}
	if *manifestArg == "" || fs.NArg() != 0 {
		return errors.New("usage: graphics-batch-tid-go --manifest path.json")
	}
	manifestPath, er := filepath.Abs(*manifestArg)
	if er != nil {
		return er
	}
	raw, er := os.ReadFile(manifestPath)
	if er != nil {
		return er
	}
	m, er := graphicsBatchValidate(raw)
	if er != nil {
		return er
	}
	base := filepath.Dir(manifestPath)
	prepared := make([]graphicsBatchPrepared, 0, len(m.Jobs))
	sources := map[string]bool{}
	outputs := map[string]bool{}
	// Phase 1: validate ALL jobs and prepare complete archive bytes in memory.
	for i, job := range m.Jobs {
		src, er := batchPath(base, job.Source)
		if er != nil {
			return fmt.Errorf("job %d: %w", i, er)
		}
		dst, er := batchPath(base, job.Output)
		if er != nil {
			return fmt.Errorf("job %d: %w", i, er)
		}
		if src == dst || sources[src] || outputs[dst] {
			return fmt.Errorf("job %d duplicated or in-place path", i)
		}
		sources[src] = true
		outputs[dst] = true
		if er = batchNoExisting(dst); er != nil {
			return er
		}
		data, _, er := batchSource(src)
		if er != nil {
			return er
		}
		if !hashMatches(data, job.SourceSHA256) {
			return fmt.Errorf("job %d pristine source SHA mismatch", i)
		}
		original, er := mskit.ParseGDAT(data)
		if er != nil {
			return fmt.Errorf("job %d invalid source GDAT: %w", i, er)
		}
		originalNames := map[string]bool{}
		for k, e := range job.Edits {
			originalTID, _, er := mskit.ExtractGraphicsTID(data, e.GDATIndex, e.Inner)
			if er != nil {
				return fmt.Errorf("job %d edit %d: %w", i, k, er)
			}
			if !hashMatches(originalTID, e.SourceTIDSHA256) {
				return fmt.Errorf("job %d edit %d original TID SHA mismatch", i, k)
			}
			name, er := batchPath(base, e.Replacement)
			if er != nil {
				return er
			}
			replacement, st, er := batchSource(name)
			if er != nil {
				return fmt.Errorf("job %d edit %d: %w", i, k, er)
			}
			if st.Size() > 64<<20 || len(replacement) == 0 {
				return fmt.Errorf("job %d edit %d replacement TID size invalid", i, k)
			}
			if !hashMatches(replacement, e.ReplacementSHA256) {
				return fmt.Errorf("job %d edit %d replacement TID SHA mismatch", i, k)
			}
			// Validate source identity against the pristine archive, even for multiple
			// edits into the same GDAT/CL3. No move/rename nor same-TID duplicates.
			key := fmt.Sprintf("%d/%s", e.GDATIndex, strings.ToLower(e.Inner))
			if originalNames[key] {
				return fmt.Errorf("duplicate target %s", key)
			}
			originalNames[key] = true
			data, _, er = mskit.PatchGraphicsTIDGoSafe(data, e.GDATIndex, e.Inner, replacement)
			if er != nil {
				return fmt.Errorf("job %d edit %d: %w", i, k, er)
			}
		}
		result, er := mskit.ParseGDAT(data)
		if er != nil {
			return er
		}
		if len(result.Entries) != len(original.Entries) {
			return fmt.Errorf("job %d GDAT count changed", i)
		}
		changed := map[int]bool{}
		for _, e := range job.Edits {
			changed[e.GDATIndex] = true
		}
		for j := range original.Entries {
			if !changed[j] && !bytes.Equal(original.Entries[j], result.Entries[j]) {
				return fmt.Errorf("job %d untouched GDAT entry %d drift", i, j)
			}
		}
		s := sha256.Sum256(data)
		prepared = append(prepared, graphicsBatchPrepared{path: dst, data: data, report: graphicsBatchResult{Source: src, Output: dst, SourceSHA256: strings.ToLower(job.SourceSHA256), OutputSHA256: hex.EncodeToString(s[:]), PatchedTIDs: len(job.Edits)}})
	}
	// Phase 1b: never publish over another source (even symlink lexical aliases).
	for _, p := range prepared {
		if sources[p.path] {
			return fmt.Errorf("output aliases batch source")
		}
	}
	// Phase 2: stage all files in destination directories without publishing.
	defer func() {
		for _, p := range prepared {
			if p.tmp != "" {
				_ = os.Remove(p.tmp)
			}
		}
	}()
	for i := range prepared {
		p := &prepared[i]
		f, er := os.CreateTemp(filepath.Dir(p.path), ".ms-graphics-batch-*.tmp")
		if er != nil {
			return fmt.Errorf("stage job %d: %w", i, er)
		}
		p.tmp = f.Name()
		if er = f.Chmod(0600); er == nil {
			_, er = f.Write(p.data)
		}
		if er == nil {
			er = f.Sync()
		}
		closeEr := f.Close()
		if er == nil {
			er = closeEr
		}
		if er != nil {
			return er
		}
	}
	// Phase 3: no-clobber atomic per-file hardlinks + rollback on publication
	// failures. Never remove preexisting files or user-managed source material.
	published := []string{}
	defer func() {
		if err != nil {
			for _, p := range published {
				_ = os.Remove(p)
			}
		}
	}()
	for i := range prepared {
		if beforePublish != nil {
			err = beforePublish(i)
			if err != nil {
				return fmt.Errorf("publish job %d: %w", i, err)
			}
		}
		if err = os.Link(prepared[i].tmp, prepared[i].path); err != nil {
			return fmt.Errorf("publish job %d: %w", i, err)
		}
		published = append(published, prepared[i].path)
	}
	response := graphicsBatchResponse{Status: "written-unapproved-for-game"}
	for _, p := range prepared {
		response.Files = append(response.Files, p.report)
	}
	// Stdout errors cannot rollback successfully published complete outputs.
	err = nil
	return json.NewEncoder(out).Encode(response)
}
func executeNativeGraphicsBatch(args []string, out io.Writer) error {
	return executeNativeGraphicsBatchWithHook(args, out, nil)
}
func runNativeGraphicsBatch(args []string) { must(executeNativeGraphicsBatch(args, os.Stdout)) }
