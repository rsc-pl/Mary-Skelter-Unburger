package maintainer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

// A multi-target family must protect every member, not just the first TID.
// This is independent synthetic evidence; original source-based tests are private.
func TestGroupedSourceGuardEveryMember(t *testing.T) {
	original := make([][]byte, 1550)
	staged := make([][]byte, 1550)
	targets := map[int]map[string]string{}
	for i := range original {
		original[i] = []byte("other-record")
		staged[i] = []byte("other-record")
	}
	for i := 0; i < 6; i++ {
		tid := tinySourceTextureFixture()
		tid[len(tid)-1] = byte(i + 17)
		cl := cl3SourceTextureFixture(t, tid, []byte("other"))
		index := 1541 + i
		original[index] = cl
		staged[index] = append([]byte{}, cl...)
		sum := sha256.Sum256(tid)
		targets[index] = map[string]string{"label.tid": hex.EncodeToString(sum[:])}
	}
	original[0] = []byte("untranslated text")
	staged[0] = []byte("staged canonical text")
	src, e := mskit.BuildGDAT(original)
	if e != nil {
		t.Fatal(e)
	}
	dst, e := mskit.BuildGDAT(staged)
	if e != nil {
		t.Fatal(e)
	}
	if e = checkTextStagedGraphicsBase(src, dst, targets); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 6; i++ {
		damaged := make([][]byte, len(staged))
		copy(damaged, staged)
		badtid := tinySourceTextureFixture()
		badtid[len(badtid)-1] = byte(i+17) ^ 0xff
		damaged[1541+i] = cl3SourceTextureFixture(t, badtid, []byte("other"))
		bad, e := mskit.BuildGDAT(damaged)
		if e != nil {
			t.Fatal(e)
		}
		if e = checkTextStagedGraphicsBase(src, bad, targets); e == nil || !strings.Contains(e.Error(), "differs from pristine") {
			t.Fatalf("wrong TID in member %d accepted: %v", i, e)
		}
	}
	if len(targets) != 6 {
		t.Fatal(fmt.Sprintf("invalid synthetic target count %d", len(targets)))
	}
}
