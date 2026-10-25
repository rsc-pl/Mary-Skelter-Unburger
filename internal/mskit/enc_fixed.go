package mskit

// Source-pinned ENC exact-footprint rewrite: preserves the original byte
// offsets of any opaque bytes following its BILZ zlib body. This is used only
// when the parent GDAT has a verified non-relocatable footer; ordinary ENC
// rebuild remains unchanged. It never truncates text or compressed content.
import (
	"bytes"
	"encoding/binary"
	"fmt"
)

func RebuildENCEntryFixedWidthGo(original []byte, edits []ENCEdit) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), original...), nil
	}
	if len(original) < OrochiBILZDataOffset || OrochiWrapperTag(original) != "enc" ||
		string(original[OrochiBILZOffset:OrochiBILZOffset+4]) != "BILZ" ||
		binary.BigEndian.Uint32(original[0x8c:0x90]) != BILZAlgoZlib {
		return nil, fmt.Errorf("fixed-width ENC requires zlib Orochi BILZ")
	}
	oldCSize := int(binary.BigEndian.Uint32(original[0x88:0x8c]))
	oldEnd := OrochiBILZDataOffset + oldCSize
	if oldCSize < 6 || oldEnd > len(original) {
		return nil, fmt.Errorf("invalid original ENC compressed length")
	}
	// The regular writer independently verifies all edited decoded rows and
	// preserves unmodified logical rows. Use Go's native matching LZO1X.
	changed, err := RebuildENCEntry(original, edits, WrapBILZGo)
	if err != nil {
		return nil, err
	}
	if len(changed) < OrochiBILZDataOffset {
		return nil, fmt.Errorf("short rewritten ENC")
	}
	newCSize := int(binary.BigEndian.Uint32(changed[0x88:0x8c]))
	newEnd := OrochiBILZDataOffset + newCSize
	if newCSize < 6 || newEnd > len(changed) {
		return nil, fmt.Errorf("invalid rewritten ENC compressed length")
	}
	if !bytes.Equal(changed[newEnd:], original[oldEnd:]) ||
		!bytes.Equal(changed[:0x84], original[:0x84]) ||
		!bytes.Equal(changed[0x8c:0x90], original[0x8c:0x90]) {
		return nil, fmt.Errorf("ENC wrapper prefix or opaque suffix changed")
	}
	padded, err := PadDenseZlibWithoutChangingText(changed[OrochiBILZDataOffset:newEnd], oldCSize)
	if err != nil {
		return nil, fmt.Errorf("ENC fixed-width compressed payload cannot fit: %w", err)
	}
	out := append([]byte(nil), original...)
	// The raw ENC row table may change size, but the outer BILZ compressed
	// payload must remain at the original length and keep its original suffix.
	copy(out[0x84:0x88], changed[0x84:0x88])
	copy(out[OrochiBILZDataOffset:oldEnd], padded)
	if len(out) != len(original) || !bytes.Equal(out[oldEnd:], original[oldEnd:]) {
		return nil, fmt.Errorf("ENC fixed-footprint or opaque suffix changed")
	}
	// All logical rows, including those deliberately left unchanged, must
	// match the ordinary writer's independently verified expected result.
	want, err := ParseENCEntry(changed)
	if err != nil {
		return nil, err
	}
	got, err := ParseENCEntry(out)
	if err != nil {
		return nil, err
	}
	if want.Count != got.Count {
		return nil, fmt.Errorf("ENC fixed row count changed")
	}
	for i := range want.Rows {
		if !bytes.Equal(want.Rows[i].Payload, got.Rows[i].Payload) {
			return nil, fmt.Errorf("ENC row %d differs from verified writer", i)
		}
	}
	return out, nil
}
