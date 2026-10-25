package maintainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func tinySourceTextureFixture() []byte {
	tid := make([]byte, 144)
	copy(tid[:4], []byte{'T', 'I', 'D', 0x90})
	binary.LittleEndian.PutUint32(tid[0x44:], 4)
	binary.LittleEndian.PutUint32(tid[0x48:], 4)
	copy(tid[0x64:], "DXT5")
	for i := 128; i < len(tid); i++ {
		tid[i] = byte(i - 128 + 4)
	}
	return tid
}

func cl3SourceTextureFixture(t *testing.T, tid []byte, ancillary []byte) []byte {
	t.Helper()
	const collection = 0xD0
	parts := [][]byte{ancillary, tid}
	names := []string{"other.dat", "label.tid"}
	dataSize := mskit.CL3EntrySize * len(parts)
	for _, p := range parts {
		dataSize += len(p)
	}
	out := make([]byte, collection+dataSize+7)
	copy(out[0x10:], "CL3L")
	binary.LittleEndian.PutUint32(out[0x18:], 3)
	binary.LittleEndian.PutUint32(out[0x1c:], 2)
	binary.LittleEndian.PutUint32(out[0x20:], 0x30)
	copy(out[0x30:], "FILE_COLLECTION")
	binary.LittleEndian.PutUint32(out[0x50:], 2)
	binary.LittleEndian.PutUint32(out[0x54:], uint32(dataSize))
	binary.LittleEndian.PutUint32(out[0x58:], collection)
	copy(out[0x80:], "FILE_LINK")
	binary.LittleEndian.PutUint32(out[0xA8:], uint32(collection+dataSize))
	off := len(parts) * mskit.CL3EntrySize
	for i, p := range parts {
		rec := collection + i*mskit.CL3EntrySize
		copy(out[rec:], names[i])
		binary.LittleEndian.PutUint32(out[rec+0x204:], uint32(off))
		binary.LittleEndian.PutUint32(out[rec+0x208:], uint32(len(p)))
		copy(out[collection+off:], p)
		off += len(p)
	}
	copy(out[collection+dataSize:], "CL3TAIL")
	return out
}

func buildStagedTextFixture(t *testing.T, tid []byte, text string) []byte {
	t.Helper()
	dat, err := mskit.BuildGDAT([][]byte{[]byte(text), cl3SourceTextureFixture(t, tid, []byte("OTHER-IN-CL3")), []byte("UNCHANGED")})
	if err != nil {
		t.Fatal(err)
	}
	return dat
}

func TestStagedPreservesExistingTextAndOriginalTID(t *testing.T) {
	tid := tinySourceTextureFixture()
	sum := sha256.Sum256(tid)
	targets := map[int]map[string]string{1: {"label.tid": hex.EncodeToString(sum[:])}}
	pristine := buildStagedTextFixture(t, tid, "OLD-TEXT")
	staged := buildStagedTextFixture(t, tid, "LONGER-TEXT-AFTER-NATIVE-WRITER")
	if bytes.Equal(pristine, staged) {
		t.Fatal("fixture not changed")
	}
	if err := checkTextStagedGraphicsBase(pristine, staged, targets); err != nil {
		t.Fatal(err)
	}
	identical := buildStagedTextFixture(t, tid, "OLD-TEXT")
	if err := checkTextStagedGraphicsBase(pristine, identical, targets); err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte{}, tid...)
	corrupted[len(corrupted)-1] ^= 0x33
	if err := checkTextStagedGraphicsBase(pristine, buildStagedTextFixture(t, corrupted, "NEW"), targets); err == nil || !strings.Contains(err.Error(), "differs from pristine") {
		t.Fatalf("corrupt TID accepted: %v", err)
	}
	if err := checkTextStagedGraphicsBase(pristine, staged, map[int]map[string]string{1: {"label.tid": strings.Repeat("0", 64)}}); err == nil {
		t.Fatal("wrong SHA accepted")
	}
	fewer, err := mskit.BuildGDAT([][]byte{[]byte("TEXT"), cl3SourceTextureFixture(t, tid, []byte("OTHER"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTextStagedGraphicsBase(pristine, fewer, targets); err == nil || !strings.Contains(err.Error(), "entry count") {
		t.Fatalf("short GDAT accepted: %v", err)
	}
	if err := checkTextStagedGraphicsBase(pristine, []byte("INVALID"), targets); err == nil {
		t.Fatal("corrupt GDAT accepted")
	}
	if err := checkTextStagedGraphicsBase(pristine, staged, map[int]map[string]string{}); err == nil {
		t.Fatal("empty target set accepted")
	}
	if err := checkTextStagedGraphicsBase(pristine, staged, map[int]map[string]string{0: {"label.tid": hex.EncodeToString(sum[:])}}); err == nil {
		t.Fatal("wrong member index accepted")
	}
}

func TestStagedDoesNotClaimTranslatedTextParity(t *testing.T) {
	// Preflight deliberately checks GRAPHICS identity, not semantic correctness
	// of canonical text. Here a changed unrelated GDAT entry (even nonsense) is allowed:
	// independent legacy decoded-text verification must certify it separately.
	tid := tinySourceTextureFixture()
	hash := sha256.Sum256(tid)
	old := buildStagedTextFixture(t, tid, "ORIGINAL")
	other := buildStagedTextFixture(t, tid, "NOT PROVEN TO BE GOOD ENGLISH")
	if err := checkTextStagedGraphicsBase(old, other, map[int]map[string]string{1: {"label.tid": hex.EncodeToString(hash[:])}}); err != nil {
		t.Fatal(err)
	}
}
