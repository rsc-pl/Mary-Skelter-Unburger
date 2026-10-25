package maintainer

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

// Minimal valid single-member CL3 fixture, independent of the game binaries.
func gapFixtureCL3(name string, tid []byte) []byte {
	const sections = 0x30
	const coll = 0xD0
	dataSize := mskit.CL3EntrySize + len(tid)
	b := make([]byte, coll+dataSize)
	copy(b[0x10:0x13], []byte("CL3"))
	b[0x13] = 'L'
	binary.LittleEndian.PutUint32(b[0x18:0x1c], 3)
	binary.LittleEndian.PutUint32(b[0x1c:0x20], 2)
	binary.LittleEndian.PutUint32(b[0x20:0x24], sections)
	copy(b[sections:sections+0x20], []byte("FILE_COLLECTION"))
	binary.LittleEndian.PutUint32(b[sections+0x20:sections+0x24], 1)
	binary.LittleEndian.PutUint32(b[sections+0x24:sections+0x28], uint32(dataSize))
	binary.LittleEndian.PutUint32(b[sections+0x28:sections+0x2c], coll)
	copy(b[sections+mskit.CL3SectionSize:sections+mskit.CL3SectionSize+0x20], []byte("FILE_LINK"))
	copy(b[coll:coll+mskit.CL3NameSize], []byte(name))
	binary.LittleEndian.PutUint32(b[coll+0x204:coll+0x208], mskit.CL3EntrySize)
	binary.LittleEndian.PutUint32(b[coll+0x208:coll+0x20c], uint32(len(tid)))
	copy(b[coll+mskit.CL3EntrySize:], tid)
	return b
}
func TestGraphicsGapReadsThreePristinePCArchivesAndMatchesOnlyExactSHA(t *testing.T) {
	root := t.TempDir()
	type item struct {
		id, ds         string
		archive, index int
		inner          string
	}
	specs := []item{
		{"MS1_INCLUDED_RESIDENCE_NAMES", "MS2_PC_LOCALIZED", 55, 1883, "menu_kyojuu.tid"},
		{"MS2_TITLE_MOD_FOOTER_MAIN", "MS2_PC_LOCALIZED", 55, 1821, "titlemenu.tid"},
		{"MS2_TITLE_MOD_FOOTER_BE", "MS2_PC_LOCALIZED", 55, 1822, "TitleBE.tid"},
		{"MSF_EQUIPMENT_HEADER_NAMES", "MSF_PC_LOCALIZED", 74, 8, "commonBE.tid"},
		{"MSF_TITLE_MOD_FOOTER", "MSF_PC_LOCALIZED", 73, 220, "title_f.tid"},
	}
	manifests := []map[string]any{}
	archives := map[int][][]byte{}
	for _, it := range specs {
		tid := []byte("pristine-test-" + it.id)
		sha := sha256.Sum256(tid)
		manifests = append(manifests, map[string]any{"id": it.id, "dataset": it.ds, "archive": it.archive, "enabled": true, "gdat_index": it.index, "cl3_inner_name": it.inner, "source_variants": []any{map[string]any{"tid_sha256": hex.EncodeToString(sha[:]), "width": 4, "height": 4, "format": "DXT5"}}})
		e := archives[it.archive]
		if len(e) <= it.index {
			next := make([][]byte, it.index+1)
			copy(next, e)
			e = next
		}
		e[it.index] = gapFixtureCL3(it.inner, tid)
		archives[it.archive] = e
	}
	if err := os.Mkdir(filepath.Join(root, "graphics"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"schema_version": 2, "assets": manifests})
	if err := os.WriteFile(filepath.Join(root, "graphics", "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	ms2 := filepath.Join(root, "ms2")
	finale := filepath.Join(root, "finale")
	for _, p := range []string{filepath.Join(ms2, "masterWindows"), filepath.Join(finale, "masterWindows")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for archive, entries := range archives {
		for i := range entries {
			if entries[i] == nil {
				entries[i] = []byte("absent-entry")
			}
		}
		dat, err := mskit.BuildGDAT(entries)
		if err != nil {
			t.Fatal(err)
		}
		p := finale
		if archive == 55 {
			p = ms2
		}
		p = filepath.Join(p, "masterWindows", "GxArchivedFile"+map[int]string{55: "055", 73: "073", 74: "074"}[archive]+".dat")
		if err = os.WriteFile(p, dat, 0600); err != nil {
			t.Fatal(err)
		}
	}
	masters := GraphicsGapMasters{MS2: ms2, Finale: finale}
	r, e := GraphicsSourceGapAudit(root, masters)
	if e != nil || r.Recovered != 5 || r.Status != "ALL_FIVE_EXACT_ORIGINALS_FOUND" {
		t.Fatalf("all exact: %+v %v", r, e)
	}
	for _, v := range r.Items {
		if v.Status != "EXACT_ORIGINAL_MATCH" || v.ActualSHA != v.ExpectedSHA {
			t.Fatalf("bad match: %+v", v)
		}
	}
	// Changed pristine source SHA must never be promoted to exact.
	for i := range manifests {
		if manifests[i]["id"] == "MSF_TITLE_MOD_FOOTER" {
			q := manifests[i]["source_variants"].([]any)[0].(map[string]any)
			q["tid_sha256"] = strings.Repeat("a", 64)
		}
	}
	raw, _ = json.Marshal(map[string]any{"schema_version": 2, "assets": manifests})
	_ = os.WriteFile(filepath.Join(root, "graphics", "manifest.json"), raw, 0600)
	r, e = GraphicsSourceGapAudit(root, masters)
	if e != nil || r.Recovered != 4 {
		t.Fatalf("mismatch incorrectly accepted: %+v %v", r, e)
	}
	foundMismatch := false
	for _, v := range r.Items {
		if v.ID == "MSF_TITLE_MOD_FOOTER" && v.Status == "SHA_MISMATCH" {
			foundMismatch = true
		}
	}
	if !foundMismatch {
		t.Fatal("missing strict mismatch result")
	}
	// Missing source must report HOLD; no scan of stale modified Switch CL3.
	if err := os.Remove(filepath.Join(finale, "masterWindows", "GxArchivedFile074.dat")); err != nil {
		t.Fatal(err)
	}
	r, e = GraphicsSourceGapAudit(root, masters)
	if e != nil || r.Recovered != 3 {
		t.Fatalf("missing pristine archive %+v %v", r, e)
	}
	// Symlink DAT is unsafe, even if it resolves to an otherwise correct master.
	good := filepath.Join(ms2, "masterWindows", "GxArchivedFile055.dat")
	link := filepath.Join(finale, "masterWindows", "GxArchivedFile074.dat")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	r, e = GraphicsSourceGapAudit(root, masters)
	if e != nil {
		t.Fatal(e)
	}
	seenUnsafe := false
	for _, v := range r.Items {
		if v.ID == "MSF_EQUIPMENT_HEADER_NAMES" && v.Status == "UNSAFE_OR_NONREGULAR_ARCHIVE" {
			seenUnsafe = true
		}
	}
	if !seenUnsafe {
		t.Fatalf("symlink source accepted: %+v", r)
	}
}
func TestGraphicsGapRejectsDuplicateOrUnapprovedManifest(t *testing.T) {
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "graphics"), 0700)
	_ = os.WriteFile(filepath.Join(root, "graphics", "manifest.json"), []byte(`{"schema_version":2,"assets":[]}`), 0600)
	if _, err := GraphicsSourceGapAudit(root, GraphicsGapMasters{}); err == nil {
		t.Fatal("empty manifest accepted")
	}
}
