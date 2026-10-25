package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const DefaultWidth = 88

type Style struct {
	Width int
	Color bool
}

func New(width int, color bool) Style {
	if width < 48 {
		width = 48
	}
	return Style{Width: width, Color: color}
}

func (s Style) paint(code, text string) string {
	if !s.Color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(text string) string    { return s.paint("1", text) }
func (s Style) Dim(text string) string     { return s.paint("2", text) }
func (s Style) Cyan(text string) string    { return s.paint("36", text) }
func (s Style) Green(text string) string   { return s.paint("32", text) }
func (s Style) Yellow(text string) string  { return s.paint("33", text) }
func (s Style) Red(text string) string     { return s.paint("31", text) }
func (s Style) Inverse(text string) string { return s.paint("7", text) }

func displayLen(text string) int {
	inEscape := false
	n := 0
	for _, r := range text {
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			continue
		}
		n++
	}
	return n
}

func PadRight(text string, width int) string {
	if n := displayLen(text); n < width {
		return text + strings.Repeat(" ", width-n)
	}
	return text
}

func Centered(text string, width int) string {
	n := displayLen(text)
	if n >= width {
		return text
	}
	left := (width - n) / 2
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", width-n-left)
}

func (s Style) BoxTop(title string) string {
	inner := s.Width - 2
	if title == "" {
		return "╭" + strings.Repeat("─", inner) + "╮"
	}
	label := " " + title + " "
	if utf8.RuneCountInString(label) > inner-2 {
		rs := []rune(title)
		max := inner - 7
		if max < 1 {
			max = 1
		}
		if len(rs) > max {
			rs = rs[:max]
		}
		label = " " + string(rs) + "… "
	}
	return "╭─" + label + strings.Repeat("─", inner-utf8.RuneCountInString(label)-1) + "╮"
}

func (s Style) BoxBottom() string { return "╰" + strings.Repeat("─", s.Width-2) + "╯" }
func (s Style) BoxRule() string   { return "├" + strings.Repeat("─", s.Width-2) + "┤" }

func (s Style) BoxLine(text string) string {
	inner := s.Width - 4
	if displayLen(text) > inner {
		rs := []rune(stripANSI(text))
		if len(rs) > inner-1 {
			rs = rs[:inner-1]
		}
		text = string(rs) + "…"
	}
	return "│ " + PadRight(text, inner) + " │"
}

func stripANSI(text string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range text {
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		if r == 0x1b {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func ProgressBar(done, total, width int) string {
	if width < 4 {
		width = 4
	}
	if total <= 0 {
		total = 1
	}
	if done < 0 {
		done = 0
	}
	if done > total {
		done = total
	}
	filled := done * width / total
	pct := done * 100 / total
	return fmt.Sprintf("[%s%s] %3d%%", strings.Repeat("█", filled), strings.Repeat("░", width-filled), pct)
}
