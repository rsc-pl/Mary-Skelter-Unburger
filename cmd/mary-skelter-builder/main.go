package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"maryskelter-unburger/internal/builderui"
	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/switchrelease"
)

func main() {
	root, err := maintainer.FindRoot(".")
	if err != nil {
		fatal(err)
	}
	if len(os.Args) < 2 {
		if err := builderui.Run(builderui.Options{Root: root, Version: version(root)}); err != nil {
			fatal(err)
		}
		return
	}
	switch os.Args[1] {
	case "ui":
		runUI(root, os.Args[2:])
	case "status":
		runStatus(root, os.Args[2:])
	case "configure":
		runConfigure(root, os.Args[2:])
	case "movies":
		runMovies(root, os.Args[2:])
	case "verify":
		runVerify(root, os.Args[2:])
	case "rebuild":
		noArgs("rebuild", os.Args[2:])
		must(maintainer.Rebuild(root, os.Stdout, os.Stderr))
	case "source-records-parity":
		runSourceRecordsParity(root, os.Args[2:])
	case "text-verify-candidate":
		runTextVerifyCandidate(root, os.Args[2:])
	case "text-diff-candidate":
		runTextDiffCandidate(root, os.Args[2:])
	case "pc-master-audit-go":
		must(runPCMasterAuditNative(root, os.Args[2:], os.Stdout))
	case "pc-text-rebuild-go":
		runNativePCTextRebuild(os.Args[2:])
	case "pc-text-batch-go":
		runNativePCTextBatch(os.Args[2:])
	case "graphics-extract-tid":
		runGraphicsExtractTID(root, os.Args[2:])
	case "graphics-patch-tid":
		runGraphicsPatchTID(root, os.Args[2:])
	case "graphics-render-label-dxt5-go":
		runRender(os.Args[2:])
	case "graphics-stage-sources-go":
		must(executeGraphicsStageGo(root, os.Args[2:], os.Stdout))
	case "graphics-camp-top2-go":
		must(executeCampTop2(root, os.Args[2:], os.Stdout))
	case "graphics-proven-pc-go":
		runGraphicsProvenPC(root, os.Args[2:])
	case "graphics-batch-tid-go":
		runNativeGraphicsBatch(os.Args[2:])
	case "graphics-patch-tid-go":
		runGraphicsPatchTIDGo(os.Args[2:])
	case "graphics-encode-dxt5":
		runGraphicsEncodeDXT5(root, os.Args[2:])
	case "graphics-decode-dxt5":
		runGraphicsDecodeDXT5(root, os.Args[2:])
	case "graphics-decode-dxt1":
		runGraphicsDecodeLegacy(os.Args[2:], "dxt1")
	case "graphics-decode-dxt3":
		runGraphicsDecodeLegacy(os.Args[2:], "dxt3")
	case "graphics-decode-bc7":
		runGraphicsDecodeBC7(root, os.Args[2:])
	case "graphics-tid-to-dds":
		runGraphicsTIDToDDS(root, os.Args[2:])
	case "graphics-splice-bc7":
		runGraphicsSpliceBC7(root, os.Args[2:])
	case "prepare":
		runPrepare(root, os.Args[2:])
	case "container":
		noArgs("container", os.Args[2:])
		must(maintainer.BuildContainer(root, os.Stdout))
	case "release":
		runRelease(root, os.Args[2:])
	case "switch-status":
		runSwitchStatus(root, os.Args[2:])
	case "switch-import":
		runSwitchImport(root, os.Args[2:])
	case "switch-release":
		runSwitchRelease(root, os.Args[2:])
	case "switch-integrated-candidate":
		must(executeSwitchIntegratedCandidate(root, os.Args[2:], os.Stdout, os.Stderr))
	case "all-versions":
		runAllVersions(root, os.Args[2:])
	case "all":
		runAll(root, os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func version(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, "version.txt"))
	if err != nil {
		return "dev"
	}
	v := strings.TrimSpace(string(raw))
	if v == "" {
		return "dev"
	}
	return v
}
func runUI(root string, args []string) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	plain := fs.Bool("plain", false, "use numbered text menu")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	must(builderui.Run(builderui.Options{Root: root, Version: version(root), Plain: *plain}))
}
func runStatus(root string, args []string) {
	noArgs("status", args)
	s := maintainer.Inspect(root)
	fmt.Printf("source_paths=%t\nsource_cache=%t\ncanonical_dist=%t\nmovie_cache=%t\nmovie_pairs=%d\nmovie_payload_objects=%d\nrelease_stage=%t\nasset_key=%t\nasset_container=%t\nend_user_release=%t\n", s.SourceConfig, s.SourceCache, s.Dist, s.MovieCache, s.MoviePairs, s.MovieObjects, s.Stage, s.Key, s.Container, s.Release)
	if len(s.MissingTools) > 0 {
		fmt.Printf("missing_tools=%s\n", strings.Join(s.MissingTools, ","))
	} else {
		fmt.Println("missing_tools=")
	}
}
func runConfigure(root string, args []string) {
	fs := flag.NewFlagSet("configure", flag.ExitOnError)
	ms2 := fs.String("ms2", "", "Mary Skelter 2 game directory")
	finale := fs.String("finale", "", "Mary Skelter Finale game directory")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	must(maintainer.ConfigureSources(root, *ms2, *finale, os.Stdout, os.Stderr))
}
func runMovies(root string, args []string) {
	fs := flag.NewFlagSet("movies", flag.ExitOnError)
	captureEN := fs.Bool("capture-en", false, "cache current Finale MOVIE_EN")
	captureJP := fs.Bool("capture-jp", false, "cache current Finale MOVIE_JP")
	refresh := fs.Bool("refresh", false, "rebuild movie audit/payload from cached EN+JP")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if !*captureEN && !*captureJP && !*refresh {
		en, jp := maintainer.CachedFinaleMovieCounts(root)
		cen, cjp, err := maintainer.CurrentFinaleMovieCounts(root)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("current Finale: MOVIE_EN=%d/22 MOVIE_JP=%d/22\ncached Finale:  MOVIE_EN=%d/22 MOVIE_JP=%d/22\n", cen, cjp, en, jp)
		return
	}
	if *captureEN {
		_, err := maintainer.CaptureFinaleMovieSet(root, "MOVIE_EN", os.Stdout)
		must(err)
	}
	if *captureJP {
		_, err := maintainer.CaptureFinaleMovieSet(root, "MOVIE_JP", os.Stdout)
		must(err)
	}
	if *refresh {
		must(maintainer.RefreshMovieAudio(root, os.Stdout, os.Stderr))
	}
}
func runVerify(root string, args []string) {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	quick := fs.Bool("quick", false, "skip expensive translation->technical bridge")
	nativeGo := fs.Bool("native-go", false, "use native Go verification; no legacy Python subprocesses")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if *nativeGo {
		must(maintainer.VerifyNativeGo(root, *quick, os.Stdout, os.Stderr))
		return
	}
	must(maintainer.Verify(root, *quick, os.Stdout, os.Stderr))
}

func runSourceRecordsParity(root string, args []string) {
	fs := flag.NewFlagSet("source-records-parity", flag.ExitOnError)
	cacheRoot := fs.String("cache-root", "", "source cache root (default: .local/source_cache)")
	report := fs.String("report", "", "parity JSON report path")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	must(maintainer.ParitySourceRecords(root, *cacheRoot, *report, os.Stdout))
}

func parseArchiveList(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []int
	seen := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid archive number %q", part)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out, nil
}

func runTextVerifyCandidate(root string, args []string) {
	fs := flag.NewFlagSet("text-verify-candidate", flag.ExitOnError)
	dataset := fs.String("dataset", "", "dataset name")
	rebuilt := fs.String("rebuilt-master", "", "rebuilt master directory")
	expected := fs.String("expected-records", "", "expected JSONL records")
	source := fs.String("source-master", "", "pristine source master directory")
	archivesRaw := fs.String("archives", "", "comma-separated archive numbers")
	report := fs.String("report", "", "output JSON report")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if *dataset == "" || *rebuilt == "" || *expected == "" || *report == "" {
		fatal(fmt.Errorf("--dataset, --rebuilt-master, --expected-records and --report are required"))
	}
	archives, err := parseArchiveList(*archivesRaw)
	must(err)
	_, err = maintainer.VerifyTextDatasetGo(*dataset, *rebuilt, *expected, *source, archives, *report)
	must(err)
	fmt.Println(*report)
}

func runTextDiffCandidate(root string, args []string) {
	fs := flag.NewFlagSet("text-diff-candidate", flag.ExitOnError)
	dataset := fs.String("dataset", "", "dataset name")
	rebuilt := fs.String("rebuilt-master", "", "rebuilt master directory")
	source := fs.String("source-master", "", "pristine source master directory")
	archivesRaw := fs.String("archives", "", "comma-separated archive numbers")
	report := fs.String("report", "", "output JSON report")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if *dataset == "" || *rebuilt == "" || *source == "" || *report == "" {
		fatal(fmt.Errorf("--dataset, --rebuilt-master, --source-master and --report are required"))
	}
	archives, err := parseArchiveList(*archivesRaw)
	must(err)
	_, err = maintainer.DiffTextDatasetGo(*dataset, *rebuilt, *source, archives, *report)
	must(err)
	fmt.Println(*report)
}

func runPrepare(root string, args []string) {
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	refresh := fs.Bool("refresh-movies", false, "refresh movie source comparison instead of reusing complete cache")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	must(maintainer.PrepareReleaseAssets(root, *refresh, os.Stdout, os.Stderr))
}
func runRelease(root string, args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	v := fs.String("version", "", "release version override")
	overwrite := fs.Bool("overwrite", false, "explicitly replace an existing PC release; save previous ZIP in private recovery")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	must(maintainer.WithReleaseReplacement(root, *v, true, false, false, *overwrite, func() error {
		_, err := maintainer.BuildRelease(root, *v, os.Stdout)
		return err
	}))
}
func runSwitchStatus(root string, args []string) {
	fs := flag.NewFlagSet("switch-status", flag.ExitOnError)
	ms2 := fs.String("ms2-master", "", "MS2 latest-update masterNX64/RomFS path")
	finale := fs.String("finale-master", "", "Finale latest-update masterNX64/RomFS path")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	status, err := switchrelease.Status(root, *ms2, *finale)
	must(err)
	for _, st := range status {
		state := "PASS"
		if st.Error != "" {
			state = "MISSING/INVALID"
		}
		fmt.Printf("%s: %s | source=%s | %v\n", st.Game, state, st.Root, st.Files)
		if st.Error != "" {
			fmt.Printf("  %s\n", st.Error)
		}
	}
}
func runSwitchImport(root string, args []string) {
	fs := flag.NewFlagSet("switch-import", flag.ExitOnError)
	ms2 := fs.String("ms2-master", "", "MS2 latest-update extracted source directory")
	finale := fs.String("finale-master", "", "Finale latest-update extracted source directory")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	if *ms2 == "" && *finale == "" {
		fatal(fmt.Errorf("provide --ms2-master and/or --finale-master"))
	}
	must(switchrelease.ImportSources(root, *ms2, *finale, os.Stdout))
}
func runSwitchRelease(root string, args []string) {
	fs := flag.NewFlagSet("switch-release", flag.ExitOnError)
	ms2 := fs.String("ms2-master", "", "MS2 latest-update extracted source directory (otherwise local cache)")
	finale := fs.String("finale-master", "", "Finale latest-update extracted source directory (otherwise local cache)")
	out := fs.String("out-dir", "", "release ZIP output directory")
	vers := fs.String("version", "", "output version (default: version.txt)")
	platform := fs.String("platform", "both", "both, atmosphere or eden")
	candidate := fs.Bool("candidate", false, "explicitly allow V18 not yet visually/runtime accepted")
	python := fs.String("python", "python3", "canonical Switch graphics builder Python interpreter")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	results, err := switchrelease.Build(switchrelease.Options{Root: root, Version: *vers, MS2Master: *ms2, FinaleMaster: *finale, OutputDir: *out, Python: *python, Platform: *platform, Candidate: *candidate, Stdout: os.Stdout, Stderr: os.Stderr})
	must(err)
	fmt.Println("SWITCH GRAPHICS + IPS CANDIDATES: built (Switch text translation NOT integrated; runtime QA pending)")
	for _, result := range results {
		fmt.Printf("%s ZIP: %s\nSHA256: %s\nFiles: %d\n", result.Platform, result.ZIP, result.SHA256, result.Files)
	}
}

func runAll(root string, args []string) {
	fs := flag.NewFlagSet("all", flag.ExitOnError)
	refresh := fs.Bool("refresh-movies", false, "refresh movie source cache")
	v := fs.String("version", "", "release version override")
	overwrite := fs.Bool("overwrite", false, "explicitly replace existing PC ZIP, preserving previous package")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}
	must(maintainer.WithReleaseReplacement(root, *v, true, false, false, *overwrite, func() error {
		return maintainer.RunAll(root, *refresh, *v, os.Stdout, os.Stderr)
	}))
}
func noArgs(name string, args []string) {
	if len(args) != 0 {
		fatal(fmt.Errorf("%s accepts no arguments", name))
	}
}
func must(err error) {
	if err != nil {
		fatal(err)
	}
}
func fatal(err error) { fmt.Fprintln(os.Stderr, "ERROR:", err); os.Exit(1) }
func usage() {
	fmt.Println(`Mary Skelter Unburger maintainer builder

Usage:
  mary-skelter-builder                         interactive maintainer wizard
  mary-skelter-builder ui [--plain]            interactive / numbered menu
  mary-skelter-builder status                  show project/build readiness
  mary-skelter-builder configure --ms2 <dir> --finale <dir>
  mary-skelter-builder movies [--capture-en] [--capture-jp] [--refresh]
  mary-skelter-builder verify [--quick]
  mary-skelter-builder rebuild
  mary-skelter-builder source-records-parity [--cache-root <dir>] [--report <json>]
  mary-skelter-builder pc-master-audit-go --ms2-master <masterWindows> --finale-master <masterWindows> [--all-manifest]   (READ-ONLY provenance, no semantic parity)
  mary-skelter-builder prepare [--refresh-movies]
  mary-skelter-builder container
  mary-skelter-builder release [--version <v>] [--overwrite]
  mary-skelter-builder switch-status [--ms2-master <dir>] [--finale-master <dir>]
  mary-skelter-builder switch-import --ms2-master <dir> --finale-master <dir>
  mary-skelter-builder switch-integrated-candidate --ms2-master <dir> --finale-master <dir> --candidate [--platform both|atmosphere|eden]   (UNAPPROVED, canonical translations + graphics + IPS)
  mary-skelter-builder switch-release [--ms2-master <dir>] [--finale-master <dir>] [--out-dir <dir>] [--version <v>] [--platform both|atmosphere|eden] --candidate
  mary-skelter-builder graphics-batch-tid-go --manifest batch.json
  mary-skelter-builder graphics-camp-top2-go --source-tid <original-camp_top_2.tid> --out-tid <new.tid> [--preview crop.png]   (PRIVATE CANDIDATE, NO GAME PATCH)
  mary-skelter-builder graphics-patch-tid-go --dat <GDAT> --out <NEW-GDAT> --source-sha256 <SHA256> --gdat-index N --inner <TID-name> < replacement.tid
  mary-skelter-builder all [--refresh-movies] [--version <v>] [--overwrite]             (PC)
  mary-skelter-builder all-versions [--overwrite] --ms2-master <dir> --finale-master <dir> --candidate [--canonical-graphics-zip <zip> --canonical-graphics-sha256 <sha>]

The interactive wizard guides source setup and Finale Steam English/Japanese movie acquisition.
The PC ZIP contains Linux/Windows updaters, run.sh/run.bat, README.txt, one authenticated .unburger-assets v3 sidecar and SHA256SUMS.txt. All final archives are in .local/release/packages.`)
}
