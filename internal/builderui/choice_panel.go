package builderui

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"maryskelter-unburger/internal/tui"
)

var plainDecisionMode bool

// Interactive build decisions use the same arrow-key widget as the main
// menu. Non-TTY/--plain sessions retain an explicit line-oriented fallback.
func chooseBuildAction(title, description string, defaultBuild bool) bool {
	return chooseBuildActionWithReadiness(title, description, defaultBuild, "")
}

func chooseBuildActionWithReadiness(title, description string, defaultBuild bool, projectRoot string) bool {
	if plainDecisionMode || !isInteractive() {
		d := "y/N"
		if defaultBuild {
			d = "Y/n"
		}
		fmt.Printf("%s [%s] ", title, d)
		raw, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		raw = strings.ToLower(strings.TrimSpace(raw))
		if raw == "" {
			return defaultBuild
		}
		return raw == "y" || raw == "yes"
	}
	var readiness []string
	if projectRoot != "" {
		readiness = combinedReadinessLines(projectRoot)
	}
	restore, err := rawMode()
	if err != nil {
		return false
	}
	defer restore()
	selection := 1
	if defaultBuild {
		selection = 0
	}
	choices := []tui.MenuItem{
		{Title: "Start build", Detail: description, Category: "RELEASE", Safety: "Start the selected build with verified prerequisites."},
		{Title: "Return to menu", Detail: "Cancel without starting a build.", Category: "EXIT", Safety: "Existing files remain unchanged."},
	}
	for {
		clear()
		for _, line := range readiness {
			fmt.Println(line)
		}
		fmt.Println(style().Cyan(boxTop(title)))
		fmt.Println(boxLine("Build only after verifying the source and runtime checklist."))
		fmt.Println(style().Cyan(boxBottom()))
		fmt.Println()
		for _, l := range tui.MenuPanel(style(), "Choose an action", choices, selection) {
			fmt.Println(l)
		}
		fmt.Println(dim("  ↑/↓ navigate   Enter select   Q/Esc cancel"))
		k, e := readKey()
		if e != nil {
			return false
		}
		switch k {
		case keyUp:
			selection = tui.Move(selection, -1, 2)
		case keyDown:
			selection = tui.Move(selection, 1, 2)
		case keyEnter:
			return selection == 0
		case keyQuit, keyEsc:
			return false
		}
	}
}
