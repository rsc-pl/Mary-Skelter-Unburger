package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MenuItem is an action owned by its caller. The common TUI only presents the
// description and context; it never invokes maintainer-only operations itself.
type MenuItem struct {
	Title    string
	Detail   string
	Category string // short, descriptive category; NOT a readiness claim
	Safety   string // per-action safety note for the right-hand help panel
}

func Banner(style Style, role, version string) []string {
	return []string{
		style.Cyan(style.BoxTop("MARY SKELTER UNBURGER")),
		style.BoxLine(style.Bold(Centered(strings.ToUpper(role)+"  ·  v"+version, style.Width-4))),
		style.Cyan(style.BoxBottom()),
	}
}

// fitRunes clips one plain-text cell to a fixed width without leaving half an
// ANSI escape sequence in the output. Presentation colors are applied after it.
func fitRunes(s string, width int) string {
	if width <= 0 {
		return ""
	}
	rs := []rune(stripANSI(s))
	if len(rs) > width {
		if width == 1 {
			return "…"
		}
		return string(rs[:width-1]) + "…"
	}
	return string(rs) + strings.Repeat(" ", width-len(rs))
}

// wrapHelp wraps by words and also copes with unusually long file/path tokens.
func wrapHelp(s string, width int) []string {
	if width <= 0 {
		return nil
	}
	if strings.TrimSpace(s) == "" {
		return []string{""}
	}
	var lines []string
	current := ""
	flush := func() {
		if current != "" {
			lines = append(lines, current)
			current = ""
		}
	}
	for _, word := range strings.Fields(s) {
		for utf8.RuneCountInString(word) > width {
			flush()
			rr := []rune(word)
			lines = append(lines, string(rr[:width]))
			word = string(rr[width:])
		}
		if current == "" {
			current = word
			continue
		}
		if utf8.RuneCountInString(current)+1+utf8.RuneCountInString(word) > width {
			flush()
			current = word
		} else {
			current += " " + word
		}
	}
	flush()
	return lines
}

func categoryColor(s Style, category string) string {
	switch strings.ToUpper(category) {
	case "PATCH", "PC", "RELEASE":
		return s.Green(category)
	case "SWITCH", "SAVE":
		return s.Cyan(category)
	case "VERIFY", "CHECK":
		return s.Yellow(category)
	case "EXIT":
		return s.Dim(category)
	default:
		return s.Bold(category)
	}
}

// MenuPanel shows a navigable, color-coded action table alongside CONTEXT HELP.
// The right panel acts as a keyboard-accessible, selection-driven tooltip; it
// does not depend on mouse hover. Every visible line has the same terminal width.
// Narrow widths degrade to a one-column menu with the selected item's details.
func MenuPanel(style Style, heading string, items []MenuItem, selected int) []string {
	if len(items) == 0 {
		return []string{style.BoxTop(heading), style.BoxLine("No actions available"), style.BoxBottom()}
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(items) {
		selected = len(items) - 1
	}
	item := items[selected]
	if style.Width < 76 { // reserved for future narrow terminal layouts
		rows := []string{style.Cyan(style.BoxTop(heading))}
		for i, it := range items {
			cell := fitRunes(fmt.Sprintf("%02d  %s", i+1, it.Title), style.Width-4)
			if i == selected {
				cell = style.Inverse(cell)
			}
			rows = append(rows, style.BoxLine(cell))
		}
		rows = append(rows, style.BoxRule())
		for _, x := range wrapHelp(item.Detail, style.Width-4) {
			rows = append(rows, style.BoxLine(style.Dim(x)))
		}
		return append(rows, style.Cyan(style.BoxBottom()))
	}
	inner := style.Width - 4
	rightWidth := 32
	leftWidth := inner - rightWidth - 3
	if leftWidth < 30 {
		leftWidth = 30
		rightWidth = inner - leftWidth - 3
	}
	helpWidth := rightWidth - 1
	type helpLine struct{ text, tone string }
	help := []helpLine{{"SELECTED ACTION", "heading"}}
	for _, x := range wrapHelp(item.Title, helpWidth) {
		help = append(help, helpLine{x, "title"})
	}
	help = append(help, helpLine{"", ""}, helpLine{"WHAT IT DOES", "heading"})
	for _, x := range wrapHelp(item.Detail, helpWidth) {
		help = append(help, helpLine{x, ""})
	}
	help = append(help, helpLine{"", ""}, helpLine{"SAFETY / SCOPE", "heading"})
	safety := item.Safety
	if safety == "" {
		safety = "Check the selected action before proceeding."
	}
	for _, x := range wrapHelp(safety, helpWidth) {
		help = append(help, helpLine{x, "warning"})
	}
	help = append(help, helpLine{"", ""}, helpLine{"KEYBOARD", "heading"}, helpLine{"↑ / ↓  Navigate", ""}, helpLine{"Enter  Select   Esc / Q  Exit", ""})
	rows := []string{style.Cyan(style.BoxTop(heading))}
	rows = append(rows, style.BoxLine(style.Bold(fitRunes("#   ACTION", leftWidth))+" │ "+style.Bold(fitRunes("CONTEXT HELP  ›", rightWidth))))
	rows = append(rows, style.BoxRule())
	total := len(items)
	if len(help) > total {
		total = len(help)
	}
	for i := 0; i < total; i++ {
		left := ""
		if i < len(items) {
			it := items[i]
			category := strings.ToUpper(it.Category)
			if category == "" {
				category = "ACTION"
			}
			// category occupies its own right-aligned column in the action table.
			titleWidth := leftWidth - 11
			prefix := "  "
			if i == selected {
				prefix = "▶ "
			}
			title := fitRunes(fmt.Sprintf("%s%02d %s", prefix, i+1, it.Title), titleWidth)
			badge := fitRunes(category, 10)
			if i == selected {
				left = style.Inverse(title + " " + badge)
			} else {
				left = title + " " + categoryColor(style, badge)
			}
		} else {
			left = fitRunes("", leftWidth)
		}
		right := fitRunes("", rightWidth)
		if i < len(help) {
			line := help[i]
			right = fitRunes(line.text, rightWidth)
			switch line.tone {
			case "heading":
				right = style.Cyan(right)
			case "title":
				right = style.Bold(right)
			case "warning":
				right = style.Yellow(right)
			}
		}
		rows = append(rows, style.BoxLine(left+" │ "+right))
	}
	rows = append(rows, style.Cyan(style.BoxBottom()))
	return rows
}

// Move clamps keyboard navigation to valid menu entries rather than wrapping.
func Move(selected, delta, count int) int {
	if count <= 0 {
		return 0
	}
	next := selected + delta
	if next < 0 {
		return 0
	}
	if next >= count {
		return count - 1
	}
	return next
}
