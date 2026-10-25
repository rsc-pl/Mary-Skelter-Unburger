package tui

import (
	"strings"
	"testing"
)

func TestStagePanelAllWidthsAndColors(t *testing.T) {
	base := []StageRow{{Title: "Verify pristine sources", Detail: "Read-only inspect actual owned game paths", Safety: "Never modifies input archives."}, {Title: "Build both PC games", Detail: "Writes staging in private project folders", Safety: "Does not overwrite original installs."}, {Title: "Package release", Detail: "Checksums and release outputs", Safety: "Do not publish original game assets."}}
	for _, color := range []bool{false, true} {
		for _, width := range []int{48, 72, 88, 120} {
			s := New(width, color)
			stages := StagesFromProgress(base, 1, false)
			rows := StagePanel(s, "STEP-BY-STEP BUILD", stages, 1, "Staging /tmp/release")
			txt := strings.Join(rows, "\n")
			for _, row := range rows {
				if displayLen(row) != width {
					t.Fatalf("width %d actual %d: %q", width, displayLen(row), row)
				}
			}
			if !strings.Contains(txt, "Staging") || !strings.Contains(txt, "Build both PC") {
				t.Fatalf("missing stage/help: %s", txt)
			}
			if color != strings.Contains(txt, "\x1b[") {
				t.Fatalf("unexpected ANSI color=%v", color)
			}
		}
	}
}
func TestStageProgressNeverAssumesCachedArtifactsComplete(t *testing.T) {
	base := []StageRow{{Title: "Inspect"}, {Title: "Build"}, {Title: "Publish"}}
	zero := StagesFromProgress(base, 0, false)
	if zero[0].State != StageRunning || zero[1].State != StagePending {
		t.Fatal("pre-run completion invented")
	}
	mid := StagesFromProgress(base, 1, true)
	if mid[0].State != StageDone || mid[1].State != StageBlocked || mid[2].State != StagePending {
		t.Fatal("bad blocked current step")
	}
	all := StagesFromProgress(base, 3, false)
	for _, s := range all {
		if s.State != StageDone {
			t.Fatal("all should be done")
		}
	}
	if base[0].State != "" {
		t.Fatal("source stages mutated")
	}
}
