package builderui

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestReleaseWizardStagesPreviewAndNoFalseCompletion(t *testing.T) {
	steps := releaseStages()
	if len(steps) != 5 {
		t.Fatalf("want five stages, got %d", len(steps))
	}
	for _, stage := range steps {
		if stage.Title == "" || stage.Detail == "" || stage.Safety == "" {
			t.Fatalf("incomplete wizard stage %+v", stage)
		}
	}
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	drawReleaseStages(Options{Version: "test"}, 0, "PREVIEW · nothing executed yet", false)
	_ = w.Close()
	os.Stdout = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	s := string(b)
	for _, expected := range []string{"PC RELEASE WORKFLOW", "Verify project", "Rebuild canonical", "Prepare release", "Build signed", "Build end-user", "PREVIEW", "0/5 stages"} {
		if !strings.Contains(s, expected) {
			t.Fatalf("stage preview missing %q: %s", expected, s)
		}
	}
}
