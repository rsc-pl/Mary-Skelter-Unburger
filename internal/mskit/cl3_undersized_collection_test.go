package mskit

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// Regression for actual MS2 PC GxArchivedFile055.dat g1448/camp_top.tid:
// the sum of file sizes may be larger than the FILE_COLLECTION logical
// DataSize (the reported difference was 232 bytes). Python's empty slice
// semantics used by the accepted CL3 reference must not panic in Go.
func TestCL3RebuildUndersizedCollectionPythonSliceParity(t *testing.T) {
	{
		bo := binary.LittleEndian
		original := graphicsTestCL3(t, tinyTID(t, 0x90, 0x45), "camp_top.tid")
		// Make file-content accounting 232 bytes longer than section DataSize.
		// The physically present source and all file extents remain intact.
		const sectionRecord, under = 0x30, 232
		oldSize := bo.Uint32(original[sectionRecord+0x24 : sectionRecord+0x28])
		if oldSize < under {
			t.Fatal("fixture too small")
		}
		bo.PutUint32(original[sectionRecord+0x24:sectionRecord+0x28], oldSize-under)
		parsed, err := ParseCL3(original)
		if err != nil {
			t.Fatal(err)
		}
		beforeLeft, _ := parsed.FileData(0)
		beforeRight, _ := parsed.FileData(2)
		edited := tinyTID(t, 0x90, 0x95)
		got, err := parsed.Rebuild(map[int][]byte{1: edited})
		if err != nil {
			t.Fatal(err)
		}
		after, err := ParseCL3(got)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := after.FileData(0)
		b, _ := after.FileData(1)
		c, _ := after.FileData(2)
		if !bytes.Equal(a, beforeLeft) || !bytes.Equal(b, edited) || !bytes.Equal(c, beforeRight) {
			t.Fatal("CL3 member data changed outside selected replacement")
		}
		if after.Sections[after.Collection].DataSize != len(edited)+len(a)+len(c)+3*CL3EntrySize {
			t.Fatalf("logical collection length got=%d expected=%d (old size=%d)", after.Sections[after.Collection].DataSize, len(edited)+len(a)+len(c)+3*CL3EntrySize, oldSize)
		}
		if dir := os.Getenv("MS_CL3_PARITY_DUMP_DIR"); dir != "" {
			if err = os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			suffix := "le"
			for key, payload := range map[string][]byte{"source": original, "replacement": edited, "go": got} {
				if err = os.WriteFile(filepath.Join(dir, suffix+"_"+key+".bin"), payload, 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestGraphicsPatchTIDUndersizedCollectionDoesNotPanic(t *testing.T) {
	oldTID := tinyTID(t, 0x90, 0x41)
	newTID := tinyTID(t, 0x90, 0x83)
	raw := graphicsTestCL3(t, oldTID, "camp_top.tid")
	const collectionSection = 0x30
	originalSize := binary.LittleEndian.Uint32(raw[collectionSection+0x24 : collectionSection+0x28])
	binary.LittleEndian.PutUint32(raw[collectionSection+0x24:collectionSection+0x28], originalSize-232)
	original := graphicsTestGDAT(t, raw, 3)
	result, loc, err := PatchGraphicsTID(original, 3, "camp_top.tid", newTID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loc.GDATIndex != 3 || loc.CL3Index != 1 {
		t.Fatalf("target drift %+v", loc)
	}
	gotTID, _, err := ExtractGraphicsTID(result, 3, "camp_top.tid")
	if err != nil || !bytes.Equal(gotTID, newTID) {
		t.Fatalf("patched TID mismatch: %v", err)
	}
	g0, _ := ParseGDAT(original)
	g1, _ := ParseGDAT(result)
	for i := range g0.Entries {
		if i != 3 && !bytes.Equal(g0.Entries[i], g1.Entries[i]) {
			t.Fatalf("unrelated GDAT entry %d changed", i)
		}
	}
}
