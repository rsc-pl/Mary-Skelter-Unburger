package builderui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/switchrelease"
	"maryskelter-unburger/internal/tui"
)

type Options struct {
	Root    string
	Version string
	Plain   bool
}

type action struct {
	Name string
	Desc string
	Run  func(Options) error
}

func availableActions() []action {
	return []action{
		{"Project status", "Show source/cache/build/release readiness", showStatus},
		{"Configure source paths", "Point builder at owned MS2 + Finale PC installs", configureSources},
		{"Acquire / refresh movie sources", "Guided Finale English/Japanese Steam-language cache", guidedMovies},
		{"Verify project", "Run full project regression/consistency checks", verifyProject},
		{"Rebuild canonical PC patch bundles", "Rebuild text/graphics/QoL bundles from source cache", rebuildProject},
		{"Prepare release assets", "GDAT structural payload + movie payload + one v3 stage", prepareAssets},
		{"Build / verify .unburger-assets v3", "Create the one end-user asset container", buildContainer},
		{"Build end-user release", "Linux + Windows updater + sidecar + SHA256SUMS", buildRelease},
		{"Switch: source/cache status", "Verify latest-update source archive fingerprints", switchSourceStatus},
		{"Switch: import missing pristine sources", "Reuse the 11 original text/graphics GDATs (no NSP re-extraction)", switchSourceImport},
		{"Switch: translated text + graphics + IPS candidate", "Build both games using canonical EN; runtime approval still pending", switchIntegratedWizard},
		{"Switch: graphics-only diagnostic ZIPs", "Graphics and IPS only; NOT a complete translation", switchReleaseWizard},
		{"Full PC release wizard", "Verify → rebuild → assets → container → PC ZIP", fullWizard},
		{"Full PC + Switch release wizard", "PC ZIP + integrated Switch Atmosphere/Eden test ZIPs in one folder", fullAllVersionsWizard},
		{"Quit", "", nil},
	}
}

func Run(o Options) error {
	plainDecisionMode = o.Plain
	actions := availableActions()
	if o.Plain || !isInteractive() {
		return runPlain(o, actions)
	}
	idx := 0
	for {
		chosen, quit, err := chooseTTY(o, actions, idx)
		if err != nil {
			return err
		}
		if quit || chosen == len(actions)-1 {
			return nil
		}
		idx = chosen
		clear()
		renderHeader(o)
		fmt.Println(boxTop(actions[chosen].Name))
		fmt.Println(boxLine(actions[chosen].Desc))
		fmt.Println(boxBottom())
		fmt.Println()
		if err := actions[chosen].Run(o); err != nil {
			fmt.Printf("\n%s %v\n", red("ERROR:"), err)
		} else {
			fmt.Printf("\n%s\n", green("✓ Action completed"))
		}
		fmt.Print("\nPress Enter to return to the builder menu.")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
}

func chooseTTY(o Options, actions []action, idx int) (int, bool, error) {
	restore, err := rawMode()
	if err != nil {
		return 0, false, err
	}
	defer restore()
	for {
		clear()
		renderHeader(o)
		s := maintainer.Inspect(o.Root)
		renderReadiness(s)
		fmt.Println()
		entries := make([]tui.MenuItem, len(actions))
		for i, a := range actions {
			cat := "TOOL"
			switch {
			case strings.Contains(a.Name, "Switch:"):
				cat = "SWITCH"
			case strings.Contains(a.Name, "Verify"), strings.Contains(a.Name, "status"):
				cat = "VERIFY"
			case strings.Contains(a.Name, "source"), strings.Contains(a.Name, "Source"):
				cat = "SOURCE"
			case strings.Contains(a.Name, "PC"), strings.Contains(a.Name, "patch"):
				cat = "PC"
			case strings.Contains(a.Name, "release"), strings.Contains(a.Name, "assets"), strings.Contains(a.Name, "container"):
				cat = "RELEASE"
			case a.Name == "Quit":
				cat = "EXIT"
			}
			tip := "Maintainer-only operation. Review source paths and verification before building."
			if a.Name == "Quit" {
				tip = "Exit safely; no file changes."
			}
			entries[i] = tui.MenuItem{Title: a.Name, Detail: a.Desc, Category: cat, Safety: tip}
		}
		for _, line := range tui.MenuPanel(style(), "Maintainer workflow", entries, idx) {
			fmt.Println(line)
		}
		fmt.Println(dim("  ↑/↓ navigate   Enter run   Q/Esc quit"))
		k, err := readKey()
		if err != nil {
			return 0, false, err
		}
		switch k {
		case keyUp:
			idx = tui.Move(idx, -1, len(actions))
		case keyDown:
			idx = tui.Move(idx, 1, len(actions))
		case keyEnter:
			return idx, false, nil
		case keyQuit, keyEsc:
			return idx, true, nil
		}
	}
}

func renderHeader(o Options) {
	for _, line := range tui.Banner(style(), "MAINTAINER BUILDER", o.Version) {
		fmt.Println(line)
	}
	fmt.Printf("%s %s\n\n", dim("Project:"), dim(o.Root))
}

func renderReadiness(s maintainer.Status) {
	fmt.Println(boxTop("Project readiness"))
	for _, row := range statusRows(s) {
		fmt.Println(boxLine(row))
	}
	fmt.Println(boxRule())
	done, total := readiness(s)
	fmt.Println(boxLine(fmt.Sprintf("Overall readiness  %s  %d/%d checks", progressBar(done, total, 38), done, total)))
	if len(s.MissingTools) > 0 {
		fmt.Println(boxLine(yellow("Missing tools: ") + strings.Join(s.MissingTools, ", ")))
	}
	fmt.Println(boxBottom())
}

func runPlain(o Options, actions []action) error {
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("MARY SKELTER UNBURGER — MAINTAINER BUILDER  v%s\n\n", o.Version)
		for i, a := range actions {
			fmt.Printf("%2d. %s\n", i+1, a.Name)
		}
		fmt.Print("Choose: ")
		if !in.Scan() {
			return nil
		}
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(in.Text()), "%d", &n); err != nil || n < 1 || n > len(actions) {
			fmt.Print("Invalid selection.\n\n")
			continue
		}
		if n == len(actions) {
			return nil
		}
		if err := actions[n-1].Run(o); err != nil {
			fmt.Printf("ERROR: %v\n", err)
		}
		fmt.Println()
	}
}

func showStatus(o Options) error {
	s := maintainer.Inspect(o.Root)
	fmt.Println(boxTop("Readiness detail"))
	for _, row := range statusRows(s) {
		fmt.Println(boxLine(row))
	}
	fmt.Println(boxRule())
	done, total := readiness(s)
	fmt.Println(boxLine(fmt.Sprintf("Overall readiness  %s  %d/%d checks", progressBar(done, total, 38), done, total)))
	if len(s.MissingTools) > 0 {
		fmt.Println(boxLine(yellow("Missing maintainer tools: ") + strings.Join(s.MissingTools, ", ")))
	}
	fmt.Println(boxBottom())
	return nil
}

func configureSources(o Options) error {
	in := bufio.NewReader(os.Stdin)
	fmt.Print("Mary Skelter 2 game directory: ")
	ms2, _ := in.ReadString('\n')
	fmt.Print("Mary Skelter Finale game directory: ")
	finale, _ := in.ReadString('\n')
	return maintainer.ConfigureSources(o.Root, strings.TrimSpace(ms2), strings.TrimSpace(finale), os.Stdout, os.Stderr)
}

func guidedMovies(o Options) error {
	if !maintainer.Inspect(o.Root).SourceConfig {
		return fmt.Errorf("configure source paths first")
	}
	if _, ready := maintainer.MovieCacheReady(o.Root); ready {
		fmt.Println("A complete verified movie cache already exists (22 Finale pairs / 18 JP payload objects).")
		if !yesNo("Refresh it anyway?", false) {
			return nil
		}
	}
	roots, err := maintainer.LoadSourceRoots(o.Root)
	if err != nil {
		return err
	}
	ms2EN := filepath.Join(roots.MS2, "masterWindows", "MOVIE_EN")
	ms2SUB := filepath.Join(roots.MS2, "masterWindows", "MOVIE_EN_SUB")
	if !isDir(ms2EN) || !isDir(ms2SUB) {
		fmt.Println("MS2 refresh needs the shipped MOVIE_EN and MOVIE_EN_SUB folders for read-only audit evidence.")
		fmt.Println("Restore/use a pristine MS2 source install, then return here. Existing completed cache can otherwise be reused without this step.")
		return fmt.Errorf("MS2 pristine movie source incomplete")
	}
	cen, cjp := maintainer.CachedFinaleMovieCounts(o.Root)
	fmt.Printf("Cached Finale sources: MOVIE_EN %d/22, MOVIE_JP %d/22\n", cen, cjp)
	if cen == 22 && cjp == 22 {
		fmt.Println("Both language source sets are already cached; no Steam language switch is needed.")
		return maintainer.RefreshMovieAudio(o.Root, os.Stdout, os.Stderr)
	}
	if cen != 22 {
		for {
			en, _, err := maintainer.CurrentFinaleMovieCounts(o.Root)
			if err != nil {
				return err
			}
			if en == 22 {
				break
			}
			waitInstruction("In Steam, set Mary Skelter Finale language to English and let the download finish. Press Enter to rescan.")
		}
		if _, err := maintainer.CaptureFinaleMovieSet(o.Root, "MOVIE_EN", os.Stdout); err != nil {
			return err
		}
		fmt.Println("Finale English movie set cached.")
	}
	if cjp != 22 {
		for {
			_, jp, err := maintainer.CurrentFinaleMovieCounts(o.Root)
			if err != nil {
				return err
			}
			if jp == 22 {
				break
			}
			waitInstruction("In Steam, temporarily set Mary Skelter Finale language to Japanese and let the download finish. Press Enter to rescan.")
		}
		if _, err := maintainer.CaptureFinaleMovieSet(o.Root, "MOVIE_JP", os.Stdout); err != nil {
			return err
		}
		fmt.Println("Finale Japanese movie set cached.")
	}
	fmt.Println("Both Finale movie source sets are now cached outside the Steam language state.")
	for {
		waitInstruction("Set Mary Skelter Finale back to English in Steam. After the download/state settles, press Enter to rescan.")
		en, _, err := maintainer.CurrentFinaleMovieCounts(o.Root)
		if err != nil {
			return err
		}
		if en == 22 {
			break
		}
		fmt.Printf("English movie set is not active yet (%d/22).\n", en)
	}
	fmt.Println("Comparing decoded audio and rebuilding the Finale-only JP audio payload...")
	return maintainer.RefreshMovieAudio(o.Root, os.Stdout, os.Stderr)
}

func verifyProject(o Options) error  { return maintainer.Verify(o.Root, false, os.Stdout, os.Stderr) }
func rebuildProject(o Options) error { return maintainer.Rebuild(o.Root, os.Stdout, os.Stderr) }
func prepareAssets(o Options) error {
	return maintainer.PrepareReleaseAssets(o.Root, false, os.Stdout, os.Stderr)
}
func buildContainer(o Options) error { return maintainer.BuildContainer(o.Root, os.Stdout) }
func buildRelease(o Options) error {
	overwrite, proceed, err := confirmReleaseReplacement(o, true, false, false)
	if err != nil || !proceed {
		return err
	}
	return maintainer.WithReleaseReplacement(o.Root, o.Version, true, false, false, overwrite, func() error {
		_, err := maintainer.BuildRelease(o.Root, "", os.Stdout)
		return err
	})
}

// confirmReleaseReplacement keeps the no-clobber default. Only an explicit
// arrow/Enter selection permits a deliberate rebuild at the same version.
func confirmReleaseReplacement(o Options, pc, atmosphere, eden bool) (bool, bool, error) {
	files, err := maintainer.ExistingReleasePackages(o.Root, o.Version, pc, atmosphere, eden)
	if err != nil {
		return false, false, err
	}
	if len(files) == 0 {
		return false, true, nil
	}
	fmt.Printf("\nExisting release archive(s):\n")
	for _, file := range files {
		if strings.HasSuffix(file, ".zip") {
			fmt.Printf("  %s\n", filepath.Base(file))
		}
	}
	ok := chooseBuildAction("Overwrite existing release ZIPs?", "Rebuild this same version. Previous ZIPs will be preserved in a private recovery directory; errors restore them.", false)
	if !ok {
		fmt.Println("Cancelled: existing packages unchanged.")
		return false, false, nil
	}
	return true, true, nil
}

func switchSourceStatus(o Options) error {
	showCombinedReadiness(o.Root)
	combined, err := switchrelease.InspectIntegratedCache(o.Root)
	if err != nil {
		return err
	}
	for _, game := range combined {
		if len(game.Missing) > 0 {
			fmt.Printf("%s: missing verified source files: %s\n", game.Game, strings.Join(game.Missing, ", "))
		}
	}
	return nil
}
func switchSourceImport(o Options) error {
	reader := bufio.NewReader(os.Stdin)
	_, _, err := prepareSwitchCache(o.Root, reader)
	return err
}

func confirmSwitchCandidate(_ *bufio.Reader, prompt string) bool {
	return chooseBuildAction(prompt, "Build the translated Switch test packages.", false)
}

// switchIntegratedWizard is the maintainer-facing entry point for source-bound
// translated Switch candidates. It requires complete original archives for
// both games and never silently falls back to graphics-only output.
// prepareSwitchCache reuses existing SHA-pinned local originals first. Only
// missing archives require a one-time explicit import; no NSP re-extraction.
func prepareSwitchCache(root string, in *bufio.Reader) (string, string, error) {
	states, err := switchrelease.InspectIntegratedCache(root)
	if err != nil {
		return "", "", err
	}
	missing := map[string]bool{}
	for _, st := range states {
		fmt.Printf("%s Switch originals: %d/%d verified in %s\n", st.Game, len(st.Verified), len(st.Verified)+len(st.Missing), st.CacheDir)
		if len(st.Missing) > 0 {
			fmt.Printf("  Missing: %s\n", strings.Join(st.Missing, ", "))
			missing[st.Game] = true
		}
	}
	if len(missing) == 0 {
		fmt.Println("Both Switch source caches complete. No masterNX64 paths or NSP extraction required.")
		return "", "", nil
	}
	// Try only documented, project-local migration locations before asking.
	if resolved, e := switchrelease.EnsureIntegratedSourceCache(root, "", ""); e == nil {
		fmt.Printf("Verified project-local originals imported: %d + %d files.\n", len(resolved[0].Verified), len(resolved[1].Verified))
		return "", "", nil
	}
	chosen := map[string]string{}
	for _, game := range []string{"ms2", "finale"} {
		if !missing[game] {
			continue
		}
		fmt.Printf("%s: path to your EXISTING extracted masterNX64 containing missing GDAT (blank to stop; do NOT re-extract NSP): ", game)
		line, e := in.ReadString('\n')
		if e != nil && e != io.EOF {
			return "", "", e
		}
		chosen[game] = strings.TrimSpace(line)
	}
	_, err = switchrelease.EnsureIntegratedSourceCache(root, chosen["ms2"], chosen["finale"])
	if err != nil {
		return "", "", err
	}
	fmt.Println("Original Switch inputs pinned and cached. Future builds do not require source paths.")
	return "", "", nil
}

func switchIntegratedWizard(o Options) error {
	in := bufio.NewReader(os.Stdin)
	fmt.Println("Switch candidate: canonical English text + graphical labels + IPS for BOTH games.")
	fmt.Println("Using SHA-verified private source cache; original source paths are optional.")
	ms2, finale, err := prepareSwitchCache(o.Root, in)
	if err != nil {
		return err
	}
	opts := switchrelease.IntegratedOptions{Options: switchrelease.Options{
		Root: o.Root, Version: o.Version, Candidate: true, Platform: "both", OutputDir: filepath.Join(o.Root, ".local", "release", "packages"), Stdout: os.Stdout, Stderr: os.Stderr,
		MS2Master: ms2, FinaleMaster: finale,
	}}
	fmt.Println(green("✓ Graphics are reused automatically only when their original inputs, fonts and recipes match."))
	fmt.Println("Combined Switch game/runtime QA is NOT yet approved. This produces TEST CANDIDATES only.")
	if !confirmSwitchCandidate(in, "Build the combined translated candidate for both games?") {
		return nil
	}
	overwrite, proceed, err := confirmReleaseReplacement(o, false, true, true)
	if err != nil || !proceed {
		return err
	}
	var result switchrelease.IntegratedResult
	err = maintainer.WithReleaseReplacement(o.Root, o.Version, false, true, true, overwrite, func() error {
		var buildErr error
		result, buildErr = switchrelease.BuildIntegratedCandidate(opts)
		return buildErr
	})
	if err != nil {
		return err
	}
	for _, game := range []string{"ms2", "finale"} {
		r := result.TextReports[game]
		fmt.Printf("%s: %d canonical text fields staged, %d edited; native GDAT report present\n", game, r.MappedFields, r.EditedFields)
	}
	for _, release := range result.Releases {
		fmt.Printf("%s integrated test ZIP: %s\nSHA256: %s\n", release.Platform, release.ZIP, release.SHA256)
	}
	if result.ReusableGraphicsZIP != "" {
		fmt.Println(green("✓ Rendered graphics saved for automatic reuse when only text changes."))
	}
	if result.GraphicsCacheWarning != "" {
		fmt.Printf("WARN: verified combined ZIPs were published, but graphics cache was not retained: %s\n", result.GraphicsCacheWarning)
	}
	return nil
}

func switchReleaseWizard(o Options) error {
	status, err := switchrelease.Status(o.Root, "", "")
	if err != nil {
		return err
	}
	for _, st := range status {
		if st.Error != "" {
			fmt.Printf("%s: %s\n", st.Game, st.Error)
			fmt.Println("Import verified source archives first (Switch: import extracted sources).")
			return fmt.Errorf("Switch sources not ready")
		}
	}
	fmt.Println("R32C3 V18 graphics still await final visual/runtime approval.")
	fmt.Println("Switch translated text is NOT yet integrated: these ZIPs contain graphics + IPS only.")
	if !yesNo("Build a candidate Switch release ZIP anyway?", false) {
		return nil
	}
	results, err := switchrelease.Build(switchrelease.Options{Root: o.Root, Version: o.Version, Candidate: true, Platform: "both", Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Printf("%s candidate ZIP: %s\nSHA256: %s\n", result.Platform, result.ZIP, result.SHA256)
	}
	return nil
}

// fullAllVersionsWizard first validates both Switch masters, then builds the PC
// release and two integrated Switch candidates under one output directory.
// It never falls back to graphics-only packages if text integration fails.
func fullAllVersionsWizard(o Options) error {
	in := bufio.NewReader(os.Stdin)
	fmt.Println("Complete output: PC ZIP + Switch Atmosphere/Eden test ZIPs.")
	fmt.Println("All finished archives: .local/release/packages")
	fmt.Println("Checking cached pristine Switch sources first; no re-extraction is requested.")
	ms2, finale, err := prepareSwitchCache(o.Root, in)
	if err != nil {
		return err
	}
	if !maintainer.Inspect(o.Root).SourceConfig {
		fmt.Println("PC source paths are not configured. Enter the two owned PC game installs.")
		if err := configureSources(o); err != nil {
			return err
		}
	}
	if !chooseBuildActionWithReadiness("Full PC + Switch release", "Build PC and two integrated Switch candidates, with automatically verified graphics reuse.", false, o.Root) {
		return nil
	}
	overwrite, proceed, err := confirmReleaseReplacement(o, true, true, true)
	if err != nil || !proceed {
		return err
	}
	fmt.Println(green("✓ Starting complete PC + Switch workflow; graphic reuse is automatic and input-verified."))
	err = maintainer.WithReleaseReplacement(o.Root, o.Version, true, true, true, overwrite, func() error {
		if err := maintainer.RunAll(o.Root, false, "", os.Stdout, os.Stderr); err != nil {
			return err
		}
		result, err := switchrelease.BuildIntegratedCandidate(switchrelease.IntegratedOptions{
			Options: switchrelease.Options{Root: o.Root, Version: o.Version, MS2Master: ms2, FinaleMaster: finale, Candidate: true, Platform: "both", OutputDir: filepath.Join(o.Root, ".local", "release", "packages"), Stdout: os.Stdout, Stderr: os.Stderr},
		})
		if err != nil {
			return err
		}
		if len(result.Releases) != 2 || len(result.TextReports) != 2 {
			return fmt.Errorf("incomplete combined Switch output")
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("3 ZIPs published in %s\n", filepath.Join(o.Root, ".local", "release", "packages"))
	return nil
}

// releaseStages describes actions, not previously cached artifacts or owner
// runtime acceptance. Both games share source validation and staging.
func releaseStages() []tui.StageRow {
	return []tui.StageRow{
		{Title: "Verify project and source paths", Detail: "Check source configuration, technical records and build prerequisites before any writes.", Safety: "Read-only checks; stop on errors."},
		{Title: "Rebuild canonical PC patches", Detail: "Rebuild translations, graphics and executable payloads for Mary Skelter 2 and Finale.", Safety: "Writes private staging. Python/Pillow are still used; FFmpeg is allowed."},
		{Title: "Prepare release assets", Detail: "Validate or acquire cached audio/movie inputs, then prepare release payloads.", Safety: "Steam language acquisition may require interaction; no silent language switch."},
		{Title: "Build signed asset container", Detail: "Assemble and validate the single .unburger-assets v3 container.", Safety: "Do not publish source games or original private fonts."},
		{Title: "Build end-user release", Detail: "Package Linux and Windows patchers with checksums for both supported PC games.", Safety: "Only package release output after all earlier stages pass."},
	}
}
func drawReleaseStages(o Options, completed int, note string, blocked bool) {
	steps := tui.StagesFromProgress(releaseStages(), completed, blocked)
	active := completed
	if active >= len(steps) {
		active = len(steps) - 1
	}
	for _, line := range tui.StagePanel(style(), "PC RELEASE WORKFLOW · BOTH GAMES", steps, active, note) {
		fmt.Println(line)
	}
}
func fullWizard(o Options) error {
	s := maintainer.Inspect(o.Root)
	fmt.Println()
	drawReleaseStages(o, 0, "PREVIEW · nothing executed yet", false)
	fmt.Println()
	if len(s.MissingTools) > 0 {
		return fmt.Errorf("missing maintainer tools: %s", strings.Join(s.MissingTools, ", "))
	}
	if !s.SourceConfig {
		fmt.Println(yellow("Source paths not configured; enter owned game paths first."))
		if err := configureSources(o); err != nil {
			return err
		}
	}
	if !yesNo("Proceed with verification and the complete PC release workflow?", false) {
		return nil
	}
	overwrite, proceed, err := confirmReleaseReplacement(o, true, false, false)
	if err != nil || !proceed {
		return err
	}
	steps := releaseStages()
	runStep := func(index int, label string, fn func() error) error {
		fmt.Println()
		drawReleaseStages(o, index, label, false)
		if err := fn(); err != nil {
			fmt.Println()
			drawReleaseStages(o, index, "STOPPED: "+err.Error(), true)
			return fmt.Errorf("stage %d/%d (%s): %w", index+1, len(steps), steps[index].Title, err)
		}
		return nil
	}
	if err := runStep(0, "Verify before changing staged outputs", func() error { return maintainer.Verify(o.Root, false, os.Stdout, os.Stderr) }); err != nil {
		return err
	}
	if !yesNo("Rebuild canonical PC patch bundles?", true) {
		return nil
	}
	if err := runStep(1, "Rebuilding both canonical PC bundles", func() error { return maintainer.Rebuild(o.Root, os.Stdout, os.Stderr) }); err != nil {
		return err
	}
	if err := runStep(2, "Preparing release assets", func() error {
		if _, ready := maintainer.MovieCacheReady(o.Root); !ready {
			fmt.Println("Movie cache incomplete; guided source acquisition required.")
			if err := guidedMovies(o); err != nil {
				return err
			}
		} else {
			fmt.Println(green("✓ Reusing verified movie cache."))
		}
		return maintainer.PrepareReleaseAssets(o.Root, false, os.Stdout, os.Stderr)
	}); err != nil {
		return err
	}
	if err := runStep(3, "Building .unburger-assets v3", func() error { return maintainer.BuildContainer(o.Root, os.Stdout) }); err != nil {
		return err
	}
	if err := runStep(4, "Building both end-user binaries and checksums", func() error {
		return maintainer.WithReleaseReplacement(o.Root, o.Version, true, false, false, overwrite, func() error {
			_, err := maintainer.BuildRelease(o.Root, "", os.Stdout)
			return err
		})
	}); err != nil {
		return err
	}
	fmt.Println()
	drawReleaseStages(o, len(steps), "All five maintainer stages returned success. In-game QA remains separate.", false)
	fmt.Println(green("✓ MAINTAINER RELEASE WORKFLOW: PASS (not game runtime certification)"))
	return nil
}

func yesNo(prompt string, def bool) bool {
	return chooseBuildAction(prompt, "Proceed with the selected maintainer action.", def)
}

func waitInstruction(s string) {
	fmt.Println("\n" + s)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
func mark(v bool) string {
	if v {
		return "✓"
	}
	return "—"
}
func clear() { fmt.Print("\x1b[2J\x1b[H") }
func isInteractive() bool {
	i, _ := os.Stdin.Stat()
	o, _ := os.Stdout.Stat()
	return i.Mode()&os.ModeCharDevice != 0 && o.Mode()&os.ModeCharDevice != 0
}
