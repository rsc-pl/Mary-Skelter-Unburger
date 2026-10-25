package tui

import (
	"strings"
	"testing"
)

func TestSharedMenuRendersRolesConsistently(t *testing.T) {
	s := New(DefaultWidth, false)
	for _, role := range []string{"MAINTAINER BUILDER", "END-USER PATCHER"} {
		b := Banner(s, role, "test")
		if len(b) != 3 || !strings.Contains(strings.Join(b, "\n"), role) {
			t.Fatalf("banner %s: %q", role, b)
		}
	}
	rows := MenuPanel(s, "Choose action", []MenuItem{{Title: "Prepare sources", Detail: "Validate source installs"}, {Title: "Create patches", Detail: "Prepare release"}, {Title: "Quit", Detail: "Leave unchanged"}}, 1)
	joined := strings.Join(rows, "\n")
	for _, v := range []string{"▶", "Create patches", "Prepare release", "Quit", "Choose action"} {
		if !strings.Contains(joined, v) {
			t.Fatalf("missing %q", v)
		}
	}
	for _, line := range rows {
		if len([]rune(line)) > DefaultWidth {
			t.Fatalf("too wide: %q", line)
		}
	}
}
func TestMenuMoveClamp(t *testing.T) {
	if Move(0, -1, 3) != 0 || Move(0, 1, 3) != 1 || Move(2, 1, 3) != 2 || Move(0, 2, 0) != 0 {
		t.Fatal("unexpected navigation")
	}
}

func TestContextPanelOnRightAndColorSafety(t *testing.T) {
	items := []MenuItem{{Title: "Prepare sources", Detail: "Inspect all game source directories first", Category: "VERIFY", Safety: "Read only."}, {Title: "Make PC and Switch patches", Detail: "Writes a staged release after checks", Category: "RELEASE", Safety: "Will not overwrite pristine source files."}}
	for _, colored := range []bool{false, true} {
		s := New(88, colored)
		first := MenuPanel(s, "WORKFLOW", items, 0)
		second := MenuPanel(s, "WORKFLOW", items, 1)
		for _, line := range first {
			if displayLen(line) != 88 {
				t.Fatalf("width=%d for %q", displayLen(line), line)
			}
		}
		if !strings.Contains(strings.Join(first, "\n"), "Inspect all game") || !strings.Contains(strings.Join(second, "\n"), "Writes a staged") {
			t.Fatal("help not updated with selected action")
		}
		if strings.Contains(strings.Join(second, "\n"), "Inspect all game") {
			t.Fatal("old action help lingered")
		}
		if colored && !strings.Contains(strings.Join(first, "\n"), "\x1b[") {
			t.Fatal("no ANSI colors")
		}
		if !colored && strings.Contains(strings.Join(first, "\n"), "\x1b[") {
			t.Fatal("NO_COLOR leaked")
		}
	}
}
func TestWrapHelpNoLostLongWords(t *testing.T) {
	input := strings.Repeat("a", 99)
	parts := wrapHelp(input, 21)
	if strings.Join(parts, "") != input {
		t.Fatal("help lost characters")
	}
	for _, p := range parts {
		if len([]rune(p)) > 21 {
			t.Fatal("wrap overflow")
		}
	}
}
