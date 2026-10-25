package mskit

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func stcmDeadCodeFixture() ([]byte, int, int, int) {
	dataOff := 0x70
	codeMark := 0x90
	start := codeMark + len("CODE_START_\x00")
	hidden := start + 0x10
	target := hidden + 0x1c
	expMark := target + 0x10
	expOff := expMark + len("EXPORT_DATA\x00")
	coll := expOff + 2*0x28
	b := make([]byte, coll+0x40)
	copy(b, []byte("STCM2L TEST"))
	binary.LittleEndian.PutUint32(b[0x20:], uint32(expOff))
	binary.LittleEndian.PutUint32(b[0x24:], 2)
	binary.LittleEndian.PutUint32(b[0x2c:], uint32(coll))
	binary.LittleEndian.PutUint32(b[dataOff:], 7)
	binary.LittleEndian.PutUint32(b[dataOff+0xc:], 16)
	copy(b[dataOff+0x10:], bytes.Repeat([]byte{'A'}, 16))
	copy(b[codeMark:], []byte("CODE_START_\x00"))
	binary.LittleEndian.PutUint32(b[start+0xc:], 0x10)
	binary.LittleEndian.PutUint32(b[hidden+4:], 3)
	binary.LittleEndian.PutUint32(b[hidden+8:], 1)
	binary.LittleEndian.PutUint32(b[hidden+0xc:], 0x1c)
	binary.LittleEndian.PutUint32(b[hidden+0x10:], 0xffffff41)
	binary.LittleEndian.PutUint32(b[hidden+0x14:], uint32(target))
	binary.LittleEndian.PutUint32(b[hidden+0x18:], 0x40000000)
	binary.LittleEndian.PutUint32(b[target+0xc:], 0x10)
	copy(b[expMark:], []byte("EXPORT_DATA\x00"))
	binary.LittleEndian.PutUint32(b[expOff:], 1)
	copy(b[expOff+4:], []byte("DATA"))
	binary.LittleEndian.PutUint32(b[expOff+0x24:], uint32(dataOff))
	copy(b[expOff+0x28+4:], []byte("main"))
	binary.LittleEndian.PutUint32(b[expOff+0x28+0x24:], uint32(start))
	return b, dataOff, hidden, target
}
func TestSTCMGoRelayoutDeadInstructionPointers(t *testing.T) {
	b, dataOff, hidden, target := stcmDeadCodeFixture()
	ref, e := STCMPhysicalReferences(b)
	if e != nil {
		t.Fatal(e)
	}
	exists := false
	for _, r := range ref {
		if r.Word == hidden+0x14 && r.Target == target {
			exists = true
		}
	}
	if !exists {
		t.Fatal("dead instruction reference missed")
	}
	out, e := RebuildSTCMRelayout(b, map[int][]byte{dataOff: bytes.Repeat([]byte{'a'}, 24)})
	if e != nil {
		t.Fatal(e)
	}
	if len(out) != len(b)+8 {
		t.Fatalf("size %d expected %d", len(out), len(b)+8)
	}
	if got := int(binary.LittleEndian.Uint32(out[hidden+8+0x14:])); got != target+8 {
		t.Fatalf("dead pointer not repointed got %#x want %#x", got, target+8)
	}
	if !bytes.Equal(out[dataOff+0x10:dataOff+0x10+24], bytes.Repeat([]byte{'a'}, 24)) {
		t.Fatal("payload moved incorrectly")
	}
	newS, e := ParseSTCM(out)
	if e != nil || len(newS.Exports) != 2 {
		t.Fatalf("STCM reparse: %v", e)
	}
}
func TestSTCMGoGrowthBlocksIncompletePhysicalScan(t *testing.T) {
	b := synthSTCMString()
	_, e := RebuildSTCMRelayout(b, map[int][]byte{0x30: []byte("Growing replacement string")})
	if e == nil || !strings.Contains(e.Error(), "unsafe STCM growth") {
		t.Fatalf("unsafe growth accepted: %v", e)
	}
	same, e := RebuildSTCMRelayout(b, map[int][]byte{0x30: []byte("Hi\x00")})
	if e != nil {
		t.Fatal(e)
	}
	it, e := STCMDataAt(same, 0x30)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(it.Payload, []byte("Hi\x00")) {
		t.Fatal("shrunk item mismatch")
	}
}
