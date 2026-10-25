package ui

import (
	"io"
	"os"
	"strings"
	"testing"

	"maryskelter-unburger/internal/updater"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	fn()
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	return string(b)
}

func TestClientTUISelectionUsesSharedVisualLanguage(t *testing.T) {
	out := captureStdout(t, func() {
		drawSelection(Options{Version: "0.1.0"}, []item{
			{key: "ms2", name: "Mary Skelter 2", root: "/games/ms2", state: "final", selected: true},
			{key: "finale", name: "Mary Skelter Finale", root: "/games/finale", state: "original", selected: true},
		}, 0, "")
	})
	for _, want := range []string{"MARY SKELTER UNBURGER", "INSTALLER  ·  v0.1.0", "Detected games", "Cleanup and optional persistent backup", "╭", "╰"} {
		if !strings.Contains(out, want) {
			t.Fatalf("selection output missing %q:\n%s", want, out)
		}
	}
}

func TestClientTUIProgressShowsStageAndUnitBars(t *testing.T) {
	out := captureStdout(t, func() {
		renderProgress(Options{Version: "0.1.0"}, 2, "Installing / updating", updater.ProgressEvent{
			GameKey: "finale", Action: "REMUX", RelativePath: "masterWindows/MOVIE_EN/600000.mp4",
			Detail: "Applying", Done: 9, Total: 18,
		})
	})
	for _, want := range []string{"Overall progress", "2/5 stages", "Installing / updating", "9/18", "REMUX", "600000.mp4", "50%", "█", "░"} {
		if !strings.Contains(out, want) {
			t.Fatalf("progress output missing %q:\n%s", want, out)
		}
	}
}
