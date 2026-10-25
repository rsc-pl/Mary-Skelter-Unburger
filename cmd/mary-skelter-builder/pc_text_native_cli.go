package main

// Explicit opt-in native text GDAT writer. Production `rebuild` remains on the
// established legacy backend until owner source archives pass complete QA.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/mskit"
)

type nativeTextCLIResult struct {
	Status     string                       `json:"status"`
	Encoder    string                       `json:"encoder"`
	SourcePath string                       `json:"source_path"`
	OutputPath string                       `json:"output_path"`
	Report     maintainer.PCTextWriteReport `json:"report"`
}

// createNewPrivate atomically publishes one brand-new file and never overwrites
// any file (including the user's game source). Hard-linking avoids rename's
// overwrite semantics even when another process creates the target meanwhile.
func createNewPrivate(path string, data []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("missing output path")
	}
	if info, err := os.Lstat(path); err == nil {
		return fmt.Errorf("refusing to overwrite %s (%s)", path, info.Mode())
	} else if !os.IsNotExist(err) {
		return err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".mary-skelter-godat-*")
	if err != nil {
		return err
	}
	tmpname := temp.Name()
	defer os.Remove(tmpname)
	if err = temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err = temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err = temp.Close(); err != nil {
		return err
	}
	if err = os.Link(tmpname, path); err != nil {
		return fmt.Errorf("refuse existing or invalid output %s: %w", path, err)
	}
	return nil
}

func executeNativePCTextRebuild(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("pc-text-rebuild-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dataset := fs.String("dataset", "", "MS2_PC_LOCALIZED, MSF_PC_LOCALIZED or MSF_PC_SYSTEM")
	archive := fs.Int("archive", -1, "archive number")
	records := fs.String("records", "", "source-pinned technical JSONL with edits")
	source := fs.String("source", "", "pristine original GDAT source (read-only)")
	output := fs.String("out", "", "new output GDAT (must not exist)")
	goZlib := fs.Bool("allow-semantic-go-zlib", false, "explicitly opt into decoded-equivalent Go zlib, NOT byte or game-runtime verified")
	allowNoop := fs.Bool("allow-no-op", false, "permit unchanged archive; normally an error")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *dataset == "" || *archive < 0 || *archive > 999 || *records == "" || *source == "" || *output == "" {
		return fmt.Errorf("usage: pc-text-rebuild-go --dataset DATASET --archive N --records TECHNICAL.jsonl --source ORIGINAL.dat --out NEW.dat [--allow-semantic-go-zlib]")
	}
	// Make implicit in-place writes and symbolic aliases fail even before reading.
	in, err := filepath.Abs(*source)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(*output)
	if err != nil {
		return err
	}
	if in == out {
		return fmt.Errorf("refusing to replace source")
	}
	if _, err = os.Lstat(out); err == nil {
		return fmt.Errorf("refusing existing output %s", out)
	} else if !os.IsNotExist(err) {
		return err
	}
	fi, err := os.Open(in)
	if err != nil {
		return err
	}
	defer fi.Close()
	st, err := fi.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file")
	}
	// No fixed low cap on real GDAT source. The output is written only after
	// successful full parse, source-pin check and post-rebuild decode validation.
	original, err := io.ReadAll(fi)
	if err != nil {
		return err
	}
	rf, err := os.Open(*records)
	if err != nil {
		return err
	}
	defer rf.Close()
	plan, err := maintainer.PlanPCTextRecords(rf, *dataset)
	if err != nil {
		return err
	}
	if plan.ChangedRecords == 0 && !*allowNoop {
		return fmt.Errorf("no changed records; refuse to publish no-op output")
	}
	if len(plan.Archives) != 0 && (len(plan.Archives) != 1 || plan.Archives[0] != *archive) {
		return fmt.Errorf("record plan addresses other archives %v; split first", plan.Archives)
	}
	var encode mskit.CanonicalBILZEncoder
	name := "strict-no-new-zlib"
	if *goZlib {
		encode = mskit.WrapBILZGo
		name = "go-zlib-decoded-equivalence-experimental"
	}
	rebuilt, report, err := maintainer.RebuildPCTextArchiveNative(original, *dataset, *archive, plan, encode)
	if err != nil {
		return err
	}
	if plan.ChangedRecords > 0 && (report.ChangedFields != plan.ChangedRecords || bytes.Equal(original, rebuilt)) {
		return fmt.Errorf("changed-field count/output inconsistency; refusing output")
	}
	// The writer verifies untouched entries and parsed output. Re-check here
	// before any filesystem publication, as an independent command boundary.
	if _, err := mskit.ParseGDAT(rebuilt); err != nil {
		return fmt.Errorf("output GDAT invalid: %w", err)
	}
	if err := createNewPrivate(out, rebuilt); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(nativeTextCLIResult{Status: "written-unapproved-for-game", Encoder: name, SourcePath: in, OutputPath: out, Report: report})
}

func runNativePCTextRebuild(args []string) { must(executeNativePCTextRebuild(args, os.Stdout)) }
