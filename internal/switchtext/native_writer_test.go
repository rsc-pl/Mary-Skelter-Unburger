package switchtext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func TestGoNativeGDATWesternENCRealSample(t *testing.T) {
	root := t.TempDir()
	master := filepath.Join(root, "masterNX64")
	if e := os.Mkdir(master, 0700); e != nil {
		t.Fatal(e)
	}
	src, e := os.ReadFile(privateFixturePath(t, "switch-enc-ms2-original"))
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := mskit.ParseENCEntry(src)
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := parsed.Rows[0].DecodedPayload()
	if e != nil {
		t.Fatal(e)
	}
	old := decoded[2055996 : 2055996+67]
	entries := [][]byte{[]byte("unchanged byte-exact entry"), src}
	gd, e := mskit.BuildGDAT(entries)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(gd)
	if e = os.WriteFile(filepath.Join(master, "GxArchivedFile073.dat"), gd, 0600); e != nil {
		t.Fatal(e)
	}
	row := nativeRecord{RecordKey: "MS2_SWITCH_WESTERN/a073/g00001/top/row0000/r00000/f2287", SourceDataset: "MS2_SWITCH_WESTERN", SourceFileSHA: hex.EncodeToString(sum[:]), Archive: 73, Entry: 1, RawHex: hex.EncodeToString(old), Encoding: "utf-8", Text: strings.Repeat("N", 139)}
	ri, off := 0, 2055996
	row.Meta.Kind = "enc_row"
	row.Meta.Row = &ri
	row.Meta.StrOffset = &off
	record, e := json.Marshal(row)
	if e != nil {
		t.Fatal(e)
	}
	input := filepath.Join(root, "edited.jsonl")
	if e = os.WriteFile(input, append(record, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	out := filepath.Join(root, "out")
	rep, e := WriteNativeCandidate(NativeWriteOptions{Game: "ms2", StagedEdits: input, SourceMaster: master, OutputDir: out})
	if e != nil {
		t.Fatal(e)
	}
	if len(rep.Archives) != 1 || rep.Archives[0].EditedFields != 1 {
		t.Fatalf("report %+v", rep)
	}
	rebuiltBytes, e := os.ReadFile(filepath.Join(out, "GxArchivedFile073.dat"))
	if e != nil {
		t.Fatal(e)
	}
	rebuilt, e := mskit.ParseGDAT(rebuiltBytes)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(rebuilt.Entries[0], entries[0]) {
		t.Fatal("untouched entry drift")
	}
	changed, e := mskit.ParseENCEntry(rebuilt.Entries[1])
	if e != nil {
		t.Fatal(e)
	}
	got, e := changed.Rows[0].DecodedPayload()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got[:off], decoded[:off]) || string(got[off:off+139]) != strings.Repeat("N", 139) || !bytes.Equal(got[off+140:], decoded[off+140:]) {
		t.Fatal("real Western ENC edited byte boundary mismatch")
	}
	if !bytes.Equal(gd, mustRead(t, filepath.Join(master, "GxArchivedFile073.dat"))) {
		t.Fatal("original source modified")
	}
	// Fail without publishing anything for mismatched hash / unsupported STCM.
	bad := row
	bad.SourceFileSHA = strings.Repeat("0", 64)
	b, e := json.Marshal(bad)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(input, append(b, '\n'), 0600)
	_, e = WriteNativeCandidate(NativeWriteOptions{Game: "ms2", StagedEdits: input, SourceMaster: master, OutputDir: filepath.Join(root, "bad")})
	if e == nil || !strings.Contains(e.Error(), "SHA mismatch") {
		t.Fatalf("bad hash accepted %v", e)
	}
	if _, e = os.Stat(filepath.Join(root, "bad")); !os.IsNotExist(e) {
		t.Fatal("failed candidate materialized")
	}
	bad = row
	bo := 128
	bad.Meta.BlobOffset = &bo
	bad.Meta.Kind = "gnl"
	b, e = json.Marshal(bad)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(input, append(b, '\n'), 0600)
	_, e = WriteNativeCandidate(NativeWriteOptions{Game: "ms2", StagedEdits: input, SourceMaster: master, OutputDir: filepath.Join(root, "unsupported")})
	if e == nil || !strings.Contains(e.Error(), "bare GBNL") {
		t.Fatalf("unsupported container accepted: %v", e)
	}
	if _, e = os.Stat(filepath.Join(root, "unsupported")); !os.IsNotExist(e) {
		t.Fatal("partial output materialized")
	}
	fmt.Println("native Go original Western ENC + full GDAT roundtrip PASS")
}
func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	v, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
