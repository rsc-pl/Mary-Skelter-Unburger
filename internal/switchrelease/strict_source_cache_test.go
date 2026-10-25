package switchrelease

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceLookupNeverProbesLegacyWorkspace(t *testing.T) {
	repo := t.TempDir()
	legacy := filepath.Join(t.TempDir(), "legacy-research")
	path := filepath.Join(legacy, ".local", "switch_recon", "ms2", "update", "romfs_full", "masterNX64")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "GxArchivedFile074.dat"), []byte("GDATxxxx"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MS2_SWITCH_MASTER", path)
	if got := ResolveMaster(repo, "ms2", ""); got != "" {
		t.Fatalf("implicit external source: %s", got)
	}
	local := filepath.Join(repo, ".local", "switch_sources", "ms2", "latest_update", "masterNX64")
	if err := os.MkdirAll(local, 0700); err != nil {
		t.Fatal(err)
	}
	if got := ResolveMaster(repo, "ms2", ""); got != local {
		t.Fatalf("local cache not preferred: %s", got)
	}
}
