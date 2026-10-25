package mskit

import (
	"bytes"
	"os"
	"testing"
)

func TestWesternMS2ENCOneRealSlot(t *testing.T) {
	src, e := os.ReadFile(privateFixturePath(t, "switch-enc-ms2-original"))
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := ParseENCEntry(src)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := parsed.Rows[0].DecodedPayload()
	if e != nil {
		t.Fatal(e)
	}
	off := 2055996
	old := append([]byte(nil), raw[off:off+67]...)
	amended := bytes.Repeat([]byte("N"), 139)
	edited, e := RebuildENCEntry(src, []ENCEdit{{Row: 0, Offset: off, Old: old, New: amended}}, WrapBILZGo)
	if e != nil {
		t.Fatal(e)
	}
	check, e := ParseENCEntry(edited)
	if e != nil {
		t.Fatal(e)
	}
	newRaw, e := check.Rows[0].DecodedPayload()
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(newRaw[off:off+139], amended) || !bytes.Equal(raw[:off], newRaw[:off]) || !bytes.Equal(raw[off+140:], newRaw[off+140:]) {
		t.Fatal("other bytes drifted")
	}
	for i := 1; i < len(parsed.Rows); i++ {
		if !bytes.Equal(parsed.Rows[i].Payload, check.Rows[i].Payload) {
			t.Fatalf("unchanged row %d drift", i)
		}
	}
	t.Logf("real Western MS2 g1: original entry=%d, one-slot amended entry=%d; compressed changed row=%d (original %d)", len(src), len(edited), len(check.Rows[0].Payload), len(parsed.Rows[0].Payload))
}
