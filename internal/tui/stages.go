package tui

import (
	"fmt"
)

// StageState describes progress in the current wizard invocation, not whether
// cached files from an earlier run are still valid.
type StageState string

const (
	StagePending StageState = "PENDING"
	StageRunning StageState = "RUNNING"
	StageDone    StageState = "DONE"
	StageBlocked StageState = "BLOCKED"
	StageSkipped StageState = "SKIPPED"
)

type StageRow struct {
	Title  string
	Detail string
	Safety string
	State  StageState
}

func stageStateColor(s Style, state StageState) string {
	label := fmt.Sprintf("%-7s", state)
	switch state {
	case StageDone:
		return s.Green(label)
	case StageRunning:
		return s.Cyan(label)
	case StageBlocked:
		return s.Red(label)
	case StageSkipped:
		return s.Yellow(label)
	default:
		return s.Dim(label)
	}
}

// StagePanel is shared by the maintainer release wizard AND the public patcher.
// It draws current-run progress, not a claim that any previously cached source
// or game output is production-approved. The selected stage drives right help.
func StagePanel(style Style, heading string, stages []StageRow, selected int, note string) []string {
	if len(stages) == 0 {
		return []string{style.BoxTop(heading), style.BoxLine("No workflow steps"), style.BoxBottom()}
	}
	if selected < 0 {
		selected = 0
	}
	if selected >= len(stages) {
		selected = len(stages) - 1
	}
	current := stages[selected]
	done := 0
	for _, st := range stages {
		if st.State == StageDone || st.State == StageSkipped {
			done++
		}
	}
	if style.Width < 76 {
		rows := []string{style.Cyan(style.BoxTop(heading))}
		for i, st := range stages {
			line := fitRunes(fmt.Sprintf("%d. %s [%s]", i+1, st.Title, st.State), style.Width-4)
			if i == selected {
				line = style.Inverse(line)
			}
			rows = append(rows, style.BoxLine(line))
		}
		rows = append(rows, style.BoxRule())
		for _, line := range wrapHelp(current.Detail+" "+current.Safety+" "+note, style.Width-4) {
			rows = append(rows, style.BoxLine(line))
		}
		return append(rows, style.Cyan(style.BoxBottom()))
	}
	inner := style.Width - 4
	rightWidth := 32
	leftWidth := inner - rightWidth - 3
	type helpLine struct{ text, tone string }
	help := []helpLine{{"CURRENT STEP", "heading"}}
	for _, l := range wrapHelp(current.Title, rightWidth) {
		help = append(help, helpLine{l, "title"})
	}
	help = append(help, helpLine{"", ""}, helpLine{"WHAT HAPPENS", "heading"})
	for _, l := range wrapHelp(current.Detail, rightWidth) {
		help = append(help, helpLine{l, ""})
	}
	help = append(help, helpLine{"", ""}, helpLine{"SAFETY", "heading"})
	for _, l := range wrapHelp(current.Safety, rightWidth) {
		help = append(help, helpLine{l, "warning"})
	}
	help = append(help, helpLine{"", ""}, helpLine{"CURRENT ACTIVITY", "heading"})
	for _, l := range wrapHelp(note, rightWidth) {
		help = append(help, helpLine{l, ""})
	}
	rows := []string{style.Cyan(style.BoxTop(heading))}
	rows = append(rows, style.BoxLine(style.Bold(fitRunes("STEP  ACTION", leftWidth))+" │ "+style.Bold(fitRunes("CONTEXT  ›", rightWidth))))
	rows = append(rows, style.BoxRule())
	total := len(stages)
	if len(help) > total {
		total = len(help)
	}
	for i := 0; i < total; i++ {
		left := fitRunes("", leftWidth)
		if i < len(stages) {
			st := stages[i]
			title := fitRunes(fmt.Sprintf("%s%02d %s", map[bool]string{true: "▶ ", false: "  "}[i == selected], i+1, st.Title), leftWidth-8)
			plain := title + " " + fitRunes(string(st.State), 7)
			if i == selected {
				left = style.Inverse(plain)
			} else {
				left = title + " " + stageStateColor(style, st.State)
			}
		}
		right := fitRunes("", rightWidth)
		if i < len(help) {
			hl := help[i]
			right = fitRunes(hl.text, rightWidth)
			switch hl.tone {
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
	rows = append(rows, style.BoxRule())
	rows = append(rows, style.BoxLine(fmt.Sprintf("Overall progress  %s  %d/%d stages", ProgressBar(done, len(stages), 24), done, len(stages))))
	rows = append(rows, style.Cyan(style.BoxBottom()))
	return rows
}

// StagesFromProgress deliberately derives only *this invocation*'s states.
func StagesFromProgress(specs []StageRow, completed int, blocked bool) []StageRow {
	out := append([]StageRow(nil), specs...)
	for i := range out {
		switch {
		case i < completed:
			out[i].State = StageDone
		case i == completed && blocked:
			out[i].State = StageBlocked
		case i == completed:
			out[i].State = StageRunning
		default:
			out[i].State = StagePending
		}
	}
	return out
}
