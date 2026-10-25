package mskit

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type BINFile struct {
	Payload   []byte
	BlockHint uint32
	Offsets   []int
	TableEnd  int
	PoolStart int
}

func DecodeOrochiEntry(entry []byte, expectedTag string) ([]byte, error) {
	if len(entry) < OrochiBILZDataOffset {
		return nil, fmt.Errorf("Orochi entry shorter than wrapper header")
	}
	if tag := OrochiWrapperTag(entry); tag != expectedTag {
		return nil, fmt.Errorf("wrapper tag is %q, not %q", tag, expectedTag)
	}
	if string(entry[OrochiBILZOffset:OrochiBILZOffset+4]) != "BILZ" {
		return nil, fmt.Errorf("no BILZ block at 0x80")
	}
	usize := int(binary.BigEndian.Uint32(entry[0x84:0x88]))
	csize := int(binary.BigEndian.Uint32(entry[0x88:0x8c]))
	algo := int(binary.BigEndian.Uint32(entry[0x8c:0x90]))
	if csize < 0 || OrochiBILZDataOffset+csize > len(entry) {
		return nil, fmt.Errorf("BILZ body exceeds Orochi entry")
	}
	payload, err := InflateBILZ(entry[OrochiBILZDataOffset:OrochiBILZDataOffset+csize], algo)
	if err != nil {
		return nil, err
	}
	if len(payload) != usize {
		return nil, fmt.Errorf("BILZ decoded size %d, expected %d", len(payload), usize)
	}
	return payload, nil
}

func ParseBINEntry(entry []byte) (BINFile, error) {
	payload, err := DecodeOrochiEntry(entry, "bin")
	if err != nil {
		return BINFile{}, err
	}
	return ParseBINPayload(payload)
}

func ParseBINPayload(payload []byte) (BINFile, error) {
	if len(payload) < 0x20 {
		return BINFile{}, fmt.Errorf("BIN payload too short")
	}
	b := BINFile{Payload: payload, BlockHint: binary.LittleEndian.Uint32(payload[:4])}
	pos := 0x10
	for pos+16 <= len(payload) {
		off := int(binary.LittleEndian.Uint64(payload[pos : pos+8]))
		if off == 0 || off >= len(payload) || (len(b.Offsets) > 0 && off < b.Offsets[len(b.Offsets)-1]) {
			break
		}
		b.Offsets = append(b.Offsets, off)
		pos += 16
	}
	b.TableEnd = pos
	b.PoolStart = pos
	if len(b.Offsets) > 0 {
		b.PoolStart = b.Offsets[0]
	}
	if b.PoolStart < pos {
		return BINFile{}, fmt.Errorf("BIN string pool overlaps record table")
	}
	return b, nil
}

func (b BINFile) String(index int) ([]byte, error) {
	if index < 0 || index >= len(b.Offsets) {
		return nil, fmt.Errorf("BIN string %d out of range", index)
	}
	off := b.Offsets[index]
	end := bytes.IndexByte(b.Payload[off:], 0)
	if end < 0 {
		end = len(b.Payload) - off
	}
	return append([]byte(nil), b.Payload[off:off+end]...), nil
}

func (b BINFile) Strings() ([][]byte, error) {
	out := make([][]byte, 0, len(b.Offsets))
	for i := range b.Offsets {
		s, err := b.String(i)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
