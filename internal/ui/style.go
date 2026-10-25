package ui

import (
	"os"

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
