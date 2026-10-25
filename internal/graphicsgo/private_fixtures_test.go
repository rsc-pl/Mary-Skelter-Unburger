package graphicsgo

import (
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"testing"
)

func privateFixturePath(t *testing.T, key string) string {
	t.Helper()
	root, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".local", "internal", "private_asset_locations.json")); os.IsNotExist(statErr) {
		t.Skip("requires owner-local, SHA-pinned original game fixture; independent public tests still run")
	} else if statErr != nil {
		t.Fatal(statErr)
	}
	path, e := privateassets.Resolve(root, key)
	if e != nil {
		t.Fatal(e)
	}
	return path
}
