package mskit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

func TestENCWesternInPlaceGrowth(t *testing.T) {
	samples := []struct {
		game                string
		row, off, old, newN int
		entryHash           string
	}{
		{"ms2", 0, 2055996, 67, 139, "6968b0d7582163e9c53a246f13c90929eabd73f12d50463fa79d114f1beae7bd"},
		{"finale", 11, 1588, 6, 10, "3508ba868aa33326bde4e8cb55cf9ffbefc20db4cf54318e622b71bc5b4cf03b"},
	}
	for _, s := range samples {
		t.Run(s.game, func(t *testing.T) {
			filename := privateFixturePath(t, "switch-enc-"+s.game+"-original")
			originalBytes, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256(originalBytes)
			if hex.EncodeToString(h[:]) != s.entryHash {
				t.Fatal("original Western ENC evidence SHA drift")
			}
			parsed, err := ParseENCEntry(originalBytes)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := parsed.Rows[s.row].DecodedPayload()
			if err != nil {
				t.Fatal(err)
			}
			old := append([]byte(nil), decoded[s.off:s.off+s.old]...)
			newText := bytes.Repeat([]byte("N"), s.newN)
			patched, err := RebuildENCEntry(originalBytes, []ENCEdit{{Row: s.row, Offset: s.off, Old: old, New: newText, Capacity: 0, RecordKey: s.game + "/interior"}}, canonicalTestENC)
			if err != nil {
				t.Fatal(err)
			}
			rebuilt, err := ParseENCEntry(patched)
			if err != nil {
				t.Fatal(err)
			}
			after, err := rebuilt.Rows[s.row].DecodedPayload()
			if err != nil {
				t.Fatal(err)
			}
			if len(decoded) != len(after) || !bytes.Equal(after[:s.off], decoded[:s.off]) || !bytes.Equal(after[s.off:s.off+s.newN], newText) || after[s.off+s.newN] != 0 || !bytes.Equal(after[s.off+s.newN+1:], decoded[s.off+s.newN+1:]) {
				t.Fatal("in-place edit altered structure or bytes outside target")
			}
			if len(rebuilt.Rows) != len(parsed.Rows) {
				t.Fatal("row count drift")
			}
			for i := range parsed.Rows {
				if i != s.row && !bytes.Equal(parsed.Rows[i].Payload, rebuilt.Rows[i].Payload) {
					t.Fatalf("untouched compressed row %d changed", i)
				}
			}
			// Exact measured padding is exceeded: never clobber the following tagged data.
			tooLong := len(decoded) - s.off // strictly longer than all row contents
			if s.game == "ms2" {
				tooLong = 264 + 12
			} else {
				tooLong = 64 + 12
			}
			_, err = RebuildENCEntry(originalBytes, []ENCEdit{{Row: s.row, Offset: s.off, Old: old, New: bytes.Repeat([]byte("Z"), tooLong), Capacity: 0}}, canonicalTestENC)
			if err == nil || !strings.Contains(err.Error(), "interior relocation unproven") {
				t.Fatalf("unsafe growth accepted: %v", err)
			}
			_, err = RebuildENCEntry(originalBytes, []ENCEdit{{Row: s.row, Offset: s.off, Old: old, New: newText, Capacity: len(old)}}, canonicalTestENC)
			if err == nil || !strings.Contains(err.Error(), "interior relocation unproven") {
				t.Fatalf("explicit capacity bypassed: %v", err)
			}
			t.Logf("original Western ENC in-place %d->%d bytes, decoded and compressed non-target rows preserved", s.old, s.newN)
		})
	}
}
