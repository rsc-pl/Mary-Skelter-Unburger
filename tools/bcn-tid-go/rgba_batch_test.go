package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func writeRGBABatchFixture(t *testing.T) (string, GDATRGBAManifest, []byte, []byte) {
	t.Helper()
	d := t.TempDir()
	originalBC7, rgbaBC7 := testTinyTID(t)
	originalBC3, rgbaBC3 := tinyDXT5(t)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			at := (y*8 + x) * 4
			rgbaBC7[at], rgbaBC7[at+1], rgbaBC7[at+2] = 128, 42, 91
			rgbaBC3[at], rgbaBC3[at+1], rgbaBC3[at+2] = 72, 129, 187
		}
	}
	aCL3 := testCL3WithTID(t, originalBC7)
	bCL3 := testCL3WithTID(t, originalBC3)
	a, e := mskit.BuildGDAT([][]byte{[]byte("guard-a"), aCL3, []byte("guard-b")})
	if e != nil {
		t.Fatal(e)
	}
	b, e := mskit.BuildGDAT([][]byte{[]byte("guard-c"), bCL3, []byte("guard-d")})
	if e != nil {
		t.Fatal(e)
	}
	for name, v := range map[string][]byte{"a.gdat": a, "b.gdat": b, "a.rgba": rgbaBC7, "b.rgba": rgbaBC3} {
		if e := os.WriteFile(filepath.Join(d, name), v, 0600); e != nil {
			t.Fatal(e)
		}
	}
	m := GDATRGBAManifest{Version: 1, Jobs: []GDATRGBAJob{
		{Source: "a.gdat", SourceSHA256: digest(a), Output: "a.out", Edits: []GDATRGBAEdit{{GDATIndex: 1, Inner: "result_2.tid", SourceTIDSHA256: digest(originalBC7), TargetRGBA: "a.rgba", TargetRGBASHA256: digest(rgbaBC7)}}},
		{Source: "b.gdat", SourceSHA256: digest(b), Output: "b.out", Edits: []GDATRGBAEdit{{GDATIndex: 1, Inner: "result_2.tid", SourceTIDSHA256: digest(originalBC3), TargetRGBA: "b.rgba", TargetRGBASHA256: digest(rgbaBC3)}}},
	}}
	return d, m, originalBC7, originalBC3
}
func writeRGBAManifest(t *testing.T, d string, m GDATRGBAManifest) string {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(d, "manifest.json")
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestNativeRawRGBABatchTwoFormatsAndPreservation(t *testing.T) {
	d, m, originalBC7, originalBC3 := writeRGBABatchFixture(t)
	path := writeRGBAManifest(t, d, m)
	r, e := RunGDATRGBABatch(path)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Jobs) != 2 || r.Status != "PASS_NATIVE_GO_RGBA_TO_BC7_DXT5_GDAT_BATCH" {
		t.Fatalf("bad report %+v", r)
	}
	for i, j := range m.Jobs {
		old, _ := os.ReadFile(filepath.Join(d, j.Source))
		output, e := os.ReadFile(filepath.Join(d, j.Output))
		if e != nil {
			t.Fatal(e)
		}
		before, _ := mskit.ParseGDAT(old)
		after, e := mskit.ParseGDAT(output)
		if e != nil {
			t.Fatal(e)
		}
		if len(after.Entries) != 3 || !bytes.Equal(before.Entries[0], after.Entries[0]) || !bytes.Equal(before.Entries[2], after.Entries[2]) {
			t.Fatal("unrelated GDAT record drift")
		}
		tid, _, e := mskit.ExtractGraphicsTID(output, 1, "result_2.tid")
		if e != nil {
			t.Fatal(e)
		}
		original := originalBC7
		if i == 1 {
			original = originalBC3
		}
		if !bytes.Equal(tid[:128], original[:128]) {
			t.Fatal("TID header drift")
		}
		if r.Jobs[i].TIDs[0].ChangedBlocks != 1 || r.Jobs[i].TIDs[0].MaxVisibleError > 64 || r.Jobs[i].TIDs[0].MaxAlphaError > 64 {
			t.Fatalf("changed block guard failed: %+v", r.Jobs[i].TIDs[0])
		}
		if !bytes.Equal(tid[128+16:], original[128+16:]) {
			t.Fatal("unmodified compressed blocks drift")
		}
		parsed, e := mskit.ParseCL3(after.Entries[1])
		if e != nil {
			t.Fatal(e)
		}
		sibling, e := parsed.FileData(0)
		if e != nil || string(sibling) != "PRIVATE-UNCHANGED" {
			t.Fatal("CL3 untouched sibling drift")
		}
	}
	if _, e := RunGDATRGBABatch(path); e == nil || !strings.Contains(e.Error(), "exists") {
		t.Fatalf("existing output not refused: %v", e)
	}
}
func TestNativeRawRGBABatchRollbackAndGuards(t *testing.T) {
	d, m, _, _ := writeRGBABatchFixture(t)
	path := writeRGBAManifest(t, d, m)
	_, e := RunGDATRGBABatchWithHook(path, func(n int) error {
		if n == 1 {
			return errors.New("injected after first publish")
		}
		return nil
	})
	if e == nil || !strings.Contains(e.Error(), "injected") {
		t.Fatalf("fault injection ignored: %v", e)
	}
	for _, name := range []string{"a.out", "b.out"} {
		if _, e := os.Lstat(filepath.Join(d, name)); !os.IsNotExist(e) {
			t.Fatalf("partial output left behind %s: %v", name, e)
		}
	}
	m.Jobs[0].Edits[0].TargetRGBASHA256 = strings.Repeat("0", 64)
	path = writeRGBAManifest(t, d, m)
	if _, e := RunGDATRGBABatch(path); e == nil || !strings.Contains(e.Error(), "size/SHA") {
		t.Fatalf("RGBA drift ignored: %v", e)
	}
	m.Jobs[0].Edits[0].TargetRGBASHA256 = digest(mustReadRGBA(t, filepath.Join(d, "a.rgba")))
	m.Jobs[1].Output = m.Jobs[0].Output
	path = writeRGBAManifest(t, d, m)
	if _, e := RunGDATRGBABatch(path); e == nil || !strings.Contains(e.Error(), "alias") {
		t.Fatalf("duplicate output allowed: %v", e)
	}
	m.Jobs[1].Output = "b.out"
	m.Jobs[0].Edits[0].TargetRGBA = "a.out"
	path = writeRGBAManifest(t, d, m)
	if _, e := RunGDATRGBABatch(path); e == nil {
		t.Fatal("self-referential output accepted")
	}
}
func mustReadRGBA(t *testing.T, path string) []byte {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return b
}

func TestRGBAAllJobPreflightBeforeFirstEncode(t *testing.T) {
	d, m, _, _ := writeRGBABatchFixture(t)
	// Job 1 is invalid even though job 0 is perfectly valid. The validator
	// must find the conflict before touching the first output or encoder.
	m.Jobs[1].Output = "a.out"
	p := writeRGBAManifest(t, d, m)
	if _, e := RunGDATRGBABatch(p); e == nil || !strings.Contains(e.Error(), "duplicated/aliased") {
		t.Fatalf("late duplicate not rejected: %v", e)
	}
	if _, e := os.Lstat(filepath.Join(d, "a.out")); !os.IsNotExist(e) {
		t.Fatal("first output published before later preflight")
	}
	m.Jobs[1].Output = "b.out"
	m.Jobs[0].Edits[0].TargetRGBA = "b.out"
	p = writeRGBAManifest(t, d, m)
	if _, e := RunGDATRGBABatch(p); e == nil || !strings.Contains(e.Error(), "RGBA file aliases batch output") {
		t.Fatalf("cross-job output-as-RGBA accepted: %v", e)
	}
	if _, e := os.Lstat(filepath.Join(d, "a.out")); !os.IsNotExist(e) {
		t.Fatal("early output published")
	}
	m.Jobs[0].Edits[0].TargetRGBA = "a.rgba"
	m.Jobs[1].Output = "a.gdat"
	p = writeRGBAManifest(t, d, m)
	if _, e := RunGDATRGBABatch(p); e == nil || !(strings.Contains(e.Error(), "output aliases batch input") || strings.Contains(e.Error(), "output exists")) {
		t.Fatalf("source overwrite alias accepted: %v", e)
	}
	// Existing dangling symlinks must be protected, too.
	m.Jobs[1].Output = "b.out"
	m.Jobs[0].Output = "owner-link"
	if e := os.Symlink("missing", filepath.Join(d, "owner-link")); e != nil {
		t.Skip(e)
	}
	p = writeRGBAManifest(t, d, m)
	if _, e := RunGDATRGBABatch(p); e == nil || !strings.Contains(e.Error(), "output exists") {
		t.Fatalf("dangling symlink overwritten: %v", e)
	}
}
