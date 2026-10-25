package mskit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The only proprietary bytes used here are the precise 1249-byte unindexed
// suffix and GDAT boundaries supplied by the user. The indexed payload is a
// synthetic stand-in; this does NOT claim full-game rebuild parity.
func TestWesternMS2073OpaqueTailPinnedInPlace(t *testing.T) {
	root := privateFixturePath(t, "switch-gdat-opaque-samples")
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	tail := read("tail.bin")
	if len(tail) != 1249 || digestHex(tail) != "da02829e7ddc75266211a2579f52936ed3a1ead8c78635a6b63ee5f3948c06a0" {
		t.Fatal("user-supplied GDAT 073 trailer evidence changed")
	}
	entries := [][]byte{make([]byte, 26457), make([]byte, 331744), make([]byte, 334961)}
	// Preserve the actual last 256 indexed bytes in the synthetic fixture.
	copy(entries[2][len(entries[2])-256:], read("preceding-entry-last-256.bin"))
	base, err := BuildGDAT(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(base) != 693194 || !bytes.Equal(base[:8], read("gdat-header-8.bin")) || !bytes.Equal(base[8:32], read("last-table-records.bin")) {
		t.Fatal("source GDAT073 metadata and synthetic index layout do not match")
	}
	src := append(base, tail...)
	witness := GDATOpaqueTailEvidence{SourceSHA256: digestHex(src), TailSHA256: digestHex(tail), EntryCount: 3, IndexedEnd: 693194, TailLength: 1249}
	if _, err := ParseSwitchGDAT(src); err == nil {
		t.Fatal("unproven nonzero trailer accepted")
	}
	if _, err := ParseSwitchGDATWithOpaqueTailEvidence(src, GDATOpaqueTailEvidence{SourceSHA256: "wrong", TailSHA256: digestHex(tail), EntryCount: 3, IndexedEnd: 693194, TailLength: 1249}); err == nil {
		t.Fatal("wrong source fingerprint accepted")
	}
	g, err := ParseSwitchGDATWithOpaqueTailEvidence(src, witness)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(g.Raw, src) || len(g.OpaqueTail) != 1249 {
		t.Fatal("source or footer changed during read")
	}

	mutated := append([]byte(nil), g.Entries[1]...)
	mutated[43] ^= 0x29
	changes := [][]byte{g.Entries[0], mutated, g.Entries[2]}
	built, err := BuildSwitchGDATFromSource(g, changes)
	if err != nil {
		t.Fatal(err)
	}
	if len(built) != len(src) || !bytes.Equal(built[:32], src[:32]) || !bytes.Equal(built[693194:], src[693194:]) {
		t.Fatal("pinned trailer or table moved")
	}
	if bytes.Equal(built, src) {
		t.Fatal("actual edit was not applied")
	}
	check, err := VerifySwitchGDATRebuild(g, built)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(check.Entries[1], mutated) || !bytes.Equal(check.Entries[2], g.Entries[2]) {
		t.Fatal("rebuild altered unmodified entries")
	}
	// A growing entry must never shift this unknown suffix, even if the
	// entire new GDAT could otherwise be parsed.
	grown := append(append([]byte(nil), mutated...), byte(0x42))
	if _, err := BuildSwitchGDATFromSource(g, [][]byte{g.Entries[0], grown, g.Entries[2]}); err == nil || !strings.Contains(err.Error(), "relocation forbidden") {
		t.Fatalf("growing entry did not fail closed: %v", err)
	}
	shorter := mutated[:len(mutated)-1]
	if _, err := BuildSwitchGDATFromSource(g, [][]byte{g.Entries[0], shorter, g.Entries[2]}); err == nil {
		t.Fatal("shrinking entry accepted")
	}
	corruptedTail := append([]byte(nil), built...)
	corruptedTail[len(corruptedTail)-1] ^= 0x40
	if _, err := VerifySwitchGDATRebuild(g, corruptedTail); err == nil {
		t.Fatal("modified trailer accepted")
	}
	corruptedTable := append([]byte(nil), built...)
	corruptedTable[16] ^= 0x10
	if _, err := VerifySwitchGDATRebuild(g, corruptedTable); err == nil {
		t.Fatal("modified entry index accepted")
	}
	badSource := append([]byte(nil), src...)
	badSource[50] ^= 0x10
	if _, err := ParseSwitchGDATWithOpaqueTailEvidence(badSource, witness); err == nil {
		t.Fatal("modified source accepted")
	}
	if _, err := BuildSwitchGDAT(entries, tail); err == nil {
		t.Fatal("generic builder accepted opaque bytes")
	}
	t.Log("GDAT073 1249-byte opaque trailer: pinned evidence, unchanged indexed layout, fixed-position preservation and failure cases PASS")
}
