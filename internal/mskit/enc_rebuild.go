package mskit

// Native, fail-closed ENC row rewrite. This is deliberately distinct from the
// research-only Switch port: the entry writer is reusable by a later verified
// Go release pipeline, but it is not wired into the public TUI until real
// Western Switch source/roundtrip/runtime testing closes the remaining gates.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

// ENCEdit addresses a NUL-terminated string by its decoded row offset. Old and
// New are raw UTF-8 byte strings; Capacity is the verified explicit slot limit,
// or 0 if no independent capacity is known. When 0, len(Old) is the initial
// bound, then immediate NUL slack can be used without moving any later bytes.
type ENCEdit struct {
	Row       int
	Offset    int
	Old       []byte
	New       []byte
	Capacity  int
	RecordKey string
}

func editENCDecodedRow(decoded []byte, edits []ENCEdit) ([]byte, error) {
	sorted := append([]ENCEdit(nil), edits...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Offset > sorted[j].Offset })
	out := append([]byte(nil), decoded...)
	previousOffset := len(out)
	for _, e := range sorted {
		label := e.RecordKey
		if label == "" {
			label = fmt.Sprintf("row=%d offset=%d", e.Row, e.Offset)
		}
		capacity := e.Capacity
		if capacity == 0 {
			capacity = len(e.Old)
		}
		if e.Offset < 0 || len(e.Old) == 0 || capacity < len(e.Old) ||
			e.Offset > len(out) || capacity >= len(out)-e.Offset {
			return nil, fmt.Errorf("%s: invalid ENC slot metadata", label)
		}
		end := e.Offset + capacity + 1
		if end > previousOffset {
			return nil, fmt.Errorf("%s: overlapping ENC edits", label)
		}
		previousOffset = e.Offset
		if bytes.IndexByte(e.New, 0) >= 0 {
			return nil, fmt.Errorf("%s: NUL in replacement", label)
		}
		if !bytes.Equal(out[e.Offset:e.Offset+len(e.Old)], e.Old) || out[e.Offset+len(e.Old)] != 0 {
			return nil, fmt.Errorf("%s: ENC source string byte/termination mismatch", label)
		}
		for _, b := range out[e.Offset+len(e.Old) : end] {
			if b != 0 {
				return nil, fmt.Errorf("%s: ENC slot crosses nonzero data", label)
			}
		}
		if len(e.New) > capacity {
			// If independently proven capacity is absent, the initial len(Old)
			// is NOT a format limit. Extend into *existing* NUL padding only,
			// leaving a terminator plus one spare NUL before later data.
			newEnd := e.Offset + len(e.New)
			inPlace := e.Capacity == 0 && newEnd+1 < len(out)
			if inPlace {
				for _, b := range out[e.Offset+len(e.Old) : newEnd+2] {
					if b != 0 {
						inPlace = false
						break
					}
				}
			}
			if inPlace {
				copy(out[e.Offset:newEnd], e.New)
				out[newEnd] = 0
			} else {
				// terminal-only relocation is safe if the remaining
				// row is all NUL. It never shifts nonzero/pointer data.
				for _, b := range out[end:] {
					if b != 0 {
						return nil, fmt.Errorf("%s: ENC text needs %d bytes but capacity holds %d; interior relocation unproven", label, len(e.New), capacity)
					}
				}
				grown := make([]byte, 0, len(out)+len(e.New)-capacity)
				grown = append(grown, out[:e.Offset]...)
				grown = append(grown, e.New...)
				grown = append(grown, 0)
				grown = append(grown, out[end:]...)
				out = grown
			}
		} else {
			copy(out[e.Offset:], e.New)
			clear(out[e.Offset+len(e.New) : end])
		}
	}
	return out, nil
}

// CompressLZO1XLiterals emits a standards-compatible LZO1X literal-only stream.
// An unchanged compressed row is copied through byte-for-byte; modified rows
// use this valid but not size-optimal encoding. The outer BILZ layer may still
// compress the output. Never claim byte-identical liblzo compression.
func CompressLZO1XLiterals(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("LZO literal-only row too short: %d", len(data))
	}
	out := make([]byte, 0, len(data)+len(data)/255+8)
	if len(data) <= 238 {
		out = append(out, byte(len(data)+17))
	} else {
		// In the decoder: token 0 -> extendLength(15) -> copy (t+3)
		// bytes. Thus the extension encodes dataLen - 18.
		n := len(data) - 18
		out = append(out, 0)
		for n > 255 {
			out = append(out, 0)
			n -= 255
		}
		if n < 1 || n > 255 {
			return nil, fmt.Errorf("LZO length encoding failed")
		}
		out = append(out, byte(n))
	}
	out = append(out, data...)
	out = append(out, 0x11, 0, 0)
	decoded, err := DecompressLZO1X(out, len(data))
	if err != nil || !bytes.Equal(decoded, data) {
		return nil, fmt.Errorf("LZO roundtrip failed: %v", err)
	}
	return out, nil
}

// RebuildENCEntry edits decoded ENC strings without touching original source or
// any unmodified logical rows. For outer zlib it requires a caller-supplied
// canonical BILZ encoder (same contract as PatchGraphicsTID); supplying nil
// fails rather than silently substituting a different compression lineage.
// It also re-parses and checks every unchanged decoded row byte-for-byte.
func RebuildENCEntry(entry []byte, edits []ENCEdit, encode CanonicalBILZEncoder) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), entry...), nil
	}
	original, err := ParseENCEntry(entry)
	if err != nil {
		return nil, err
	}
	grouped := make(map[int][]ENCEdit)
	for _, e := range edits {
		if e.Row < 0 || e.Row >= original.Count {
			return nil, fmt.Errorf("ENC edit row %d outside entry", e.Row)
		}
		grouped[e.Row] = append(grouped[e.Row], e)
	}
	logicalRows := make([][]byte, original.Count)
	compressedRows := make([][]byte, original.Count)
	sizes := make([]uint32, original.Count)
	aux := make([]uint32, original.Count)
	for i, row := range original.Rows {
		aux[i] = row.Aux
		sizes[i] = row.SizeField
		compressedRows[i] = append([]byte(nil), row.Payload...)
		if len(grouped[i]) == 0 {
			continue
		}
		decoded, err := row.DecodedPayload()
		if err != nil {
			return nil, fmt.Errorf("ENC row %d decompression: %w", i, err)
		}
		updated, err := editENCDecodedRow(decoded, grouped[i])
		if err != nil {
			return nil, err
		}
		logicalRows[i] = updated
		if row.LZOCompressed() {
			compressedRows[i], err = CompressLZO1XMatches(updated)
			if err != nil {
				return nil, fmt.Errorf("ENC row %d recompression: %w", i, err)
			}
			if len(updated) > math.MaxUint32 || len(compressedRows[i]) > math.MaxInt32 {
				return nil, fmt.Errorf("ENC row %d size overflow", i)
			}
			sizes[i] = 0x80000000 | uint32(len(compressedRows[i]))
			aux[i] = uint32(len(updated))
		} else {
			if len(updated) > math.MaxInt32 {
				return nil, fmt.Errorf("ENC row %d size overflow", i)
			}
			compressedRows[i] = updated
			sizes[i] = uint32(len(updated))
		}
	}
	payload := make([]byte, 4+original.Count*ENCRowRecordSize)
	binary.LittleEndian.PutUint32(payload[:4], uint32(original.Count))
	for i, encoded := range compressedRows {
		for len(payload)%0x40 != 0 {
			payload = append(payload, 0)
		}
		if len(payload) > math.MaxUint32 || len(encoded) > math.MaxUint32-len(payload) {
			return nil, fmt.Errorf("ENC payload overflow")
		}
		p := 4 + i*ENCRowRecordSize
		binary.LittleEndian.PutUint32(payload[p:p+4], sizes[i])
		binary.LittleEndian.PutUint32(payload[p+4:p+8], aux[i])
		binary.LittleEndian.PutUint32(payload[p+8:p+12], uint32(len(payload)))
		payload = append(payload, encoded...)
	}
	if len(entry) < 0x90 || binary.BigEndian.Uint32(entry[0x8c:0x90]) != 7 {
		return nil, fmt.Errorf("unsupported outer ENC BILZ algorithm")
	}
	if encode == nil {
		return nil, fmt.Errorf("ENC canonical zlib encoder required")
	}
	wrapped, err := encode(payload, 7, 7)
	if err != nil {
		return nil, err
	}
	rebuilt := append(append([]byte(nil), entry[:0x80]...), wrapped...)
	csize := int(binary.BigEndian.Uint32(entry[0x88:0x8c]))
	if 0x90+csize > len(entry) {
		return nil, fmt.Errorf("invalid original ENC compressed size")
	}
	rebuilt = append(rebuilt, entry[0x90+csize:]...)
	check, err := ParseENCEntry(rebuilt)
	if err != nil {
		return nil, fmt.Errorf("ENC rebuild parse failure: %w", err)
	}
	if check.Count != original.Count {
		return nil, fmt.Errorf("ENC row count changed")
	}
	for i, row := range original.Rows {
		old, err := row.DecodedPayload()
		if err != nil {
			return nil, err
		}
		got, err := check.Rows[i].DecodedPayload()
		if err != nil {
			return nil, err
		}
		want := old
		if len(grouped[i]) != 0 {
			want = logicalRows[i]
		} else if !bytes.Equal(check.Rows[i].Payload, row.Payload) {
			return nil, fmt.Errorf("ENC unmodified compressed row %d changed", i)
		}
		if !bytes.Equal(got, want) {
			return nil, fmt.Errorf("ENC decoded row %d verification failure", i)
		}
	}
	return rebuilt, nil
}
