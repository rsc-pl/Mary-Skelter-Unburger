package maintainer

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This source-level regression gate deliberately does not inspect frozen
// game text, vendored dependencies or private owner-only material.
func TestPublicAuthoredSourcesContainNoDevelopmentHistory(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	numericLabel := regexp.MustCompile(`\b(?:I[0-9]{2,3}|B[0-9]{2}|G[0-9]+R[0-9]+)\b`)
	internalTerms := []string{
		"Chat" + "GPT", "Deep" + "Seek", "dev" + "log", "hand" + "off",
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case ".local", ".git", "third_party", "translations", "python-runtime":
				return filepath.SkipDir
			}
			return nil
		}
		// Third-party runtimes and vendored packages retain their original
		// upstream source. They are not authored by this project.
		if !d.Type().IsRegular() {
			return nil
		}
		ext := filepath.Ext(d.Name())
		if ext != ".go" && ext != ".py" && ext != ".md" && ext != ".sh" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		if numericLabel.MatchString(text) {
			t.Errorf("numbered development label in %s", path)
		}
		for _, item := range internalTerms {
			if strings.Contains(strings.ToLower(text), strings.ToLower(item)) {
				t.Errorf("private development term in %s", path)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
