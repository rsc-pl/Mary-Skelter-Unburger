package mskit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGDATFileReadMatchesMemoryParser(t *testing.T) {
	entries := [][]byte{[]byte("alpha"), {0, 1, 2, 3}, bytes.Repeat([]byte("z"), 123)}
	raw, err := BuildGDAT(entries)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "GxArchivedFile001.dat")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	gf, err := OpenGDATFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer gf.Close()
	if gf.Count() != len(entries) {
		t.Fatalf("count=%d", gf.Count())
	}
	for i, want := range entries {
		got, err := gf.ReadEntry(i)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("entry %d drift", i)
		}
	}
}
