package maintainer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseReplacementExplicitOptInRecoveryAndRepeat(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".local", "release", "packages")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "version.txt"), []byte("0.1.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	names := []string{"Mary-Skelter-2-Finale-UNBURGER-PC-v0.1.0.zip", "Mary-Skelter-2-Finale-UNBURGER-Switch-Atmosphere-v0.1.0.zip", "Mary-Skelter-2-Finale-UNBURGER-Switch-Eden-v0.1.0.zip"}
	for _, name := range names {
		for _, s := range []string{"", ".sha256"} {
			if err := os.WriteFile(filepath.Join(dir, name+s), []byte("OLD:"+name+s), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	called := false
	if err := WithReleaseReplacement(root, "", true, true, true, false, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("missing explicit approval guard")
	}
	fail := errors.New("injected post-PC failure")
	err := WithReleaseReplacement(root, "", true, true, true, true, func() error {
		for _, name := range names[:2] {
			for _, s := range []string{"", ".sha256"} {
				if err := os.WriteFile(filepath.Join(dir, name+s), []byte("NEW"), 0644); err != nil {
					return err
				}
			}
		}
		return fail
	})
	if !errors.Is(err, fail) {
		t.Fatalf("did not propagate failure: %v", err)
	}
	for _, name := range names {
		for _, s := range []string{"", ".sha256"} {
			b, e := os.ReadFile(filepath.Join(dir, name+s))
			if e != nil || string(b) != "OLD:"+name+s {
				t.Fatalf("rollback did not restore %s: %q %v", name+s, b, e)
			}
		}
	}
	err = WithReleaseReplacement(root, "", true, true, true, true, func() error {
		for _, name := range names {
			for _, s := range []string{"", ".sha256"} {
				if err := os.WriteFile(filepath.Join(dir, name+s), []byte("NEW"), 0644); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		for _, s := range []string{"", ".sha256"} {
			b, e := os.ReadFile(filepath.Join(dir, name+s))
			if e != nil || string(b) != "NEW" {
				t.Fatalf("output mismatch %s %q %v", name+s, b, e)
			}
		}
	}
	backups, _ := filepath.Glob(filepath.Join(root, ".local", "release", "replaced-packages", "previous-*"))
	if len(backups) != 2 {
		t.Fatalf("expected two retained backup attempts, got %d", len(backups))
	}
	for _, name := range names {
		for _, s := range []string{"", ".sha256"} {
			var b []byte
			var e error
			for _, backup := range backups {
				b, e = os.ReadFile(filepath.Join(backup, name+s))
				if e == nil {
					break
				}
			}
			if e != nil || !strings.HasPrefix(string(b), "OLD:") {
				t.Fatalf("lost old output in saved release: %s %v", name+s, e)
			}
		}
	}
}

func TestReleaseReplacementRefusesSymlinkAndDoesNotCallBuild(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".local", "release", "packages")
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "version.txt"), []byte("0.1.0"), 0644); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(dir, "Mary-Skelter-2-Finale-UNBURGER-PC-v0.1.0.zip")
	if e := os.Symlink(filepath.Join(root, "other-file"), target); e != nil {
		t.Fatal(e)
	}
	called := false
	err := WithReleaseReplacement(root, "", true, false, false, true, func() error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("symlink was accepted, err=%v", err)
	}
}

func TestPriorVersionZIPsDoNotBlockNewRelease(t *testing.T) {
	root := t.TempDir()
	out := filepath.Join(root, ".local", "release", "packages")
	if err := os.MkdirAll(out, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "version.txt"), []byte("0.4.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	old := "Mary-Skelter-2-Finale-UNBURGER-PC-v0.4.2.zip"
	if err := os.WriteFile(filepath.Join(out, old), []byte("prior release"), 0644); err != nil {
		t.Fatal(err)
	}
	called := false
	err := WithReleaseReplacement(root, "", true, false, false, false, func() error {
		called = true
		for _, suffix := range []string{".zip", ".zip.sha256"} {
			if err := os.WriteFile(filepath.Join(out, "Mary-Skelter-2-Finale-UNBURGER-PC-v0.4.3"+suffix), []byte("current release"), 0644); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("older ZIP blocked new version: called=%v err=%v", called, err)
	}
	b, err := os.ReadFile(filepath.Join(out, old))
	if err != nil || string(b) != "prior release" {
		t.Fatalf("older ZIP changed: %q %v", b, err)
	}
}
