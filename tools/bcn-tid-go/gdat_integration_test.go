package main

import (
	"bytes"
	"encoding/binary"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

// A source-derived synthetic CL3/GDAT envelope verifies that the actual bcn
// encoder output can pass the pre-existing Go-only safe GDAT patch route.
func testCL3WithTID(t *testing.T, tid []byte) []byte {
	t.Helper()
	const head = 0x30
	const coll = 0xD0
	payloads := [][]byte{[]byte("PRIVATE-UNCHANGED"), tid}
	names := []string{"sentinel.bin", "result_2.tid"}
	sectionSize := mskit.CL3EntrySize * len(payloads)
	for _, p := range payloads {
		sectionSize += len(p)
	}
	out := make([]byte, coll+sectionSize+7)
	copy(out[0x10:0x13], "CL3")
	out[0x13] = 'L'
	binary.LittleEndian.PutUint32(out[0x18:0x1c], 3)
	binary.LittleEndian.PutUint32(out[0x1c:0x20], 2)
	binary.LittleEndian.PutUint32(out[0x20:0x24], head)
	putSection := func(at int, name string, n, size, off int) {
		copy(out[at:at+0x20], name)
		binary.LittleEndian.PutUint32(out[at+0x20:at+0x24], uint32(n))
		binary.LittleEndian.PutUint32(out[at+0x24:at+0x28], uint32(size))
		binary.LittleEndian.PutUint32(out[at+0x28:at+0x2c], uint32(off))
	}
	putSection(head, "FILE_COLLECTION", len(payloads), sectionSize, coll)
	putSection(head+mskit.CL3SectionSize, "FILE_LINK", 0, 0, coll+sectionSize)
	pos := mskit.CL3EntrySize * len(payloads)
	for i, p := range payloads {
		at := coll + i*mskit.CL3EntrySize
		copy(out[at:at+mskit.CL3NameSize], names[i])
		binary.LittleEndian.PutUint32(out[at+0x200:at+0x204], uint32(0x20+i))
		binary.LittleEndian.PutUint32(out[at+0x204:at+0x208], uint32(pos))
		binary.LittleEndian.PutUint32(out[at+0x208:at+0x20c], uint32(len(p)))
		binary.LittleEndian.PutUint32(out[at+0x20c:at+0x210], uint32(i))
		binary.LittleEndian.PutUint32(out[at+0x210:at+0x214], uint32(i+1))
		copy(out[coll+pos:], p)
		pos += len(p)
	}
	copy(out[coll+sectionSize:], "CL3TAIL")
	return out
}
func TestRealBCNIntoNativeSafeGDAT(t *testing.T) {
	tid, rgba := testTinyTID(t)
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			i := (y*8 + x) * 4
			rgba[i], rgba[i+1], rgba[i+2] = 125, 29, 66
		}
	}
	changed, report, e := EncodeChangedBC7(tid, rgba, 6, 64, 64)
	if e != nil {
		t.Fatal(e)
	}
	if report.ChangedBlocks != 1 {
		t.Fatalf("changed block count %d", report.ChangedBlocks)
	}
	cl3 := testCL3WithTID(t, tid)
	src, e := mskit.BuildGDAT([][]byte{[]byte("UNTOUCHED-GDAT"), cl3, []byte("UNTOUCHED-TAIL")})
	if e != nil {
		t.Fatal(e)
	}
	result, loc, e := mskit.PatchGraphicsTIDGoSafe(src, 1, "result_2.tid", changed)
	if e != nil {
		t.Fatal(e)
	}
	if loc.GDATIndex != 1 || loc.BILZ {
		t.Fatalf("wrong location %+v", loc)
	}
	before, _ := mskit.ParseGDAT(src)
	after, e := mskit.ParseGDAT(result)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(before.Entries[0], after.Entries[0]) || !bytes.Equal(before.Entries[2], after.Entries[2]) {
		t.Fatal("untouched GDAT member drift")
	}
	extracted, _, e := mskit.ExtractGraphicsTID(result, 1, "result_2.tid")
	if e != nil || !bytes.Equal(extracted, changed) {
		t.Fatal("TID lost in native GDAT roundtrip", e)
	}
	parsed, e := mskit.ParseCL3(after.Entries[1])
	if e != nil {
		t.Fatal(e)
	}
	sentinel, e := parsed.FileData(0)
	if e != nil || string(sentinel) != "PRIVATE-UNCHANGED" {
		t.Fatal("CL3 sibling member drift", e)
	}
}
