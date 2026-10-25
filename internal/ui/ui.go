package ui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"maryskelter-unburger/internal/tui"
	"maryskelter-unburger/internal/updater"
)

type Options struct {
	Version, BackupRoot string
	Roots               map[string]string
	Assets              *updater.ReleaseAssets
	OpenAssets          func() (*updater.ReleaseAssets, error)
	Plain               bool
}

type item struct {
	key, name, root, state string
	selected               bool
}

const workflowStages = 5

func Run(o Options) error {
	if o.Plain || !isInteractive() {
		var closeAssets func() error
		if o.Assets == nil {
			if o.OpenAssets == nil {
				return fmt.Errorf("release assets are required for install mode")
			}
			a, err := o.OpenAssets()
			if err != nil {
				return err
			}
			o.Assets = a
			closeAssets = a.Close
			defer closeAssets()
		}
		items, err := makeItems(o.Roots)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return fmt.Errorf("no supported Mary Skelter installation detected")
		}
		return runPlain(o, items)
	}
	for {
		action, err := mainMenuTTY(o)
		if err != nil {
			return err
		}
		switch action {
		case "install":
			if o.Assets == nil {
				if o.OpenAssets == nil {
					return fmt.Errorf("release assets are required for install mode")
				}
				a, err := o.OpenAssets()
				if err != nil {
					return err
				}
				o.Assets = a
				defer a.Close()
			}
			items, err := makeItems(o.Roots)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return fmt.Errorf("no supported Mary Skelter installation detected")
			}
			return runTTY(o, items)
		case "save":
			return runSaveConverter(o)
		case "quit":
			clear()
			return nil
		}
	}
}

func mainMenuTTY(o Options) (string, error) {
	restore, err := rawMode()
	if err != nil {
		return "", err
	}
	defer restore()
	items := []struct{ action, title, detail string }{
		{"install", "Install / Update / Repair Mod", "Detect installed games, verify managed state and apply only required changes."},
		{"save", "Convert Mary Skelter 2 Save", "Convert SaveDataBE safely in either direction: Nintendo Switch ↔ PC."},
		{"quit", "Quit", "Exit without changing anything."},
	}
	idx := 0
	for {
		clear()
		renderHeader(o, "MAIN MENU")
		entries := make([]tui.MenuItem, len(items))
		for i, it := range items {
			cat, safety := "PATCH", "The installer verifies inputs and preserves user data."
			if it.action == "save" {
				cat, safety = "SAVE", "Conversion never overwrites the input save or platform SystemData files."
			}
			if it.action == "quit" {
				cat, safety = "EXIT", "Exit without changing anything."
			}
			entries[i] = tui.MenuItem{Title: it.title, Detail: it.detail, Category: cat, Safety: safety}
		}
		for _, line := range tui.MenuPanel(style(), "Choose an action", entries, idx) {
			fmt.Println(line)
		}
		if items[idx].action == "save" {
			fmt.Println(dim("  Source files are never modified; SystemData_0000.* is never converted."))
		}
		fmt.Println(dim("  ↑/↓ move   Enter select   Q quit"))
		k, err := readKey()
		if err != nil {
			return "", err
		}
		switch k {
		case keyUp:
			idx = tui.Move(idx, -1, len(items))
		case keyDown:
			idx = tui.Move(idx, 1, len(items))
		case keyEnter:
			return items[idx].action, nil
		case keyQuit, keyEsc:
			return "quit", nil
		}
	}
}

func chooseSaveDirectionTTY(o Options) (updater.SavePlatform, updater.SavePlatform, bool, error) {
	restore, err := rawMode()
	if err != nil {
		return "", "", false, err
	}
	defer restore()
	idx := 0
	rows := []struct {
		from, to      updater.SavePlatform
		title, detail string
	}{
		{updater.SavePlatformSwitch, updater.SavePlatformPC, "Nintendo Switch → PC", "Produces native Ghostlight/Steam SaveDataBE files with validated PC wrappers."},
		{updater.SavePlatformPC, updater.SavePlatformSwitch, "PC → Nintendo Switch", "Removes only proven x64 padding/wrappers and rebuilds the Switch main-save MD5."},
	}
	for {
		clear()
		renderHeader(o, "MS2 SAVE CONVERTER")
		fmt.Println(boxTop("Conversion direction"))
		for i, row := range rows {
			line := "   " + row.title
			if i == idx {
				line = inverse(" ▶ " + padRight(row.title, 76))
			}
			fmt.Println(boxLine(line))
		}
		fmt.Println(boxRule())
		fmt.Println(boxLine(dim(rows[idx].detail)))
		fmt.Println(boxLine(dim("The converter handles SaveDataBE.* only. Keep platform SystemData_0000.* files.")))
		fmt.Println(boxBottom())
		fmt.Println(dim("  ↑/↓ move   Enter select   Esc/Q back"))
		k, e := readKey()
		if e != nil {
			return "", "", false, e
		}
		switch k {
		case keyUp:
			if idx > 0 {
				idx--
			}
		case keyDown:
			if idx < len(rows)-1 {
				idx++
			}
		case keyEnter:
			return rows[idx].from, rows[idx].to, true, nil
		case keyEsc, keyQuit:
			return "", "", false, nil
		}
	}
}

func runSaveConverter(o Options) error {
	from, to, ok, err := chooseSaveDirectionTTY(o)
	if err != nil || !ok {
		return err
	}
	clear()
	renderHeader(o, "MS2 SAVE CONVERTER")
	fmt.Println(boxTop("Safe conversion"))
	fmt.Println(boxLine(fmt.Sprintf("Direction                  %s → %s", strings.ToUpper(string(from)), strings.ToUpper(string(to)))))
	fmt.Println(boxLine("Input must contain exactly one SaveDataBE_XXXX.sav triplet."))
	fmt.Println(boxLine("Source files will not be modified. Output must be a separate directory."))
	fmt.Println(boxLine("SystemData_0000.* is intentionally ignored on both platforms."))
	fmt.Println(boxBottom())
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("\nSource directory: ")
	in, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	in = strings.TrimSpace(in)
	if in == "" {
		return fmt.Errorf("source directory is required")
	}
	absIn, _ := filepath.Abs(in)
	suffix := "-pc"
	if to == updater.SavePlatformSwitch {
		suffix = "-switch"
	}
	defOut := filepath.Join(filepath.Dir(absIn), filepath.Base(absIn)+suffix)
	fmt.Printf("Output directory [%s]: ", defOut)
	out, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		out = defOut
	}
	report, err := updater.ConvertMS2Save(in, out, from, to, false)
	if err != nil {
		fmt.Println()
		fmt.Println(boxTop("Conversion failed"))
		fmt.Println(boxLine(red("ERROR: ") + err.Error()))
		fmt.Println(boxBottom())
		return err
	}
	fmt.Println()
	fmt.Println(green(boxTop("SAVE CONVERSION COMPLETE")))
	fmt.Println(boxLine(green("✓ Output verified as a valid " + strings.ToUpper(string(to)) + " SaveDataBE triplet")))
	fmt.Println(boxLine("Output                    " + report.OutputDirectory))
	fmt.Println(boxLine("Old main MD5              " + report.OldMainMD5))
	fmt.Println(boxLine("New main MD5              " + report.NewMainMD5))
	fmt.Println(boxRule())
	fmt.Println(boxLine("conversion_report.json was written beside the converted files."))
	fmt.Println(boxLine("SystemData_0000.* was not generated or replaced."))
	fmt.Println(green(boxBottom()))
	return nil
}

func makeItems(roots map[string]string) ([]item, error) {
	order := []string{"ms2", "finale"}
	var out []item
	for _, key := range order {
		root := roots[key]
		if root == "" {
			continue
		}
		p := updater.Profiles[key]
		st, err := updater.InspectGame(root, p)
		if err != nil {
			return nil, err
		}
		out = append(out, item{key: key, name: p.DisplayName, root: root, state: st.ExeState, selected: true})
	}
	return out, nil
}

func runTTY(o Options, items []item) error {
	restore, err := rawMode()
	if err != nil {
		return runPlain(o, items)
	}
	defer restore()
	idx := 0
	for {
		drawSelection(o, items, idx, "")
		k, err := readKey()
		if err != nil {
			return err
		}
		switch k {
		case keyUp:
			if idx > 0 {
				idx--
			}
		case keyDown:
			if idx < len(items)-1 {
				idx++
			}
		case keySpace:
			items[idx].selected = !items[idx].selected
		case keyQuit:
			clear()
			return nil
		case keyEnter:
			roots := selectedRoots(items)
			if len(roots) == 0 {
				drawSelection(o, items, idx, "Select at least one game.")
				time.Sleep(900 * time.Millisecond)
				continue
			}
			removeEnglish, keepBackup, back := configureOptions(o, roots)
			if back {
				continue
			}

			var last updater.ProgressEvent
			plan, err := updater.BuildAssetPlanWithProgress(o.Assets, roots, func(e updater.ProgressEvent) {
				last = e
				renderProgress(o, 0, "Scanning managed files", e)
			})
			if err != nil {
				renderFailure(o, "Managed-file preflight failed", err)
				waitAny()
				continue
			}
			if last.Total == 0 {
				last = updater.ProgressEvent{Done: 1, Total: 1, Detail: "Managed files verified"}
			} else {
				last.Done = last.Total
				last.Detail = "Managed files verified"
			}
			renderProgress(o, 1, "Managed-file scan complete", last)

			var cleanup updater.CleanupPlan
			var cleanupErr error
			if removeEnglish {
				cleanup, cleanupErr = updater.BuildCleanupPlanWithProgress(roots, func(e updater.ProgressEvent) {
					renderProgress(o, 1, "Verifying English-dub cleanup", e)
				})
				if cleanupErr != nil {
					plan.Warnings = append(plan.Warnings, "English-dub cleanup preflight failed; the main patch will still install and cleanup will be skipped: "+cleanupErr.Error())
					renderProgress(o, 2, "Cleanup verification warning", updater.ProgressEvent{Done: 1, Total: 1, Detail: "Cleanup will be skipped; main patch remains installable"})
				} else {
					renderProgress(o, 2, "Cleanup verification complete", updater.ProgressEvent{Done: 1, Total: 1, Detail: cleanupPlanLabel(cleanup)})
				}
			} else {
				renderProgress(o, 2, "Cleanup verification skipped", updater.ProgressEvent{Done: 1, Total: 1, Detail: "English-dub assets will be kept by user choice"})
			}

			if !confirmPlan(o, plan, cleanup, removeEnglish, keepBackup) {
				continue
			}
			backup := ""
			if keepBackup {
				backup = o.BackupRoot
				if backup == "" {
					home, _ := os.UserHomeDir()
					backup = filepath.Join(home, "MarySkelterUnburgerBackups", time.Now().Format("20060102-150405"))
				}
			}

			r, err := updater.InstallAssetsWithProgress(plan, roots, backup, false, o.Assets, func(e updater.ProgressEvent) {
				renderProgress(o, 2, "Installing / updating", e)
			})
			if err != nil {
				renderFailure(o, "Install failed — transaction rollback attempted", err)
				waitAny()
				return err
			}
			renderProgress(o, 3, "Managed patch complete", updater.ProgressEvent{Done: 1, Total: 1, Detail: patchResultLabel(r)})

			var cleaned updater.CleanupReceipt
			var cleanupRunErr error
			if removeEnglish && cleanupErr == nil && cleanupFileCount(cleanup) > 0 {
				cleanupReceipt := ""
				if keepBackup {
					cleanupReceipt = filepath.Join(backup, "cleanup_receipt.json")
				}
				cleaned, cleanupRunErr = updater.RemoveEnglishAssetsWithProgress(cleanup, roots, cleanupReceipt, func(e updater.ProgressEvent) {
					renderProgress(o, 3, "Removing verified English-dub assets", e)
				})
				if cleanupRunErr != nil {
					renderProgress(o, 4, "Cleanup completed with warning", updater.ProgressEvent{Done: 1, Total: 1, Detail: cleanupRunErr.Error()})
				} else {
					renderProgress(o, 4, "English-dub cleanup complete", updater.ProgressEvent{Done: 1, Total: 1, Detail: fmt.Sprintf("%d files · %s reclaimed", len(cleaned.Files), updater.FormatBytes(cleaned.TotalBytes))})
				}
			} else {
				if removeEnglish {
					cleanupRunErr = cleanupErr
				}
				label := "Already clean"
				if !removeEnglish {
					label = "Kept by user choice"
				} else if cleanupErr != nil {
					label = "Skipped after cleanup preflight warning"
				}
				renderProgress(o, 4, "English-dub cleanup", updater.ProgressEvent{Done: 1, Total: 1, Detail: label})
			}

			receiptPath, receiptErr := writeRunReceipt(o, roots, r, removeEnglish, cleaned, cleanupRunErr, backup)
			if receiptErr != nil {
				r.Warnings = append(r.Warnings, "install completed, but receipt could not be written: "+receiptErr.Error())
				receiptPath = "not written: " + receiptErr.Error()
			}
			renderProgress(o, 5, "Install complete", updater.ProgressEvent{Done: 1, Total: 1, Detail: "Receipt and final state recorded"})
			renderSummary(o, r, removeEnglish, cleaned, cleanupRunErr, backup, receiptPath)
			waitAny()
			return nil
		}
	}
}

func configureOptions(o Options, roots map[string]string) (removeEnglish bool, keepBackup bool, back bool) {
	selected := []bool{true, o.BackupRoot != ""}
	idx := 0
	labels := []string{
		"Purge all known English dub assets",
		"Keep persistent backup of original patched files",
	}
	for {
		clear()
		renderHeader(o, "INSTALLER")
		fmt.Println(boxTop("Install options"))
		for i, label := range labels {
			mark := " "
			if selected[i] {
				mark = "✓"
			}
			row := fmt.Sprintf("[%s] %s", mark, label)
			if i == idx {
				row = inverse(" ▶ " + padRight(row, 76))
			} else {
				row = "   " + row
			}
			fmt.Println(boxLine(row))
			if i == 0 {
				fmt.Println(boxLine(dim(fmt.Sprintf("    Default ON · reclaim up to %s · purge targets are never backed up", updater.FormatBytes(updater.CleanupPotentialBytes(roots))))))
			} else {
				fmt.Println(boxLine(dim("    Default OFF · atomic rollback always protects an incomplete transaction")))
			}
		}
		fmt.Println(boxRule())
		fmt.Println(boxLine("Required automatically: UNBURGER data · JP voice · startup skip · Finale JP movie audio"))
		fmt.Println(boxLine("MS2 required Minigame.dat restoration is automatic when missing."))
		fmt.Println(boxBottom())
		fmt.Println(dim("  ↑/↓ move   Space toggle   Enter continue   Esc/Q back"))
		k, _ := readKey()
		switch k {
		case keyUp:
			if idx > 0 {
				idx--
			}
		case keyDown:
			if idx < len(selected)-1 {
				idx++
			}
		case keySpace:
			selected[idx] = !selected[idx]
		case keyEnter:
			return selected[0], selected[1], false
		case keyEsc, keyQuit:
			return false, false, true
		}
	}
}

func confirmPlan(o Options, plan updater.Plan, cleanup updater.CleanupPlan, removeEnglish bool, keepBackup bool) bool {
	for {
		clear()
		renderHeader(o, "INSTALLER")
		fmt.Println(boxTop("Install / update plan"))
		for _, game := range plan.Games {
			fmt.Println(boxLine(fmt.Sprintf("%-24s executable  %s", game.Profile.DisplayName, stateLabel(game.ExeState))))
		}
		fmt.Println(boxRule())
		counts := planCounts(plan)
		fmt.Println(boxLine(fmt.Sprintf("Executable patches        %3d    Structural GDAT patches  %3d", counts["PATCH"], counts["PATCH-GDAT"])))
		fmt.Println(boxLine(fmt.Sprintf("Finale movie remuxes      %3d    Required file creates    %3d", counts["REMUX"], counts["CREATE"])))
		if len(plan.SoftFailures) > 0 {
			fmt.Println(boxLine(yellow(fmt.Sprintf("Pre-existing non-critical movie issues: %d", len(plan.SoftFailures)))))
		}
		fmt.Println(boxRule())
		if removeEnglish {
			fmt.Println(boxLine(fmt.Sprintf("English-dub cleanup       %s", cleanupPlanLabel(cleanup))))
		} else {
			fmt.Println(boxLine("English-dub cleanup       kept by user choice"))
		}
		if keepBackup {
			fmt.Println(boxLine("Persistent backup         ON"))
		} else {
			fmt.Println(boxLine("Persistent backup         OFF · transaction rollback still active"))
		}
		for _, w := range plan.Warnings {
			fmt.Println(boxLine(yellow("Warning: ") + w))
		}
		fmt.Println(boxBottom())
		fmt.Println(dim("  Enter install/update   Esc/Q back"))
		k, _ := readKey()
		if k == keyEnter {
			return true
		}
		if k == keyQuit || k == keyEsc {
			return false
		}
	}
}

func drawSelection(o Options, items []item, idx int, msg string) {
	clear()
	renderHeader(o, "INSTALLER")
	fmt.Println(boxTop("Detected games"))
	for i, it := range items {
		mark := " "
		if it.selected {
			mark = "✓"
		}
		row := fmt.Sprintf("[%s] %-29s %s", mark, it.name, stateLabel(it.state))
		if i == idx {
			row = inverse(" ▶ " + padRight(row, 76))
		} else {
			row = "   " + row
		}
		fmt.Println(boxLine(row))
		fmt.Println(boxLine(dim("    " + it.root)))
	}
	fmt.Println(boxRule())
	fmt.Println(boxLine("Automatic: UNBURGER data · JP voice · startup-logo skip · Finale JP movie-audio remux"))
	fmt.Println(boxLine("Cleanup and optional persistent backup are configured on the next screen."))
	if msg != "" {
		fmt.Println(boxLine(yellow(msg)))
	}
	fmt.Println(boxBottom())
	fmt.Println(dim("  ↑/↓ move   Space toggle game   Enter continue   Q quit"))
}

func renderHeader(o Options, mode string) {
	for _, line := range tui.Banner(style(), "END-USER PATCHER · "+mode, o.Version) {
		fmt.Println(line)
	}
	fmt.Println()
}

func patchStages() []tui.StageRow {
	return []tui.StageRow{
		{Title: "Scan managed game files", Detail: "Verify exact versions, owned game installations and available patch assets.", Safety: "Read only. Unsupported builds stop before changes."},
		{Title: "Review optional cleanup", Detail: "Inspect verified English voice files to remove only with owner confirmation.", Safety: "Cleanup can be skipped; never delete unrecognized private files."},
		{Title: "Apply patch transaction", Detail: "Install or repair the selected Mary Skelter PC games using rollback protection.", Safety: "Restore prior state if a managed write fails."},
		{Title: "Optional voice cleanup", Detail: "Remove only selected, exact verified English-dub files.", Safety: "Keep source archives and unrelated files."},
		{Title: "Receipt and final summary", Detail: "Record installed files, movie status, warnings and backup path.", Safety: "Report unresolved soft failures rather than claiming full success."},
	}
}
func renderProgress(o Options, completedStages int, stage string, e updater.ProgressEvent) {
	clear()
	renderHeader(o, "INSTALLER")
	rows := patchStages()
	current := completedStages
	if current >= len(rows) {
		current = len(rows) - 1
	}
	note := stage
	if e.Detail != "" {
		note += " · " + e.Detail
	}
	for _, line := range tui.StagePanel(style(), "PATCH INSTALL · PROGRESS", tui.StagesFromProgress(rows, completedStages, false), current, note) {
		fmt.Println(line)
	}
	fmt.Println()
	fmt.Println(boxTop(stage))
	done, total := e.Done, e.Total
	if total <= 0 {
		done, total = 0, 1
	}
	fmt.Println(boxLine(fmt.Sprintf("%s  %d/%d", progressBar(done, total, 36), e.Done, e.Total)))
	if e.GameKey != "" || e.Action != "" {
		fmt.Println(boxRule())
		fmt.Println(boxLine(fmt.Sprintf("%-12s %-11s %s", gameShort(e.GameKey), e.Action, e.RelativePath)))
	}
	if e.Detail != "" {
		fmt.Println(boxLine(dim(e.Detail)))
	}
	fmt.Println(boxBottom())
}

func renderFailure(o Options, titleText string, err error) {
	clear()
	renderHeader(o, "INSTALLER")
	fmt.Println(boxTop(titleText))
	fmt.Println(boxLine(red("ERROR: ") + err.Error()))
	fmt.Println(boxBottom())
	fmt.Println(dim("  Press any key to continue"))
}

func renderSummary(o Options, patch updater.Receipt, cleanupRequested bool, cleanup updater.CleanupReceipt, cleanupErr error, backup, receiptPath string) {
	clear()
	renderHeader(o, "INSTALLER")
	critical, remuxed := updater.ReceiptOperationCounts(patch)
	movieFailures := 0
	for _, f := range patch.SoftFailures {
		if f.Action == "REMUX" {
			movieFailures++
		}
	}
	fmt.Println(green(boxTop("INSTALL / UPDATE COMPLETE")))
	if critical == 0 && remuxed == 0 && movieFailures == 0 {
		fmt.Println(boxLine(green("✓ Managed patch state is already current")))
	} else {
		fmt.Println(boxLine(fmt.Sprintf("Managed patch             %d critical operation(s)", critical)))
		movieLine := fmt.Sprintf("Finale movies             %d remuxed · %d unresolved", remuxed, movieFailures)
		if movieFailures == 0 {
			movieLine = green("✓ ") + movieLine
		} else {
			movieLine = yellow("! ") + movieLine
		}
		fmt.Println(boxLine(movieLine))
	}
	if cleanupRequested {
		switch {
		case cleanupErr != nil:
			fmt.Println(boxLine(yellow("! English-dub cleanup     " + cleanupErr.Error())))
		case len(cleanup.Files) == 0:
			fmt.Println(boxLine(green("✓ English-dub cleanup     already clean")))
		default:
			fmt.Println(boxLine(green(fmt.Sprintf("✓ English-dub cleanup     %d files · %s reclaimed", len(cleanup.Files), updater.FormatBytes(cleanup.TotalBytes)))))
		}
	} else {
		fmt.Println(boxLine("English-dub cleanup       kept by user choice"))
	}
	if backup != "" {
		fmt.Println(boxLine("Persistent backup         " + backup))
	} else {
		fmt.Println(boxLine("Persistent backup         not created"))
	}
	fmt.Println(boxRule())
	fmt.Println(boxLine("Receipt                   " + receiptPath))
	fmt.Println(green(boxBottom()))
	if len(patch.SoftFailures) > 0 {
		fmt.Println()
		fmt.Println(boxTop("Non-critical items to retry"))
		for _, f := range patch.SoftFailures {
			fmt.Println(boxLine(yellow(fmt.Sprintf("%s · %s · %s", gameShort(f.GameKey), f.Action, f.RelativePath))))
		}
		fmt.Println(boxLine(dim("Re-run the updater; already-current files will not be rewritten.")))
		fmt.Println(boxBottom())
	}
	fmt.Println(dim("  Press any key to exit"))
}

func planCounts(plan updater.Plan) map[string]int {
	out := map[string]int{"PATCH": 0, "PATCH-GDAT": 0, "REMUX": 0, "CREATE": 0}
	for _, op := range plan.Operations {
		out[op.Action]++
	}
	return out
}

func cleanupFileCount(plan updater.CleanupPlan) int {
	n := len(plan.Files)
	for _, d := range plan.Directories {
		n += len(d.Files)
	}
	return n
}

func cleanupPlanLabel(plan updater.CleanupPlan) string {
	n := cleanupFileCount(plan)
	if n == 0 {
		return "already clean"
	}
	return fmt.Sprintf("%d verified file(s) · reclaim %s", n, updater.FormatBytes(plan.TotalBytes))
}

func patchResultLabel(r updater.Receipt) string {
	critical, remuxed := updater.ReceiptOperationCounts(r)
	return fmt.Sprintf("%d critical operation(s) · %d movie remux(es) · %d unresolved", critical, remuxed, len(r.SoftFailures))
}

func stateLabel(state string) string {
	switch state {
	case "final":
		return green("● current")
	case "original":
		return yellow("● ready to patch")
	default:
		return yellow("● " + state)
	}
}

func gameShort(key string) string {
	switch key {
	case "ms2":
		return "MS2"
	case "finale":
		return "Finale"
	default:
		return key
	}
}

func runPlain(o Options, items []item) error {
	in := bufio.NewScanner(os.Stdin)
	fmt.Printf("MARY SKELTER UNBURGER MOD  v%s\n\n", o.Version)
	fmt.Println("Detected games:")
	for i, it := range items {
		fmt.Printf("  %d. %s [%s]\n     %s\n", i+1, it.name, it.state, it.root)
	}
	fmt.Print("\nInstall/update all detected games? [Y/n] ")
	if in.Scan() && strings.EqualFold(strings.TrimSpace(in.Text()), "n") {
		return nil
	}
	roots := selectedRoots(items)
	removeEnglish := true
	fmt.Printf("Purge all known English dub assets after install (up to %s, never backed up)? [Y/n] ", updater.FormatBytes(updater.CleanupPotentialBytes(roots)))
	if in.Scan() && strings.EqualFold(strings.TrimSpace(in.Text()), "n") {
		removeEnglish = false
	}
	keepBackup := o.BackupRoot != ""
	if !keepBackup {
		fmt.Print("Keep a persistent backup of original patched files? [y/N] ")
		if in.Scan() && strings.EqualFold(strings.TrimSpace(in.Text()), "y") {
			keepBackup = true
		}
	}
	plan, err := updater.BuildAssetPlan(o.Assets, roots)
	if err != nil {
		return err
	}
	var cleanup updater.CleanupPlan
	var cleanupErr error
	if removeEnglish {
		cleanup, cleanupErr = updater.BuildCleanupPlan(roots)
		if cleanupErr != nil {
			plan.Warnings = append(plan.Warnings, "English-dub cleanup preflight failed; the main patch will still install and cleanup will be skipped: "+cleanupErr.Error())
		}
	}
	fmt.Print("\n" + updater.FormatPlan(plan))
	if removeEnglish {
		fmt.Print(updater.FormatCleanupPlan(cleanup))
	}
	fmt.Print("Continue? [y/N] ")
	if !in.Scan() || !strings.EqualFold(strings.TrimSpace(in.Text()), "y") {
		return nil
	}
	backup := ""
	if keepBackup {
		backup = o.BackupRoot
		if backup == "" {
			home, _ := os.UserHomeDir()
			backup = filepath.Join(home, "MarySkelterUnburgerBackups", time.Now().Format("20060102-150405"))
		}
	}
	r, err := updater.InstallAssets(plan, roots, backup, false, o.Assets)
	if err != nil {
		return err
	}
	var cleaned updater.CleanupReceipt
	var cleanupRunErr error
	if removeEnglish && cleanupErr == nil {
		cleanupReceipt := ""
		if keepBackup {
			cleanupReceipt = filepath.Join(backup, "cleanup_receipt.json")
		}
		cleaned, cleanupRunErr = updater.RemoveEnglishAssets(cleanup, roots, cleanupReceipt)
		if cleanupRunErr != nil {
			fmt.Printf("WARNING: main install completed, but English-dub cleanup was incomplete after reclaiming %s: %v\n", updater.FormatBytes(cleaned.TotalBytes), cleanupRunErr)
		}
	} else if removeEnglish {
		cleanupRunErr = cleanupErr
	}
	fmt.Print(updater.FormatRunSummary(r, removeEnglish, cleaned, cleanupRunErr))
	printSoftFailures(r.SoftFailures)
	if keepBackup {
		fmt.Printf("Persistent original-file backup: %s\n", backup)
	} else {
		fmt.Println("Persistent original-file backup: not created; transaction rollback state was discarded after success")
	}
	path, receiptErr := writeRunReceipt(o, roots, r, removeEnglish, cleaned, cleanupRunErr, backup)
	if receiptErr != nil {
		fmt.Printf("WARNING: install completed, but receipt could not be written: %v\n", receiptErr)
	} else {
		fmt.Printf("Receipt: %s\n", path)
	}
	return nil
}

func writeRunReceipt(o Options, roots map[string]string, patch updater.Receipt, cleanupRequested bool, cleanup updater.CleanupReceipt, cleanupErr error, backup string) (string, error) {
	path, err := updater.DefaultRunReceiptPath("install")
	if err != nil {
		return "", err
	}
	r := updater.NewRunReceipt("install", o.Version, o.Assets, roots, patch, cleanupRequested, cleanup, cleanupErr, backup)
	if err := updater.WriteRunReceipt(path, r); err != nil {
		return "", err
	}
	return path, nil
}

func printSoftFailures(failures []updater.OperationFailure) {
	if len(failures) == 0 {
		return
	}
	fmt.Printf("WARNING: %d non-critical movie operation(s) did not complete:\n", len(failures))
	for _, f := range failures {
		fmt.Printf("  - %s: %s\n", f.RelativePath, f.Error)
	}
	fmt.Println("The rest of the patch remains installed. Re-running the updater will retry only unresolved movies.")
}

func selectedRoots(items []item) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		if it.selected {
			out[it.key] = it.root
		}
	}
	return out
}

func clear() { fmt.Print("\x1b[2J\x1b[H") }
func isInteractive() bool {
	i, _ := os.Stdin.Stat()
	o, _ := os.Stdout.Stat()
	return i.Mode()&os.ModeCharDevice != 0 && o.Mode()&os.ModeCharDevice != 0
}

// Stable helper retained for deterministic tests/debug views.
func sortedRootKeys(roots map[string]string) []string {
	keys := make([]string, 0, len(roots))
	for key := range roots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
