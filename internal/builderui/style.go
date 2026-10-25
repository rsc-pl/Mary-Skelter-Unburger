package builderui

import (
	"fmt"
	"os"

	"maryskelter-unburger/internal/maintainer"
	"maryskelter-unburger/internal/tui"
)

const tuiWidth = tui.DefaultWidth

func style() tui.Style                          { return tui.New(tuiWidth, ansiEnabled()) }
func ansiEnabled() bool                         { return isInteractive() && os.Getenv("NO_COLOR") == "" }
func bold(s string) string                      { return style().Bold(s) }
func dim(s string) string                       { return style().Dim(s) }
func cyan(s string) string                      { return style().Cyan(s) }
func green(s string) string                     { return style().Green(s) }
func yellow(s string) string                    { return style().Yellow(s) }
func red(s string) string                       { return style().Red(s) }
func inverse(s string) string                   { return style().Inverse(s) }
func padRight(s string, width int) string       { return tui.PadRight(s, width) }
func centered(s string, width int) string       { return tui.Centered(s, width) }
func boxTop(title string) string                { return style().BoxTop(title) }
func boxBottom() string                         { return style().BoxBottom() }
func boxLine(s string) string                   { return style().BoxLine(s) }
func boxRule() string                           { return style().BoxRule() }
func progressBar(done, total, width int) string { return tui.ProgressBar(done, total, width) }

func stateWord(ok bool) string {
	if ok {
		return green("● READY")
	}
	return dim("○ pending")
}

func statusCell(label string, ok bool) string {
	return fmt.Sprintf("%-21s %s", label, padRight(stateWord(ok), 16))
}

func statusRows(s maintainer.Status) []string {
	toolsOK := len(s.MissingTools) == 0
	rows := []string{
		statusCell("Source paths", s.SourceConfig) + "  " + statusCell("Source cache", s.SourceCache),
		statusCell("Canonical bundles", s.Dist) + "  " + statusCell("Release stage", s.Stage),
		statusCell("Private v3 key", s.Key) + "  " + statusCell("V3 container", s.Container),
		statusCell("End-user release", s.Release) + "  " + statusCell("Toolchain", toolsOK),
	}
	movie := fmt.Sprintf("%d/22 pairs · %d JP audio objects", s.MoviePairs, s.MovieObjects)
	rows = append(rows, statusCell("Movie cache", s.MovieCache)+"  "+movie)
	return rows
}

func readiness(s maintainer.Status) (int, int) {
	vals := []bool{s.SourceConfig, s.SourceCache, s.Dist, s.MovieCache, s.Stage, s.Key, s.Container, s.Release, len(s.MissingTools) == 0}
	done := 0
	for _, v := range vals {
		if v {
			done++
		}
	}
	return done, len(vals)
}

func workflowProgress(done, total int, label string) {
	fmt.Println(boxTop("Workflow progress"))
	fmt.Println(boxLine(fmt.Sprintf("%s  %d/%d  %s", progressBar(done, total, 34), done, total, label)))
	fmt.Println(boxBottom())
}
