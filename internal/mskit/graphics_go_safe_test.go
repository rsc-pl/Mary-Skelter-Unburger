package mskit

import (
	"bytes"
	"strings"
	"testing"
)

func TestGoSafeGraphicsZlibAndOpaqueTail(t *testing.T) {
	tid := tinyTID(t, 0x90, 0x55)
	target := tinyTID(t, 0x90, 0xAA)
	cl := graphicsTestCL3(t, tid, "test_tid.tid")
	compressed, err := WrapBILZGo(cl, BILZAlgoZlib, 9)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("CUSTOM-OPAQUE-PREFIX")
	source := graphicsTestGDAT(t, append(append([]byte{}, prefix...), compressed...), 1)
	result, loc, err := PatchGraphicsTIDGoSafe(source, 1, "TEST_TID.TID", target)
	if err != nil {
		t.Fatal(err)
	}
	if !loc.BILZ || loc.BILZAlgo != BILZAlgoZlib || loc.PrefixLen != len(prefix) {
		t.Fatalf("location drift: %+v", loc)
	}
	got, _, err := ExtractGraphicsTID(result, 1, "test_tid.tid")
	if err != nil || !bytes.Equal(got, target) {
		t.Fatalf("target drift %v", err)
	}
	srcGDAT, _ := ParseGDAT(source)
	corrupt := append([]byte{}, srcGDAT.Entries[1]...)
	corrupt = append(corrupt, []byte("TRAILER-WITH-UNKNOWN-OFFSETS")...)
	srcGDAT.Entries[1] = corrupt
	unsafe, err := BuildGDAT(srcGDAT.Entries)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PatchGraphicsTIDGoSafe(unsafe, 1, "test_tid.tid", target); err == nil || !strings.Contains(err.Error(), "unknown trailing bytes") {
		t.Fatalf("expected unknown BILZ trailer rejection, got %v", err)
	}
	if _, _, err := PatchGraphicsTIDGoSafe(source, 1, "test_tid.tid", []byte("wrong")); err == nil {
		t.Fatal("wrong TID metadata accepted")
	}
}
