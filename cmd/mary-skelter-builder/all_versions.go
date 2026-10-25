package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/switchrelease"
)

func runAllVersions(root string, args []string) {
	fs := flag.NewFlagSet("all-versions", flag.ExitOnError)
	ms2 := fs.String("ms2-master", "", "full pristine MS2 latest Western Switch masterNX64")
	finale := fs.String("finale-master", "", "full pristine Finale latest Western Switch masterNX64")
	candidate := fs.Bool("candidate", false, "acknowledge unapproved Switch gameplay/runtime candidate")
	refresh := fs.Bool("refresh-movies", false, "refresh Finale movie audio source cache")
	overwrite := fs.Bool("overwrite", false, "replace existing PC and both Switch ZIPs; keep old copies for rollback")
	graphicsZIP := fs.String("canonical-graphics-zip", "", "optional previously verified Switch graphics ZIP")
	graphicsSHA := fs.String("canonical-graphics-sha256", "", "full SHA-256 required for graphics reuse")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if !*candidate {
		fatal(errors.New("all-versions requires --candidate: Switch combined release has not passed final game QA"))
	}
	// Cached original source files take precedence. CLI paths, when provided,
	// are one-time imports for missing, pinned input files only.
	if _, err := switchrelease.EnsureIntegratedSourceCache(root, *ms2, *finale); err != nil {
		fatal(err)
	}
	if (*graphicsZIP == "") != (*graphicsSHA == "") {
		fatal(errors.New("graphics ZIP requires matching SHA-256"))
	}
	// Reject unavailable Switch masters before the slower PC stages start.
	// Exact text and graphics archive source SHA checks occurred before the PC stages.
	fmt.Println("Building PC release, then BOTH translated Switch candidates. Switch runtime QA remains pending.")
	must(maintainer.WithReleaseReplacement(root, version(root), true, true, true, *overwrite, func() error {
		if err := maintainer.RunAll(root, *refresh, "", os.Stdout, os.Stderr); err != nil {
			return err
		}
		result, err := switchrelease.BuildIntegratedCandidate(switchrelease.IntegratedOptions{
			Options:              switchrelease.Options{Root: root, Version: version(root), MS2Master: *ms2, FinaleMaster: *finale, Candidate: true, Platform: "both", OutputDir: filepath.Join(root, ".local", "release", "packages"), Stdout: os.Stdout, Stderr: os.Stderr},
			CanonicalGraphicsZIP: *graphicsZIP, CanonicalGraphicsSHA256: *graphicsSHA,
		})
		if err != nil {
			return err
		}
		if len(result.Releases) != 2 || len(result.TextReports) != 2 {
			return errors.New("incomplete integrated Switch output")
		}
		return nil
	}))
	fmt.Printf("ALL VERSIONS: 1 PC + 2 Switch candidate ZIPs under %s\n", filepath.Join(root, ".local", "release", "packages"))
}

func mustStatus(root, ms2, finale string) []switchrelease.SourceStatus {
	states, err := switchrelease.Status(root, ms2, finale)
	must(err)
	return states
}
