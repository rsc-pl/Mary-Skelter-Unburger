package mskit

import (
	"encoding/binary"
	"fmt"
)

const (
	GDATHeaderSize = 8
	GDATEntrySize  = 8
)

type GDAT struct {
	Raw     []byte
	Entries [][]byte
}

func ParseGDAT(raw []byte) (GDAT, error) {
	if len(raw) < GDATHeaderSize || string(raw[:4]) != "GDAT" {
		return GDAT{}, fmt.Errorf("not a GDAT archive")
	}
	n := int(binary.LittleEndian.Uint32(raw[4:8]))
	if n < 0 || n > 5_000_000 {
		return GDAT{}, fmt.Errorf("implausible GDAT entry count %d", n)
	}
	tableEnd := GDATHeaderSize + GDATEntrySize*n
	if tableEnd > len(raw) {
		return GDAT{}, fmt.Errorf("truncated GDAT table")
	}
	entries := make([][]byte, n)
	prevEnd := tableEnd
	for i := 0; i < n; i++ {
		p := GDATHeaderSize + i*GDATEntrySize
		off := int(binary.LittleEndian.Uint32(raw[p : p+4]))
		size := int(binary.LittleEndian.Uint32(raw[p+4 : p+8]))
		if off < tableEnd || off > len(raw) || size < 0 || size > len(raw)-off {
			return GDAT{}, fmt.Errorf("GDAT entry %d outside archive", i)
		}
		if i == 0 && off != tableEnd {
			return GDAT{}, fmt.Errorf("GDAT first entry starts at %d, expected %d", off, tableEnd)
		}
		if i > 0 && off != prevEnd {
			return GDAT{}, fmt.Errorf("GDAT entry %d is not contiguous", i)
		}
		entries[i] = raw[off : off+size]
		prevEnd = off + size
	}
	if n == 0 {
		if tableEnd != len(raw) {
			return GDAT{}, fmt.Errorf("zero-entry GDAT has trailing bytes")
		}
	} else if prevEnd != len(raw) {
		return GDAT{}, fmt.Errorf("GDAT has trailing bytes")
	}
	return GDAT{Raw: raw, Entries: entries}, nil
}

func BuildGDAT(entries [][]byte) ([]byte, error) {
	header := GDATHeaderSize + GDATEntrySize*len(entries)
	total := int64(header)
	for _, e := range entries {
		total += int64(len(e))
		if total > int64(^uint32(0)) {
			return nil, fmt.Errorf("GDAT exceeds 32-bit offset range")
		}
	}
	out := make([]byte, int(total))
	copy(out[:4], []byte("GDAT"))
	binary.LittleEndian.PutUint32(out[4:8], uint32(len(entries)))
	off := header
	for i, e := range entries {
		p := GDATHeaderSize + i*GDATEntrySize
		binary.LittleEndian.PutUint32(out[p:p+4], uint32(off))
		binary.LittleEndian.PutUint32(out[p+4:p+8], uint32(len(e)))
		copy(out[off:], e)
		off += len(e)
	}
	return out, nil
}
