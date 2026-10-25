package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"maryskelter-unburger/internal/mskit"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeNameBankFixture(t *testing.T) (string, string, NameFamilyManifest, map[string][]byte) {
	t.Helper()
	root := os.Getenv("MS_PRIVATE_PROJECT_ROOT")
	if root == "" {
		t.Skip("private source tree required")
	}
	dir := t.TempDir()
	records := make([][]byte, 1858)
	for i := range records {
		records[i] = []byte{byte(i), byte(i >> 8), 'R', 'E', 'C'}
	}
	originals := map[string][]byte{}
	for id, sp := range nameFamilyIdentities {
		sourceKey := map[string]string{"MS2_RESULT_NAMES": "source-result-tid", "MS2_BATTLE_NAMES": "source-battle-tid", "MS2_CAMP_CHARACTER_NAMES": "source-camp-tid"}[id]
		originalPath, e := privateassets.Resolve(root, sourceKey)
		if e != nil {
			t.Fatal(e)
		}
		b, e := os.ReadFile(originalPath)
		if e != nil {
			t.Fatal(e)
		}
		if digest(b) != sp.sha {
			t.Fatalf("source drift %s", id)
		}
		cl3 := testCL3WithTID(t, b)
		off := 0xD0 + mskit.CL3EntrySize
		for i := 0; i < mskit.CL3NameSize; i++ {
			cl3[off+i] = 0
		}
		copy(cl3[off:off+mskit.CL3NameSize], sp.inner)
		records[sp.index] = cl3
		originals[id] = b
	}
	raw, e := mskit.BuildGDAT(records)
	if e != nil {
		t.Fatal(e)
	}
	src := filepath.Join(dir, "source.gdat")
	if e = os.WriteFile(src, raw, 0600); e != nil {
		t.Fatal(e)
	}
	m := NameFamilyManifest{Version: 1, ProjectRoot: root, Jobs: []NameFamilyJob{{Source: "source.gdat", SourceSHA256: digest(raw), Output: "output.gdat", Families: []string{"MS2_RESULT_NAMES", "MS2_BATTLE_NAMES", "MS2_CAMP_CHARACTER_NAMES"}}}}
	return dir, src, m, originals
}
func saveNameBankManifest(t *testing.T, dir string, m NameFamilyManifest) string {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(dir, "families.json")
	if e = os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func syntheticBankRaster(_ string, id string, source []byte) ([]byte, error) {
	meta, e := mskit.ParseTIDMeta(source)
	if e != nil {
		return nil, e
	}
	var rgba []byte
	if meta.Format == "BC7" {
		rgba, e = mskit.DecodeBC7RGBA(source[128:], meta.Width, meta.Height)
	} else {
		rgba, e = mskit.DecodeDXT5RGBA(source[128:], meta.Width, meta.Height)
	}
	if e != nil {
		return nil, e
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			k := (y*meta.Width + x) * 4
			rgba[k], rgba[k+1], rgba[k+2], rgba[k+3] = 85, 130, 170, 255
		}
	}
	return rgba, nil
}
func TestNameBankThreeSourceAtlasOneGDATNativeTransaction(t *testing.T) {
	d, src, m, orig := makeNameBankFixture(t)
	p := saveNameBankManifest(t, d, m)
	report, e := runNameFamilyBatch(p, syntheticBankRaster, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Jobs) != 1 || len(report.Jobs[0].TIDs) != 3 {
		t.Fatalf("unexpected transaction: %+v", report)
	}
	old, e := os.ReadFile(src)
	if e != nil {
		t.Fatal(e)
	}
	out, e := os.ReadFile(filepath.Join(d, "output.gdat"))
	if e != nil {
		t.Fatal(e)
	}
	a, e := mskit.ParseGDAT(old)
	if e != nil {
		t.Fatal(e)
	}
	b, e := mskit.ParseGDAT(out)
	if e != nil {
		t.Fatal(e)
	}
	for k := range a.Entries {
		if k != 1331 && k != 1333 && k != 1431 && !bytes.Equal(a.Entries[k], b.Entries[k]) {
			t.Fatalf("unrelated GDAT drift at %d", k)
		}
	}
	for i, id := range m.Jobs[0].Families {
		sp := nameFamilyIdentities[id]
		tid, _, e := mskit.ExtractGraphicsTID(out, sp.index, sp.inner)
		if e != nil {
			t.Fatal(e)
		}
		original := orig[id]
		if !bytes.Equal(tid[:128], original[:128]) || !bytes.Equal(tid[144:], original[144:]) {
			t.Fatalf("unmodified TID bytes drift for %s", id)
		}
		if report.Jobs[0].TIDs[i].ChangedBlocks != 1 {
			t.Fatalf("changed block count %s %+v", id, report.Jobs[0].TIDs[i])
		}
		cl3, e := mskit.ParseCL3(b.Entries[sp.index])
		if e != nil {
			t.Fatal(e)
		}
		sib, e := cl3.FileData(0)
		if e != nil || string(sib) != "PRIVATE-UNCHANGED" {
			t.Fatalf("CL3 sibling drift %s", id)
		}
	}
	t.Log("source-verified, three original TIDs, one 1858-record synthetic GDAT, 1855 untouched records, source TID headers and other blocks exact")
}
func TestNameBankFailClosedOnSourceDriftAndMissingFonts(t *testing.T) {
	d, _, m, _ := makeNameBankFixture(t)
	m.Jobs[0].SourceSHA256 = strings.Repeat("0", 64)
	p := saveNameBankManifest(t, d, m)
	if _, e := runNameFamilyBatch(p, syntheticBankRaster, nil); e == nil || !strings.Contains(e.Error(), "SHA drift") {
		t.Fatalf("source drift allowed: %v", e)
	}
	m.Jobs[0].SourceSHA256 = digest(mustReadRGBA(t, filepath.Join(d, "source.gdat")))
	m.Jobs[0].Families = []string{"MS2_RESULT_NAMES", "MS2_RESULT_NAMES"}
	p = saveNameBankManifest(t, d, m)
	if _, e := runNameFamilyBatch(p, syntheticBankRaster, nil); e == nil || !strings.Contains(e.Error(), "duplicate") {
		t.Fatalf("duplicate family allowed: %v", e)
	}
	m.Jobs[0].Families = []string{"MS2_RESULT_NAMES"}
	p = saveNameBankManifest(t, d, m)
	if _, e := runNameFamilyBatch(p, func(_, _ string, _ []byte) ([]byte, error) { return nil, errors.New("font missing") }, nil); e == nil || !strings.Contains(e.Error(), "font missing") {
		t.Fatalf("renderer error ignored: %v", e)
	}
	if _, e := os.Lstat(filepath.Join(d, "output.gdat")); !os.IsNotExist(e) {
		t.Fatalf("output created on render failure: %v", e)
	}
}
func TestNameBankAbortPublicationRestoresZeroOutputs(t *testing.T) {
	d, _, m, _ := makeNameBankFixture(t)
	m.Jobs = append(m.Jobs, m.Jobs[0])
	m.Jobs[1].Output = "other.gdat"
	m.Jobs[1].Source = "source-two.gdat"
	if e := os.WriteFile(filepath.Join(d, "source-two.gdat"), mustReadRGBA(t, filepath.Join(d, "source.gdat")), 0600); e != nil {
		t.Fatal(e)
	}
	p := saveNameBankManifest(t, d, m)
	_, e := runNameFamilyBatch(p, syntheticBankRaster, func(n int) error {
		if n == 1 {
			return errors.New("injected failure")
		}
		return nil
	})
	if e == nil || !strings.Contains(e.Error(), "injected") {
		t.Fatalf("hook ignored: %v", e)
	}
	for _, name := range []string{"output.gdat", "other.gdat"} {
		if _, e := os.Lstat(filepath.Join(d, name)); !os.IsNotExist(e) {
			t.Fatalf("partial publish after failure %s: %v", name, e)
		}
	}
}
