package switchtext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

// Finale has records where the original speaker's string is valid but empty.
// Changing empty -> Marchen is not the same as writing to a NULL GBNL field.
func TestNativeFinaleEmptySourceGBNLAndPadding(t *testing.T) {
	original, err := hex.DecodeString("4753544c01000000100000000400000001000000400000000200000004000000010000004800000000000000500000000800000000000000000000000000000000000000000000000500000000000000536e6f772057686974652e2e2e00")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0x50; i < len(original); i++ {
		original[i] = 0
	} // valid empty, not NULL
	fixture := func(blob []byte) ([]byte, string, string) {
		src, e := mskit.BuildSwitchGDAT([][]byte{blob}, make([]byte, 32))
		if e != nil {
			t.Fatal(e)
		}
		h := sha256.Sum256(src)
		root := t.TempDir()
		m := filepath.Join(root, "masterNX64")
		if e = os.MkdirAll(m, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(m, "GxArchivedFile072.dat"), src, 0600); e != nil {
			t.Fatal(e)
		}
		return src, m, hex.EncodeToString(h[:])
	}
	src, master, sha := fixture(original)
	r := nativeRecord{RecordKey: "MSF_SWITCH_WESTERN/a072/g00024/c0/d000100/r00008/f474", SourceDataset: "MSF_SWITCH_WESTERN", SourceFileSHA: sha, Archive: 72, Entry: 0, RawHex: "", Encoding: "utf-8", Text: "Marchen", RecordIndex: 0, FieldIndex: 0}
	r.Meta.Kind = "gnl"
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(t.TempDir(), "candidate.jsonl")
	if err = os.WriteFile(in, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "generated")
	_, err = WriteNativeCandidate(NativeWriteOptions{Game: "finale", StagedEdits: in, SourceMaster: master, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	built, err := os.ReadFile(filepath.Join(out, "GxArchivedFile072.dat"))
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := mskit.ParseSwitchGDAT(built)
	if err != nil {
		t.Fatal(err)
	}
	g, err := mskit.ParseGBNL(parsed.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	name, err := g.Field(0, 0)
	if err != nil || string(name.StrRaw) != "Marchen" {
		t.Fatalf("edited field: %q %v", name.StrRaw, err)
	}
	second, err := g.Field(1, 0)
	if err != nil || second.IsNull || len(second.StrRaw) != 0 {
		t.Fatal("unchanged empty second field lost")
	}
	if !bytes.Equal(parsed.ZeroTail, make([]byte, 32)) {
		t.Fatal("source GDAT padding discarded")
	}
	unchanged, err := os.ReadFile(filepath.Join(master, "GxArchivedFile072.dat"))
	if err != nil || !bytes.Equal(unchanged, src) {
		t.Fatal("pristine source modified")
	}

	// NULL speaker slots must NEVER be treated as the valid empty string.
	nilslot := append([]byte(nil), original...)
	for j := 0; j < 4; j++ {
		nilslot[0x40+j] = 0xff
	}
	_, nilmaster, nilsha := fixture(nilslot)
	r.SourceFileSHA = nilsha
	b, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(in, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	rejected := filepath.Join(t.TempDir(), "reject")
	_, err = WriteNativeCandidate(NativeWriteOptions{Game: "finale", StagedEdits: in, SourceMaster: nilmaster, OutputDir: rejected})
	if err == nil || !strings.Contains(err.Error(), "GBNL original bytes drift") {
		t.Fatalf("NULL source accepted: %v", err)
	}
	if _, err = os.Stat(rejected); !os.IsNotExist(err) {
		t.Fatal("partial candidate published")
	}
}
