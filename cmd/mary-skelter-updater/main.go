package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"maryskelter-unburger/internal/ui"
	"maryskelter-unburger/internal/updater"
)

var embeddedVersion string

func main() {
	if len(os.Args) < 2 {
		interactive(nil)
		return
	}
	switch os.Args[1] {
	case "ui":
		interactive(os.Args[2:])
	case "status":
		status(os.Args[2:])
	case "install":
		runInstall(os.Args[2:], "install")
	case "repair":
		runInstall(os.Args[2:], "repair")
	case "cleanup-audit":
		cleanupAudit(os.Args[2:])
	case "convert-save":
		convertSave(os.Args[2:])
	case "verify-save":
		verifySave(os.Args[2:])
	case "build-gdat-payload":
		buildGDATPayload(os.Args[2:])
	case "build-remux-targets":
		buildRemuxTargets(os.Args[2:])
	case "build-assets-v3":
		buildAssetsV3(os.Args[2:])
	case "verify-assets-v3":
		verifyAssetsV3(os.Args[2:])
	case "build-release":
		buildRelease(os.Args[2:])
	case "build-deltas":
		buildDeltas(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func roots(ms2, finale string) map[string]string {
	out := map[string]string{}
	if ms2 != "" {
		out["ms2"] = ms2
	}
	if finale != "" {
		out["finale"] = finale
	}
	return out
}

func gameFlags(fs *flag.FlagSet) (*string, *string) {
	return fs.String("ms2", os.Getenv("MARY_SKELTER_MS2_DIR"), "Mary Skelter 2 game directory"),
		fs.String("finale", os.Getenv("MARY_SKELTER_FINALE_DIR"), "Mary Skelter Finale game directory")
}

func defaultAssetsPath() string {
	if p := os.Getenv("MARY_SKELTER_ASSETS"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "mary-skelter.unburger-assets")
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p
		}
	}
	p := filepath.Join(".local", "release", "mary-skelter.unburger-assets")
	if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
		return p
	}
	return ""
}

func assetKey(override string) ([]byte, error) {
	if override != "" {
		return os.ReadFile(override)
	}
	if key, ok, err := updater.EmbeddedAssetV3Key(); err != nil {
		return nil, err
	} else if ok {
		return key, nil
	}
	dev := filepath.Join(".local", "secrets", "unburger-assets.key")
	if info, err := os.Stat(dev); err == nil && info.Mode().IsRegular() {
		return os.ReadFile(dev)
	}
	return nil, fmt.Errorf("development runtime requires --assets-key or .local/secrets/unburger-assets.key")
}

func openAssets(path, keyOverride string) (*updater.ReleaseAssets, error) {
	if path == "" {
		path = defaultAssetsPath()
	}
	if path == "" {
		return nil, fmt.Errorf("mary-skelter.unburger-assets not found beside updater or under .local/release")
	}
	key, err := assetKey(keyOverride)
	if err != nil {
		return nil, err
	}
	return updater.OpenReleaseAssets(path, key)
}

func interactive(args []string) {
	fs := flag.NewFlagSet("ui", flag.ExitOnError)
	ms2, finale := gameFlags(fs)
	assetsPath := fs.String("assets", os.Getenv("MARY_SKELTER_ASSETS"), "single .unburger-assets v3 release container")
	assetsKey := fs.String("assets-key", os.Getenv("MARY_SKELTER_ASSETS_KEY"), "maintainer/dev raw key override; final release binaries use embedded key shares")
	backup := fs.String("backup-root", "", "optional persistent original-file backup directory")
	plain := fs.Bool("plain", false, "use text prompts instead of the interactive terminal UI")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %q", fs.Arg(0)))
	}

	detected := updater.DetectGames()
	if *ms2 == "" && len(detected["ms2"]) > 0 {
		*ms2 = detected["ms2"][0]
	}
	if *finale == "" && len(detected["finale"]) > 0 {
		*finale = detected["finale"][0]
	}
	assetLoader := func() (*updater.ReleaseAssets, error) {
		return openAssets(*assetsPath, *assetsKey)
	}
	uiVersion := version()
	if assets, err := assetLoader(); err == nil {
		if assets.Reader.Manifest.ReleaseVersion != "" {
			uiVersion = assets.Reader.Manifest.ReleaseVersion
		}
		_ = assets.Close()
	} else if embeddedVersion != "" {
		fatal(fmt.Errorf("cannot read release assets for version display: %w", err))
	}
	if err := ui.Run(ui.Options{Version: uiVersion, OpenAssets: assetLoader, BackupRoot: *backup, Roots: roots(*ms2, *finale), Plain: *plain}); err != nil {
		fatal(err)
	}
}

func version() string {
	if embeddedVersion != "" {
		return embeddedVersion
	}
	for _, p := range []string{"version.txt", filepath.Join("..", "version.txt")} {
		if b, err := os.ReadFile(p); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	return "dev"
}

func status(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	ms2, finale := gameFlags(fs)
	_ = fs.Parse(args)
	if fs.NArg() != 0 || (*ms2 == "" && *finale == "") {
		fatal(fmt.Errorf("status requires --ms2 and/or --finale"))
	}
	for key, root := range roots(*ms2, *finale) {
		p := updater.Profiles[key]
		st, err := updater.InspectGame(root, p)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s\n  root: %s\n  exe: %s (%s)\n", p.DisplayName, st.Root, st.ExeState, st.ExeSHA)
		for _, w := range st.LegacyWarnings {
			fmt.Printf("  WARNING: %s\n", w)
		}
	}
}

func runInstall(args []string, mode string) {
	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	ms2, finale := gameFlags(fs)
	assetsPath := fs.String("assets", os.Getenv("MARY_SKELTER_ASSETS"), "single .unburger-assets v3 release container")
	assetsKey := fs.String("assets-key", os.Getenv("MARY_SKELTER_ASSETS_KEY"), "maintainer/dev raw key override; final releases use embedded shares")
	backupRoot := fs.String("backup-root", "", "optional persistent original-file backup directory")
	backupOriginals := fs.Bool("backup-originals", false, "keep a persistent backup of original patched files (off by default)")
	receiptPath := fs.String("receipt", "", "write the completed run receipt here; default is the per-user state directory")
	dryRun := fs.Bool("dry-run", false, "validate and print without changing files")
	removeEnglish := fs.Bool("remove-english-assets", true, "purge all known English-dub assets after install/repair (default true); purge assets are never backed up")
	keepEnglish := fs.Bool("keep-english-assets", false, "keep known English-dub assets (disables the default purge)")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || (*ms2 == "" && *finale == "") {
		fatal(fmt.Errorf("%s requires --ms2 and/or --finale", mode))
	}
	assets, err := openAssets(*assetsPath, *assetsKey)
	if err != nil {
		fatal(err)
	}
	defer assets.Close()
	rs := roots(*ms2, *finale)
	plan, err := updater.BuildAssetPlan(assets, rs)
	if err != nil {
		fatal(err)
	}
	doCleanup := *removeEnglish && !*keepEnglish
	var cleanup updater.CleanupPlan
	var cleanupErr error
	if doCleanup {
		cleanup, cleanupErr = updater.BuildCleanupPlan(rs)
		if cleanupErr != nil {
			plan.Warnings = append(plan.Warnings, "English-dub cleanup preflight failed; the main patch will still install and cleanup will be skipped: "+cleanupErr.Error())
		}
	}
	fmt.Print(updater.FormatPlan(plan))
	if doCleanup && cleanupErr == nil {
		fmt.Print(updater.FormatCleanupPlan(cleanup))
	}
	if *dryRun {
		fmt.Println("DRY RUN: no files changed")
		return
	}
	keepBackup := *backupOriginals || *backupRoot != ""
	if keepBackup && *backupRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fatal(err)
		}
		*backupRoot = filepath.Join(home, "MarySkelterUnburgerBackups", time.Now().Format("20060102-150405"))
	}
	installBackupRoot := ""
	if keepBackup {
		installBackupRoot = *backupRoot
	}
	patchReceipt, err := updater.InstallAssets(plan, rs, installBackupRoot, false, assets)
	if err != nil {
		fatal(err)
	}
	var cleaned updater.CleanupReceipt
	var cleanupRunErr error
	if doCleanup && cleanupErr == nil {
		cleaned, cleanupRunErr = updater.RemoveEnglishAssets(cleanup, rs, "")
		if cleanupRunErr != nil {
			fmt.Printf("WARNING: main patch completed, but English-dub cleanup was incomplete after reclaiming %s: %v\n", updater.FormatBytes(cleaned.TotalBytes), cleanupRunErr)
		}
	} else if doCleanup {
		cleanupRunErr = cleanupErr
	}

	fmt.Print(updater.FormatRunSummary(patchReceipt, doCleanup, cleaned, cleanupRunErr))
	printSoftFailures(patchReceipt.SoftFailures)
	if keepBackup {
		fmt.Printf("Persistent original-file backup: %s\n", *backupRoot)
	} else {
		fmt.Println("Persistent original-file backup: not created (transaction rollback state was discarded after success)")
	}

	path := *receiptPath
	if path == "" {
		path, err = updater.DefaultRunReceiptPath(mode)
		if err != nil {
			fmt.Printf("WARNING: could not choose receipt path: %v\n", err)
		}
	}
	if path != "" {
		r := updater.NewRunReceipt(mode, version(), assets, rs, patchReceipt, doCleanup, cleaned, cleanupRunErr, installBackupRoot)
		if err := updater.WriteRunReceipt(path, r); err != nil {
			fmt.Printf("WARNING: install/repair completed, but receipt could not be written: %v\n", err)
		} else {
			fmt.Printf("Receipt: %s\n", path)
		}
	}
}

func parseSavePlatform(s string) (updater.SavePlatform, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pc", "windows", "steam":
		return updater.SavePlatformPC, nil
	case "switch", "nx":
		return updater.SavePlatformSwitch, nil
	default:
		return "", fmt.Errorf("unknown save platform %q (use pc or switch)", s)
	}
}

func convertSave(args []string) {
	fs := flag.NewFlagSet("convert-save", flag.ExitOnError)
	game := fs.String("game", "ms2", "save format: ms2 (Finale support is not yet proven)")
	fromRaw := fs.String("from", "", "source platform: switch or pc")
	toRaw := fs.String("to", "", "target platform: pc or switch")
	input := fs.String("input", "", "directory containing one SaveDataBE triplet")
	output := fs.String("output", "", "separate output directory")
	overwrite := fs.Bool("overwrite", false, "overwrite converted output files if they already exist")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *input == "" || *output == "" || *fromRaw == "" || *toRaw == "" {
		fatal(fmt.Errorf("convert-save requires --from, --to, --input and --output"))
	}
	if strings.ToLower(strings.TrimSpace(*game)) != "ms2" {
		fatal(fmt.Errorf("save conversion is currently proven only for Mary Skelter 2"))
	}
	from, err := parseSavePlatform(*fromRaw)
	if err != nil {
		fatal(err)
	}
	to, err := parseSavePlatform(*toRaw)
	if err != nil {
		fatal(err)
	}
	report, err := updater.ConvertMS2Save(*input, *output, from, to, *overwrite)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("PASS: Mary Skelter 2 save conversion %s complete.\n", report.Direction)
	fmt.Printf("  output: %s\n", report.OutputDirectory)
	fmt.Printf("  old main MD5: %s\n", report.OldMainMD5)
	fmt.Printf("  new main MD5: %s\n", report.NewMainMD5)
	fmt.Println("  SystemData_0000.* was not generated or replaced.")
}

func verifySave(args []string) {
	fs := flag.NewFlagSet("verify-save", flag.ExitOnError)
	game := fs.String("game", "ms2", "save format: ms2")
	platformRaw := fs.String("platform", "", "save platform: switch or pc")
	input := fs.String("input", "", "directory containing one SaveDataBE triplet")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *input == "" || *platformRaw == "" {
		fatal(fmt.Errorf("verify-save requires --platform and --input"))
	}
	if strings.ToLower(strings.TrimSpace(*game)) != "ms2" {
		fatal(fmt.Errorf("save verification is currently proven only for Mary Skelter 2"))
	}
	platform, err := parseSavePlatform(*platformRaw)
	if err != nil {
		fatal(err)
	}
	if err := updater.VerifyMS2SaveDirectory(*input, platform); err != nil {
		fatal(err)
	}
	fmt.Printf("PASS: Mary Skelter 2 %s save triplet is structurally valid.\n", platform)
}

func cleanupAudit(args []string) {
	fs := flag.NewFlagSet("cleanup-audit", flag.ExitOnError)
	ms2, finale := gameFlags(fs)
	jsonOut := fs.String("json", "", "write the exact candidate inventory as JSON")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || (*ms2 == "" && *finale == "") {
		fatal(fmt.Errorf("cleanup-audit requires --ms2 and/or --finale"))
	}
	audit, err := updater.AuditCleanupCandidates(roots(*ms2, *finale))
	if err != nil {
		fatal(err)
	}
	for _, f := range audit.Files {
		fmt.Printf("%-6s %12d  %s  %s\n", f.GameKey, f.Size, f.SHA256, f.RelativePath)
	}
	fmt.Printf("TOTAL: %d bytes across %d candidate file(s)\n", audit.TotalBytes, len(audit.Files))
	if *jsonOut != "" {
		raw, err := audit.JSON()
		if err != nil {
			fatal(err)
		}
		if err := os.WriteFile(*jsonOut, append(raw, '\n'), 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("JSON: %s\n", *jsonOut)
	}
	if len(audit.Files) > 0 {
		fmt.Println("AUDIT ONLY: no files were removed. Normal install/UI selects the verified English-dub purge by default; users may opt out.")
	}
}

func buildGDATPayload(args []string) {
	fs := flag.NewFlagSet("build-gdat-payload", flag.ExitOnError)
	sourceCache := fs.String("source-cache", filepath.Join(".local", "source_cache"), "persistent pristine source cache")
	bundleRoot := fs.String("bundle-root", filepath.Join(".local", "dist"), "verified canonical DAT bundle root")
	out := fs.String("out", filepath.Join(".local", "gdat_payload"), "output directory for source-pinned GDAT entry payload")
	lineagePath := fs.String("lineage", filepath.Join("config", "content_lineage.json"), "public prior-content lineage manifest")
	ms2 := fs.Bool("ms2", true, "build Mary Skelter 2 GDAT entry payload")
	finale := fs.Bool("finale", true, "build Mary Skelter Finale GDAT entry payload")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || (!*ms2 && !*finale) {
		fatal(fmt.Errorf("build-gdat-payload accepts no positional arguments and requires at least one game"))
	}
	var keys []string
	if *ms2 {
		keys = append(keys, "ms2")
	}
	if *finale {
		keys = append(keys, "finale")
	}
	if err := os.RemoveAll(*out); err != nil {
		fatal(err)
	}
	fmt.Println("Building source-pinned GDAT entry payload...")
	lineage, err := updater.LoadContentLineage(*lineagePath)
	if err != nil {
		fatal(fmt.Errorf("load public content lineage: %w", err))
	}
	manifest, err := updater.BuildGDATPatchPayload(*sourceCache, *bundleRoot, *out, keys, &lineage)
	if err != nil {
		fatal(err)
	}
	for _, a := range manifest.Archives {
		raw := int64(0)
		for _, e := range a.ChangedEntries {
			raw += e.TargetSize
		}
		ratio := float64(0)
		if a.CanonicalTargetSize > 0 {
			ratio = float64(a.StoredBytes) * 100 / float64(a.CanonicalTargetSize)
		}
		fmt.Printf("%-6s %-34s entries=%4d  stored=%9s  changed=%9s  %5.2f%% of target\n",
			a.GameKey, a.RelativePath, len(a.ChangedEntries), updater.FormatBytes(a.StoredBytes), updater.FormatBytes(raw), ratio)
	}
	fmt.Printf("TOTAL STRUCTURAL PAYLOAD: %s (%d archive(s))\n", updater.FormatBytes(manifest.TotalStoredBytes), len(manifest.Archives))
	fmt.Printf("Manifest: %s\n", filepath.Join(*out, "GDAT_PATCH_MANIFEST.json"))
	fmt.Println("Each payload was rebuilt against the canonical pristine source and exact target SHA-verified.")
	fmt.Println("Unrelated GDAT entries are not part of the payload and are preserved from the user's installed archive.")
}

func buildAssetsV3(args []string) {
	fs := flag.NewFlagSet("build-assets-v3", flag.ExitOnError)
	stage := fs.String("asset-stage", filepath.Join(".local", "release_asset_stage"), "single release asset staging root")
	out := fs.String("out", filepath.Join(".local", "release", "mary-skelter.unburger-assets"), "output .unburger-assets v3 container")
	keyPath := fs.String("key", filepath.Join(".local", "secrets", "unburger-assets.key"), "32-byte maintainer build key")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("build-assets-v3 accepts no positional arguments"))
	}
	m, err := updater.BuildAssetContainerV3(*stage, *out, *keyPath)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("UNBURGER-ASSETS V3: PASS (%d objects, %s plain)\n", len(m.Objects), updater.FormatBytes(m.TotalPlainBytes))
	fmt.Printf("Container: %s\nBuild key: %s (private; never publish it)\n", *out, *keyPath)
}

func buildRemuxTargets(args []string) {
	fs := flag.NewFlagSet("build-remux-targets", flag.ExitOnError)
	stageRoot := fs.String("asset-stage", filepath.Join(".local", "release_asset_stage"), "single .unburger-assets v3 staging root")
	sourceMovies := fs.String("source-movies", filepath.Join(".local", "movie_audio_source_cache", "finale", "MOVIE_EN"), "cached pristine Finale MOVIE_EN directory")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("build-remux-targets accepts no positional arguments"))
	}
	fmt.Println("Finalizing deterministic native-Go Finale remux target metadata...")
	m, err := updater.BuildFinaleRemuxTargets(*stageRoot, *sourceMovies)
	if err != nil {
		fatal(err)
	}
	count := 0
	for _, o := range m.Objects {
		if o.Kind != "movie_jp_audio" {
			continue
		}
		count++
		fmt.Printf("%-12s target=%s size=%s\n", filepath.Base(o.TargetMovie), o.RemuxTargetSHA256[:12], updater.FormatBytes(o.RemuxTargetSize))
	}
	fmt.Printf("FINALE REMUX TARGET METADATA: PASS (%d/%d)\n", count, 18)
	fmt.Printf("Manifest: %s\n", filepath.Join(*stageRoot, "ASSET_STAGE_MANIFEST.json"))
}

func buildRelease(args []string) {
	fs := flag.NewFlagSet("build-release", flag.ExitOnError)
	assets := fs.String("assets", filepath.Join(".local", "release", "mary-skelter.unburger-assets"), "verified .unburger-assets v3 sidecar")
	key := fs.String("key", filepath.Join(".local", "secrets", "unburger-assets.key"), "private 32-byte maintainer build key")
	out := fs.String("out", filepath.Join(".local", "release", "package"), "output end-user release directory")
	releaseVersion := fs.String("version", "", "release version override; default root version.txt")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("build-release accepts no positional arguments"))
	}
	r, err := updater.BuildUpdaterRelease(*assets, *key, *out, *releaseVersion)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("END-USER RELEASE: PASS (%s)\n", r.Version)
	fmt.Printf("Linux:   %s\nWindows: %s\nAssets:  %s\nSHA256:  %s\n", r.LinuxBinary, r.WindowsBinary, r.Assets, r.Checksums)
	fmt.Println("Raw asset key: not included; both binaries reconstruct it from linker-injected XOR/permutation shares.")
}

func verifyAssetsV3(args []string) {
	fs := flag.NewFlagSet("verify-assets-v3", flag.ExitOnError)
	path := fs.String("assets", os.Getenv("MARY_SKELTER_ASSETS"), "single .unburger-assets v3 release container")
	keyOverride := fs.String("assets-key", os.Getenv("MARY_SKELTER_ASSETS_KEY"), "maintainer/dev raw key override")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("verify-assets-v3 accepts no positional arguments"))
	}
	a, err := openAssets(*path, *keyOverride)
	if err != nil {
		fatal(err)
	}
	defer a.Close()
	if err := updater.VerifyAssetContainerV3(a.Path, func() []byte { k, _ := assetKey(*keyOverride); return k }()); err != nil {
		fatal(err)
	}
	fmt.Printf("UNBURGER-ASSETS V3 RUNTIME VERIFY: PASS (%d objects)\n", len(a.Reader.Manifest.Objects))
}

func buildDeltas(args []string) {
	fs := flag.NewFlagSet("build-deltas", flag.ExitOnError)
	sourceCache := fs.String("source-cache", filepath.Join(".local", "source_cache"), "persistent pristine source cache")
	bundleRoot := fs.String("bundle-root", filepath.Join(".local", "dist"), "verified canonical DAT bundle root")
	out := fs.String("out", filepath.Join(".local", "delta_payload"), "output directory for compact DAT delta payload")
	ms2 := fs.Bool("ms2", true, "build Mary Skelter 2 DAT deltas")
	finale := fs.Bool("finale", true, "build Mary Skelter Finale DAT deltas")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || (!*ms2 && !*finale) {
		fatal(fmt.Errorf("build-deltas accepts no positional arguments and requires at least one game"))
	}
	var keys []string
	if *ms2 {
		keys = append(keys, "ms2")
	}
	if *finale {
		keys = append(keys, "finale")
	}
	if err := os.RemoveAll(*out); err != nil {
		fatal(err)
	}
	fmt.Println("Building compact DAT delta payload...")
	manifest, err := updater.BuildDeltaPayload(*sourceCache, *bundleRoot, *out, keys)
	if err != nil {
		fatal(err)
	}
	for _, e := range manifest.Entries {
		ratio := float64(e.Stats.PatchBytes) * 100 / float64(e.Header.TargetSize)
		fmt.Printf("%-6s %-34s %10d bytes  %5.2f%% of target  copied=%s literal=%s\n",
			e.GameKey, e.RelativePath, e.Stats.PatchBytes, ratio,
			updater.FormatBytes(e.Stats.CopiedBytes), updater.FormatBytes(e.Stats.LiteralBytes))
	}
	fmt.Printf("TOTAL DELTA PAYLOAD: %s (%d file(s))\n", updater.FormatBytes(manifest.TotalBytes), len(manifest.Entries))
	fmt.Printf("Manifest: %s\n", filepath.Join(*out, "DAT_DELTA_MANIFEST.json"))
	fmt.Println("Each delta was immediately reconstructed and SHA-verified against the canonical DAT target.")
}

func printSoftFailures(failures []updater.OperationFailure) {
	if len(failures) == 0 {
		return
	}
	fmt.Printf("WARNING: %d non-critical movie operation(s) did not complete:\n", len(failures))
	for _, f := range failures {
		fmt.Printf("  - %s: %s\n", f.RelativePath, f.Error)
	}
	fmt.Println("The rest of the patch remains installed. Re-running the updater retries unresolved movies.")
}

func fatal(err error) { fmt.Fprintln(os.Stderr, "ERROR:", err); os.Exit(1) }
func usage() {
	fmt.Println(`Mary Skelter Unburger updater

Usage:
  mary-skelter-updater                           interactive installer + save tools
  mary-skelter-updater ui [--plain] [options]   guided installer (TTY also exposes save tools)
  mary-skelter-updater status  --ms2 <dir> [--finale <dir>]
  mary-skelter-updater install [--assets <file>] --ms2 <dir> [--finale <dir>] [--backup-originals] [--backup-root <dir>] [--receipt <file>] [--remove-english-assets=false|--keep-english-assets] [--dry-run]
  mary-skelter-updater repair  [--assets <file>] --ms2 <dir> [--finale <dir>] [same options as install]
  mary-skelter-updater cleanup-audit --ms2 <dir> [--finale <dir>] [--json <file>]
  mary-skelter-updater convert-save --game ms2 --from switch|pc --to pc|switch --input <dir> --output <dir> [--overwrite]
  mary-skelter-updater verify-save --game ms2 --platform switch|pc --input <dir>
  mary-skelter-updater build-gdat-payload [--source-cache <dir>] [--bundle-root <dir>] [--out <dir>]
  mary-skelter-updater build-remux-targets [--asset-stage <dir>] [--source-movies <dir>]
  mary-skelter-updater build-assets-v3 [--asset-stage <dir>] [--out <file>] [--key <file>]
  mary-skelter-updater verify-assets-v3 [--assets <file>] [--assets-key <file>]
  mary-skelter-updater build-release [--assets <file>] [--key <file>] [--out <dir>] [--version <v>]
  mary-skelter-updater build-deltas [--source-cache <dir>] [--bundle-root <dir>] [--out <dir>]  # exact-build fallback

Environment: MARY_SKELTER_MS2_DIR, MARY_SKELTER_FINALE_DIR, MARY_SKELTER_ASSETS, MARY_SKELTER_ASSETS_KEY, MARY_SKELTER_STEAM_DIR`)
}
