package mskit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestGoGBNLIndependentGoldenByteParity(t *testing.T) {
	root := privateFixturePath(t, "text-codec-golden-vectors")
	cases := []struct {
		name  string
		edits map[TextFieldID][]byte
	}{
		{"gbnl_shared_divergent", map[TextFieldID][]byte{{0, 0}: []byte("Oyayubi-neesama!"), {1, 0}: []byte("Oyayubi-oneesama!")}},
		{"gbnl_one_sharer", map[TextFieldID][]byte{{0, 0}: []byte("Shirayuki...")}},
		{"gbnl_multi_growth", map[TextFieldID][]byte{{0, 0}: []byte("aaaaaaaaaaaa"), {1, 0}: []byte("bbbbbbbbbbbbbbbbbb"), {2, 0}: []byte("cccccccccccccccccccccccc")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			raw, e := os.ReadFile(filepath.Join(root, c.name+".input.bin"))
			if e != nil {
				t.Fatal(e)
			}
			expected, e := os.ReadFile(filepath.Join(root, c.name+".output.bin"))
			if e != nil {
				t.Fatal(e)
			}
			got, e := RebuildGBNL(raw, c.edits)
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(got, expected) {
				t.Fatalf("golden byte parity mismatch got=%d want=%d", len(got), len(expected))
			}
		})
	}
}
