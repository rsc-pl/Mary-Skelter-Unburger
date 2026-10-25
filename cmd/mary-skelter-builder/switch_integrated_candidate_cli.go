package main

// The previously private Switch integration already builds text from the
// canonical PC translations. Expose it for explicit maintainer-only QA without
// conflating it with the graphics-only --candidate command or runtime approval.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"maryskelter-unburger/internal/switchrelease"
)

func executeSwitchIntegratedCandidate(root string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("switch-integrated-candidate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	ms2 := fs.String("ms2-master", "", "pristine latest Western MS2 masterNX64")
	finale := fs.String("finale-master", "", "pristine latest Western Finale masterNX64")
	nativeMS2 := fs.String("ms2-native-records", "", "private MS2 original native records; default pinned project input")
	nativeFinale := fs.String("finale-native-records", "", "private Finale original native records; default pinned project input")
	dest := fs.String("out-dir", "", "candidate destination")
	platform := fs.String("platform", "both", "both, atmosphere, eden")
	python := fs.String("python", "python3", "existing graphics renderer interpreter")
	graphicsZIP := fs.String("canonical-graphics-zip", "", "prebuilt source-bound canonical graphics candidate ZIP (explicit Go packaging path)")
	graphicsSHA := fs.String("canonical-graphics-sha256", "", "full SHA-256 of prebuilt canonical ZIP; required with --canonical-graphics-zip")
	candidate := fs.Bool("candidate", false, "acknowledge source-bound, NOT runtime-approved candidate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if !*candidate {
		return errors.New("integrated Switch output is NOT runtime-approved: explicit --candidate is required")
	}
	// Empty source flags mean use previously imported verified originals.
	// Existing source paths can supplement missing originals once.
	if _, err := switchrelease.EnsureIntegratedSourceCache(root, *ms2, *finale); err != nil {
		return fmt.Errorf("integrated Switch candidate BLOCKED: %w", err)
	}
	if *dest == "" {
		*dest = filepath.Join(root, ".local", "release", "packages")
	}
	// An explicit flag is the sole opt-in. The integrated builder never silently
	// drops native text, reuses a copied Switch EN corpus, or invokes the
	// graphics-only release path after a failed validation.
	result, err := switchrelease.BuildIntegratedCandidate(switchrelease.IntegratedOptions{
		Options: switchrelease.Options{
			Root: root, MS2Master: *ms2, FinaleMaster: *finale,
			OutputDir: *dest, Platform: *platform, Python: *python,
			Candidate: true, Stdout: stdout, Stderr: stderr,
		},
		MS2NativeRecords: *nativeMS2, FinaleNativeRecords: *nativeFinale,
		CanonicalGraphicsZIP: *graphicsZIP, CanonicalGraphicsSHA256: *graphicsSHA,
	})
	if err != nil {
		return fmt.Errorf("integrated Switch candidate BLOCKED: %w", err)
	}
	if len(result.Releases) == 0 || len(result.TextReports) != 2 || len(result.NativeReports) != 2 {
		return errors.New("integrated Switch candidate missing dual-game text/release proof")
	}
	fmt.Fprintln(stdout, "INTEGRATED SWITCH CANDIDATE ONLY — NOT RUNTIME APPROVED; canonical PC translations were freshly staged")
	for _, r := range result.Releases {
		fmt.Fprintf(stdout, "%s candidate ZIP: %s\nSHA256: %s\nFiles: %d\n", r.Platform, r.ZIP, r.SHA256, r.Files)
	}
	return nil
}
