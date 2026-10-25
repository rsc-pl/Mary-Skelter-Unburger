package builderui

import (
	"fmt"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/switchrelease"
)

func switchReadinessLine(label string, done, total int) string {
	state := red("✗ INCOMPLETE")
	if done == total && total > 0 {
		state = green("✓ VERIFIED")
	}
	return fmt.Sprintf("%-17s %s  %2d/%d", label, state, done, total)
}

// All numbers here refer to independently SHA-verified original GDATs; PC
// source configuration and later full-build validation are separate checks.
func combinedReadinessLines(root string) []string {
	lines := []string{""}
	lines = append(lines, style().Cyan(boxTop("Release prerequisites · PC + Switch")))
	states, e := switchrelease.InspectIntegratedCache(root)
	if e != nil {
		lines = append(lines, boxLine(red("Switch source cache inspection failed: "+e.Error())))
	} else {
		verified, total := 0, 0
		for _, s := range states {
			n := len(s.Verified)
			t := n + len(s.Missing)
			lines = append(lines, boxLine(switchReadinessLine("Switch "+s.Game, n, t)))
			verified += n
			total += t
		}
		lines = append(lines, boxRule())
		lines = append(lines, boxLine(switchReadinessLine("Switch originals", verified, total)))
	}
	s := maintainer.Inspect(root)
	check := func(v bool) string {
		if v {
			return green("✓ READY")
		}
		return yellow("○ NOT READY")
	}
	// These two flags only check file existence; do not display READY or
	// VERIFIED as if pristine PC archives had passed integrity validation.
	configState := yellow("○ NOT CONFIGURED")
	if s.SourceConfig {
		configState = green("✓ PATHS SAVED (not validated)")
	}
	cacheState := yellow("○ NO CACHE INDEX")
	if s.SourceCache {
		cacheState = green("✓ INDEX PRESENT (data not SHA-checked here)")
	}
	lines = append(lines, boxLine(fmt.Sprintf("%-17s %s", "PC game paths", configState)))
	lines = append(lines, boxLine(fmt.Sprintf("%-17s %s", "PC cache index", cacheState)))
	lines = append(lines, boxLine(fmt.Sprintf("%-17s %s", "Movie cache", check(s.MovieCache))))
	lines = append(lines, boxLine(fmt.Sprintf("%-17s %s", "Release key", check(s.Key))))
	lines = append(lines, boxRule())
	lines = append(lines, boxLine(dim("Font/renderer checks and text archive validation run during the build.")))
	lines = append(lines, boxLine(dim("PASS above does not certify gameplay or a finished public release.")))
	lines = append(lines, style().Cyan(boxBottom()))
	lines = append(lines, "")
	return lines
}

func showCombinedReadiness(root string) {
	for _, line := range combinedReadinessLines(root) {
		fmt.Println(line)
	}
}
