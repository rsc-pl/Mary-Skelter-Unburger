package mskit

import (
	"bytes"
	"strings"
	"testing"
)

// A Western archive with extra zero bytes must not lose that source padding
// during a rebuild. An opaque/nonzero footer cannot be relocated without proof.
func TestGDATSourceZeroPadding(t *testing.T) {
	records := [][]byte{[]byte("first"), []byte("second record")}
	src, err := BuildSwitchGDAT(records, make([]byte, 48))
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSwitchGDAT(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ZeroTail) != 48 || !bytes.Equal(got.ZeroTail, make([]byte, 48)) {
		t.Fatal("source padding lost")
	}
	same, err := BuildSwitchGDAT(got.Entries, got.ZeroTail)
	if err != nil || !bytes.Equal(src, same) {
		t.Fatal("unmodified source not byte exact", err)
	}
	got.Entries[0] = []byte("first now expanded")
	rebuilt, err := BuildSwitchGDAT(got.Entries, got.ZeroTail)
	if err != nil {
		t.Fatal(err)
	}
	check, err := ParseSwitchGDAT(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if string(check.Entries[0]) != "first now expanded" || string(check.Entries[1]) != "second record" || !bytes.Equal(check.ZeroTail, got.ZeroTail) {
		t.Fatal("record or trailer changed")
	}
	bad := append(append([]byte(nil), src...), byte(0xa5))
	if _, err := ParseSwitchGDAT(bad); err == nil || !strings.Contains(err.Error(), "unknown footer") {
		t.Fatalf("nonzero GDAT footer accepted: %v", err)
	}
	if _, err := BuildSwitchGDAT(records, []byte{0, 0x7f}); err == nil {
		t.Fatal("nonzero footer built")
	}
	if _, err := BuildSwitchGDAT(records, make([]byte, MaxSwitchGDATZeroTail+1)); err == nil {
		t.Fatal("unbounded tail built")
	}
}
