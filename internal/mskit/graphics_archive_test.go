package mskit

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func tinyTID(t *testing.T, magic byte, fill byte) []byte {
	t.Helper()
	const w, h = 4, 4
	b := make([]byte, 0x80+16)
	copy(b[:4], []byte{'T', 'I', 'D', magic})
	if magic == 0x90 {
		binary.LittleEndian.PutUint32(b[0x44:0x48], w)
		binary.LittleEndian.PutUint32(b[0x48:0x4c], h)
		copy(b[0x64:0x68], []byte("DXT5"))
	} else {
		binary.BigEndian.PutUint32(b[0x44:0x48], w)
		binary.BigEndian.PutUint32(b[0x48:0x4c], h)
		copy(b[0x64:0x68], []byte("5TXD"))
	}
	for i := 0x80; i < len(b); i++ {
		b[i] = fill
	}
	return b
}

func graphicsTestCL3(t *testing.T, tid []byte, name string) []byte {
	t.Helper()
	const sections = 0x30
	const coll = 0xD0
	payloads := [][]byte{[]byte("SENTINEL-LEFT"), tid, []byte("SENTINEL-RIGHT")}
	names := []string{"keep_before.bin", name, "keep_after.bin"}
	count := len(payloads)
	dataSize := CL3EntrySize * count
	for _, p := range payloads {
		dataSize += len(p)
	}
	total := coll + dataSize + 7
	b := make([]byte, total)
	copy(b[:0x10], bytes.Repeat([]byte{0xCC}, 0x10))
	copy(b[0x10:0x13], []byte("CL3"))
	b[0x13] = 'L'
	binary.LittleEndian.PutUint32(b[0x18:0x1c], 3)
	binary.LittleEndian.PutUint32(b[0x1c:0x20], 2)
	binary.LittleEndian.PutUint32(b[0x20:0x24], sections)
	binary.LittleEndian.PutUint32(b[0x24:0x28], 0xAABBCCDD)
	writeSection := func(rec int, sectionName string, n, size, off int) {
		copy(b[rec:rec+0x20], []byte(sectionName))
		binary.LittleEndian.PutUint32(b[rec+0x20:rec+0x24], uint32(n))
		binary.LittleEndian.PutUint32(b[rec+0x24:rec+0x28], uint32(size))
		binary.LittleEndian.PutUint32(b[rec+0x28:rec+0x2c], uint32(off))
	}
	writeSection(sections, "FILE_COLLECTION", count, dataSize, coll)
	writeSection(sections+CL3SectionSize, "FILE_LINK", 0, 0, coll+dataSize)
	cursor := CL3EntrySize * count
	for i := range payloads {
		rec := coll + i*CL3EntrySize
		copy(b[rec:rec+CL3NameSize], append([]byte(names[i]), 0))
		binary.LittleEndian.PutUint32(b[rec+0x200:rec+0x204], uint32(0x20+i))
		binary.LittleEndian.PutUint32(b[rec+0x204:rec+0x208], uint32(cursor))
		binary.LittleEndian.PutUint32(b[rec+0x208:rec+0x20c], uint32(len(payloads[i])))
		binary.LittleEndian.PutUint32(b[rec+0x20c:rec+0x210], uint32(i))
		binary.LittleEndian.PutUint32(b[rec+0x210:rec+0x214], uint32(i+1))
		for j := 0x214; j < CL3EntrySize; j++ {
			b[rec+j] = byte(0x50 + i)
		}
		copy(b[coll+cursor:], payloads[i])
		cursor += len(payloads[i])
	}
	copy(b[coll+dataSize:], []byte("CL3TAIL"))
	return b
}

func graphicsTestGDAT(t *testing.T, target []byte, index int) []byte {
	t.Helper()
	entries := make([][]byte, index+2)
	for i := range entries {
		entries[i] = []byte(strings.Repeat(string(rune('a'+i%26)), 9+i%7))
	}
	entries[index] = target
	raw, err := BuildGDAT(entries)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestExtractGraphicsTIDCaseInsensitive(t *testing.T) {
	tid := tinyTID(t, 0x91, 0x11)
	cl3 := graphicsTestCL3(t, tid, "Camp_Top_2.TID")
	dat := graphicsTestGDAT(t, cl3, 3)
	got, loc, err := ExtractGraphicsTID(dat, 3, "camp_top_2.tid")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, tid) || loc.GDATIndex != 3 || loc.CL3Index != 1 || loc.BILZ {
		t.Fatalf("extract drift loc=%+v", loc)
	}
}

func TestPatchGraphicsTIDPlainPreservesOtherMembersAndEntries(t *testing.T) {
	oldTID := tinyTID(t, 0x90, 0x22)
	newTID := tinyTID(t, 0x90, 0x33)
	cl3 := graphicsTestCL3(t, oldTID, "result_2.tid")
	dat := graphicsTestGDAT(t, cl3, 4)
	before, _ := ParseGDAT(dat)
	out, loc, err := PatchGraphicsTID(dat, 4, "RESULT_2.TID", newTID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if loc.BILZ || loc.CL3Index != 1 {
		t.Fatalf("location drift %+v", loc)
	}
	got, _, err := ExtractGraphicsTID(out, 4, "result_2.tid")
	if err != nil || !bytes.Equal(got, newTID) {
		t.Fatalf("patched TID drift err=%v", err)
	}
	after, _ := ParseGDAT(out)
	for i := range before.Entries {
		if i != 4 && !bytes.Equal(before.Entries[i], after.Entries[i]) {
			t.Fatalf("non-target GDAT entry %d changed", i)
		}
	}
}

func TestPatchGraphicsTIDStoredBILZPreservesPrefix(t *testing.T) {
	oldTID := tinyTID(t, 0x90, 0x44)
	newTID := tinyTID(t, 0x90, 0x55)
	cl3 := graphicsTestCL3(t, oldTID, "Bonus_Character_CharacterSelect.tid")
	wrapped, err := WrapBILZGo(cl3, BILZAlgoStored, 9)
	if err != nil {
		t.Fatal(err)
	}
	prefix := bytes.Repeat([]byte{0xA7}, 0x25)
	dat := graphicsTestGDAT(t, append(append([]byte{}, prefix...), wrapped...), 1)
	out, loc, err := PatchGraphicsTID(dat, 1, "bonus_character_characterselect.TID", newTID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !loc.BILZ || loc.BILZAlgo != BILZAlgoStored || loc.PrefixLen != len(prefix) {
		t.Fatalf("stored BILZ location drift %+v", loc)
	}
	g, _ := ParseGDAT(out)
	if !bytes.Equal(g.Entries[1][:len(prefix)], prefix) {
		t.Fatal("opaque BILZ prefix changed")
	}
	got, _, err := ExtractGraphicsTID(out, 1, "Bonus_Character_CharacterSelect.tid")
	if err != nil || !bytes.Equal(got, newTID) {
		t.Fatalf("stored BILZ patched TID drift err=%v", err)
	}
}

func TestPatchGraphicsTIDZlibRequiresCanonicalEncoder(t *testing.T) {
	oldTID := tinyTID(t, 0x90, 0x66)
	newTID := tinyTID(t, 0x90, 0x77)
	cl3 := graphicsTestCL3(t, oldTID, "battle_f_NX.tid")
	wrapped, err := WrapBILZGo(cl3, BILZAlgoZlib, 9)
	if err != nil {
		t.Fatal(err)
	}
	dat := graphicsTestGDAT(t, wrapped, 0)
	if _, _, err := PatchGraphicsTID(dat, 0, "battle_f_NX.tid", newTID, nil); err == nil || !strings.Contains(err.Error(), "canonical zlib BILZ encoder required") {
		t.Fatalf("expected canonical encoder gate, got %v", err)
	}
	out, loc, err := PatchGraphicsTID(dat, 0, "battle_f_NX.tid", newTID, func(data []byte, algo, level int) ([]byte, error) {
		return WrapBILZGo(data, algo, level)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !loc.BILZ || loc.BILZAlgo != BILZAlgoZlib {
		t.Fatalf("zlib BILZ location drift %+v", loc)
	}
	got, _, err := ExtractGraphicsTID(out, 0, "battle_f_NX.tid")
	if err != nil || !bytes.Equal(got, newTID) {
		t.Fatalf("zlib BILZ patched TID drift err=%v", err)
	}
}

func TestPatchGraphicsTIDRejectsMetadataDrift(t *testing.T) {
	oldTID := tinyTID(t, 0x90, 0x88)
	bad := tinyTID(t, 0x91, 0x99)
	dat := graphicsTestGDAT(t, graphicsTestCL3(t, oldTID, "commonBE.tid"), 2)
	if _, _, err := PatchGraphicsTID(dat, 2, "commonBE.tid", bad, nil); err == nil || !strings.Contains(err.Error(), "TID metadata changed") {
		t.Fatalf("expected metadata guard, got %v", err)
	}
}
