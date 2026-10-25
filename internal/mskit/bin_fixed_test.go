package mskit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWesternMS2073PureGoExactFixedBIN(t *testing.T) {
	base := privateFixturePath(t, "switch-bin-fixed-samples")
	read := func(name string) []byte {
		t.Helper()
		b, e := os.ReadFile(filepath.Join(base, name))
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	old := read("original-entry0.bin")
	plain := read("rebuilt-bin.bin")
	var input map[string]string
	if e := json.Unmarshal(read("changed-fields.json"), &input); e != nil {
		t.Fatal(e)
	}
	edits := map[int][]byte{}
	for si, text := range input {
		i, e := strconv.Atoi(si)
		if e != nil {
			t.Fatal(e)
		}
		edits[i] = []byte(text)
	}
	if len(edits) != 315 {
		t.Fatalf("expected 315 edits, got %d", len(edits))
	}
	fixed, e := RebuildBINEntryFixedWidthGo(old, edits)
	if e != nil {
		t.Fatal(e)
	}
	if len(fixed) != len(old) {
		t.Fatal("fixed compressed BIN must not grow")
	}
	if !bytes.Equal(fixed[0x90+21435:], old[0x90+21435:]) {
		t.Fatal("opaque suffix moved or changed")
	}
	decoded, e := DecodeOrochiEntry(fixed, "bin")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatal("final BIN differs from 315-edits canonical corpus")
	}
	if bytes.Equal(fixed, old) {
		t.Fatal("no changed text produced")
	}
	t.Log("315 current-PC English translations -> pure Go zlib: exact 26457-byte entry, unchanged 4878-byte suffix PASS")
}
