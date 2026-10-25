package main

// Research-only, opt-in multi-archive GDAT writer. Everything is validated and
// staged before publishing; pre-existing paths are never replaced. Neither
// production Rebuild nor the release pipeline invokes this command.

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/mskit"
)

type pcBatchJob struct {
	Dataset string `json:"dataset"`
	Archive int    `json:"archive"`
	Records string `json:"records"`
	Source  string `json:"source"`
	Output  string `json:"output"`
}
type pcBatchManifest struct {
	Version int          `json:"version"`
	Jobs    []pcBatchJob `json:"jobs"`
}
type pcBatchReport struct {
	Status  string                `json:"status"`
	Encoder string                `json:"encoder"`
	Files   []nativeTextCLIResult `json:"files"`
}
type pcBatchPrepared struct {
	path   string
	data   []byte
	temp   string
	result nativeTextCLIResult
}

func batchPath(base, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("empty batch path")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	return filepath.Abs(filepath.Clean(path))
}
func batchNoExisting(path string) error {
	if i, err := os.Lstat(path); err == nil {
		return fmt.Errorf("existing output %s (%s)", path, i.Mode())
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
func batchSource(path string) ([]byte, os.FileInfo, error) {
	fi, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer fi.Close()
	stat, err := fi.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !stat.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("source is not regular: %s", path)
	}
	raw, err := io.ReadAll(fi)
	return raw, stat, err
}
func stageBatchOutput(p *pcBatchPrepared) error {
	f, err := os.CreateTemp(filepath.Dir(p.path), ".mary-skelter-batch-*")
	if err != nil {
		return err
	}
	p.temp = f.Name()
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(p.data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// The hook is only for fault-injection QA; public dispatch passes nil. Each
// successful link is owned by this invocation, so publication failure rolls
// back only new files, never existing files or user data.
func executeNativePCTextBatchWithHook(args []string, stdout io.Writer, beforeLink func(int) error) error {
	fs := flag.NewFlagSet("pc-text-batch-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifestArg := fs.String("manifest", "", "JSON with version 1 and ordered jobs")
	goZlib := fs.Bool("allow-semantic-go-zlib", false, "RESEARCH ONLY: encoded bytes may differ from legacy")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestArg == "" || fs.NArg() != 0 {
		return fmt.Errorf("usage: pc-text-batch-go --manifest batch.json [--allow-semantic-go-zlib]")
	}
	manifestPath, err := filepath.Abs(*manifestArg)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest pcBatchManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&manifest); err != nil {
		return err
	}
	var trailing any
	if err = dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("manifest contains trailing JSON or invalid data: %v", err)
	}
	if manifest.Version != 1 || len(manifest.Jobs) == 0 || len(manifest.Jobs) > 999 {
		return fmt.Errorf("batch manifest must have version 1 and 1..999 jobs")
	}
	base := filepath.Dir(manifestPath)
	var encode mskit.CanonicalBILZEncoder
	encoder := "strict-no-new-zlib"
	if *goZlib {
		encode = mskit.WrapBILZGo
		encoder = "go-zlib-decoded-equivalence-experimental"
	}
	prepared := make([]pcBatchPrepared, 0, len(manifest.Jobs))
	outputKeys := map[string]bool{}
	sourceKeys := map[string]bool{}
	sourceInfo := make([]os.FileInfo, 0, len(manifest.Jobs))
	sourcePaths := make([]string, 0, len(manifest.Jobs))
	// Phase 1: validate and rebuild all sources IN MEMORY. Publish nothing.
	for i, job := range manifest.Jobs {
		if job.Archive < 0 || job.Archive > 999 {
			return fmt.Errorf("job %d invalid archive", i)
		}
		source, err := batchPath(base, job.Source)
		if err != nil {
			return fmt.Errorf("job %d source: %w", i, err)
		}
		target, err := batchPath(base, job.Output)
		if err != nil {
			return fmt.Errorf("job %d output: %w", i, err)
		}
		records, err := batchPath(base, job.Records)
		if err != nil {
			return fmt.Errorf("job %d records: %w", i, err)
		}
		if filepath.Base(source) != fmt.Sprintf("GxArchivedFile%03d.dat", job.Archive) {
			return fmt.Errorf("job %d expected canonical GDAT source filename", i)
		}
		if source == target {
			return fmt.Errorf("job %d attempted in-place rewrite", i)
		}
		if outputKeys[target] {
			return fmt.Errorf("job %d repeated output target", i)
		}
		outputKeys[target] = true
		if sourceKeys[source] {
			return fmt.Errorf("job %d repeated source path", i)
		}
		sourceKeys[source] = true
		if err = batchNoExisting(target); err != nil {
			return err
		}
		input, stat, err := batchSource(source)
		if err != nil {
			return fmt.Errorf("job %d: %w", i, err)
		}
		rf, err := os.Open(records)
		if err != nil {
			return err
		}
		plan, pErr := maintainer.PlanPCTextRecords(rf, job.Dataset)
		closeErr := rf.Close()
		if pErr != nil {
			return fmt.Errorf("job %d: %w", i, pErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if plan.ChangedRecords == 0 || len(plan.Archives) != 1 || plan.Archives[0] != job.Archive {
			return fmt.Errorf("job %d requires changed records for exactly this archive", i)
		}
		rebuilt, report, err := maintainer.RebuildPCTextArchiveNative(input, job.Dataset, job.Archive, plan, encode)
		if err != nil {
			return fmt.Errorf("job %d rebuild: %w", i, err)
		}
		if report.ChangedFields != plan.ChangedRecords || bytes.Equal(input, rebuilt) {
			return fmt.Errorf("job %d inconsistent field count or unchanged result", i)
		}
		if _, err = mskit.ParseGDAT(rebuilt); err != nil {
			return fmt.Errorf("job %d result invalid: %w", i, err)
		}
		prepared = append(prepared, pcBatchPrepared{path: target, data: rebuilt, result: nativeTextCLIResult{Status: "written-unapproved-for-game", Encoder: encoder, SourcePath: source, OutputPath: target, Report: report}})
		sourceInfo = append(sourceInfo, stat)
		sourcePaths = append(sourcePaths, source)
	}
	// Cross-job aliases: avoid writing an output over another input's inode,
	// even when the input was a hard link or had an alternate lexical path.
	for i := range prepared {
		if sourceKeys[prepared[i].path] {
			return fmt.Errorf("output path aliases batch input")
		}
		for j := range sourcePaths {
			if prepared[i].path == sourcePaths[j] {
				return fmt.Errorf("output path is a batch source")
			}
			if st, err := os.Stat(prepared[i].path); err == nil && os.SameFile(st, sourceInfo[j]) {
				return fmt.Errorf("output aliases a source inode")
			}
		}
	}
	// Phase 2: stage every output before creating any public target.
	defer func() {
		for i := range prepared {
			if prepared[i].temp != "" {
				os.Remove(prepared[i].temp)
			}
		}
	}()
	for i := range prepared {
		if err = stageBatchOutput(&prepared[i]); err != nil {
			return fmt.Errorf("stage job %d: %w", i, err)
		}
	}
	// Phase 3: no-clobber publish. Any failure removes only targets we created.
	published := make([]string, 0, len(prepared))
	defer func() {
		if err != nil {
			for _, p := range published {
				os.Remove(p)
			}
		}
	}()
	for i := range prepared {
		if beforeLink != nil {
			err = beforeLink(i)
			if err != nil {
				return fmt.Errorf("publish fault at job %d: %w", i, err)
			}
		}
		if err = os.Link(prepared[i].temp, prepared[i].path); err != nil {
			return fmt.Errorf("publish job %d: %w", i, err)
		}
		published = append(published, prepared[i].path)
	}
	result := pcBatchReport{Status: "written-unapproved-for-game", Encoder: encoder}
	for _, p := range prepared {
		result.Files = append(result.Files, p.result)
	}
	// A report transport error does not undo already published valid files.
	// Avoid destructive cleanup on an unrelated stdout failure.
	err = nil
	return json.NewEncoder(stdout).Encode(result)
}
func executeNativePCTextBatch(args []string, stdout io.Writer) error {
	return executeNativePCTextBatchWithHook(args, stdout, nil)
}
func runNativePCTextBatch(args []string) { must(executeNativePCTextBatch(args, os.Stdout)) }
