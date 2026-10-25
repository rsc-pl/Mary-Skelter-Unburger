package switchrelease

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A second process may publish the same release after the preflight but before
// final publication. Both platforms and checksum sidecars must stay no-clobber.
func TestSwitchReleaseLateCollisionNoClobberAndRollback(t *testing.T) {
	root := testRoot(t)
	for _, phase := range []string{"zip", "sha256"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			stage := filepath.Join(dir, "stage")
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			canonical := canonicalFixture(t, root, false)
			sentinel := []byte("important user artifact: do not overwrite")
			var planted string
			hook := func(i int, part string) error {
				if i != 1 || part != phase {
					return nil
				}
				planted = filepath.Join(dir, releaseName("0.1.0", "eden"))
				if phase == "sha256" {
					planted += ".sha256"
				}
				return os.WriteFile(planted, sentinel, 0600)
			}
			result, err := packagePairWithTextHook(root, canonical, dir, stage, "0.1.0", "both", nil, hook)
			if err == nil || !strings.Contains(err.Error(), "no-clobber") {
				t.Fatalf("late collision not rejected: %v", err)
			}
			if len(result) != 0 {
				t.Fatal("partial successful report")
			}
			actual, err := os.ReadFile(planted)
			if err != nil || !bytes.Equal(actual, sentinel) {
				t.Fatalf("existing file overwritten or removed: %v", err)
			}
			for _, platform := range []string{"atmosphere", "eden"} {
				for _, ext := range []string{"", ".sha256"} {
					path := filepath.Join(dir, releaseName("0.1.0", platform)+ext)
					if path == planted {
						continue
					}
					if _, err := os.Lstat(path); !os.IsNotExist(err) {
						t.Fatalf("published artifact not rolled back: %s, %v", path, err)
					}
				}
			}
		})
	}
}

func TestSwitchReleaseSymlinkRaceDoesNotOverwriteTarget(t *testing.T) {
	root := testRoot(t)
	dir := t.TempDir()
	stage := filepath.Join(dir, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "owner-important.txt")
	const content = "owner file stays untouched"
	if err := os.WriteFile(original, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	canonical := canonicalFixture(t, root, false)
	hook := func(i int, part string) error {
		if i != 0 || part != "zip" {
			return nil
		}
		return os.Symlink(original, filepath.Join(dir, releaseName("0.1.0", "atmosphere")))
	}
	if _, err := packagePairWithTextHook(root, canonical, dir, stage, "0.1.0", "both", nil, hook); err == nil {
		t.Fatal("symlink race accepted")
	}
	actual, err := os.ReadFile(original)
	if err != nil || string(actual) != content {
		t.Fatalf("victim changed: %v", err)
	}
	dest := filepath.Join(dir, releaseName("0.1.0", "atmosphere"))
	if fi, err := os.Lstat(dest); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("competing symlink removed or replaced: %v", err)
	}
}

func TestSwitchReleaseNoClobberTwoPlatformsSuccess(t *testing.T) {
	root := testRoot(t)
	dir := t.TempDir()
	stage := filepath.Join(dir, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	canonical := canonicalFixture(t, root, false)
	assets := fixtureTextAssets(t, root)
	results, err := packagePairWithTextHook(root, canonical, dir, stage, "0.1.0", "both", assets, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("wanted both release ZIPs: %d", len(results))
	}
	for _, result := range results {
		if result.Files != 13 {
			t.Fatalf("unexpected text+graphics+IPS count: %d", result.Files)
		}
		sum, err := os.ReadFile(result.ZIP + ".sha256")
		if err != nil || string(sum) != result.SHA256+"  "+filepath.Base(result.ZIP)+"\n" {
			t.Fatalf("invalid checksum sidecar for %s: %v", result.Platform, err)
		}
	}
}
