package mskit

// Pure-Go, exact-width Orochi BIN rewriting for source-pinned Switch entries.
// Different DEFLATE blocks for the offset table and string pool substantially
// improve compression. This does not rely on Python, external executables or
// non-Go libraries and preserves all unknown entry bytes in their old positions.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
)

func compressBINBlocksGo(raw []byte, split1, split2 int) ([]byte, error) {
	if split1 <= 0 || split2 <= split1 || split2 >= len(raw) {
		return nil, fmt.Errorf("invalid BIN deflate split %d/%d (size %d)", split1, split2, len(raw))
	}
	var output bytes.Buffer
	z, err := zlib.NewWriterLevel(&output, 9)
	if err != nil {
		return nil, err
	}
	if _, err = z.Write(raw[:split1]); err != nil {
		return nil, err
	}
	if err = z.Flush(); err != nil {
		return nil, err
	}
	if _, err = z.Write(raw[split1:split2]); err != nil {
		return nil, err
	}
	if err = z.Flush(); err != nil {
		return nil, err
	}
	if _, err = z.Write(raw[split2:]); err != nil {
		return nil, err
	}
	if err = z.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// RebuildBINEntryFixedWidthGo rewrites the entire native BIN table and string
// pool while keeping the entry and compressed payload at exactly the old byte
// sizes. It tries a bounded, deterministic set of DEFLATE block boundaries.
// Failure is explicit when arbitrary future translations cannot safely fit.
func RebuildBINEntryFixedWidthGo(entry []byte, edits map[int][]byte) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), entry...), nil
	}
	orig, err := ParseBINEntry(entry)
	if err != nil {
		return nil, err
	}
	stringsBefore, err := orig.Strings()
	if err != nil {
		return nil, err
	}
	stringsAfter := make([][]byte, len(stringsBefore))
	copy(stringsAfter, stringsBefore)
	for i, txt := range edits {
		if i < 0 || i >= len(stringsAfter) {
			return nil, fmt.Errorf("BIN row index %d invalid", i)
		}
		if bytes.IndexByte(txt, 0) >= 0 {
			return nil, fmt.Errorf("BIN row %d replacement contains NUL", i)
		}
		stringsAfter[i] = append([]byte(nil), txt...)
	}
	plain := append([]byte(nil), orig.Payload[:orig.PoolStart]...)
	for i, txt := range stringsAfter {
		binary.LittleEndian.PutUint64(plain[0x10+16*i:0x18+16*i], uint64(len(plain)))
		plain = append(plain, txt...)
		plain = append(plain, 0)
	}
	if orig.PoolStart+1 >= len(plain) {
		return nil, fmt.Errorf("BIN has no string pool")
	}
	// One block for pointers; later blocks for the changing string distribution.
	// Try the best empirically tested range first but do not require a specific
	// distribution: future PC translation edits can change compression behavior.
	poolSize := len(plain) - orig.PoolStart
	fractions := [][2]int{{2, 5}, {1, 2}, {1, 3}, {3, 5}, {2, 3}, {1, 4}, {3, 4}}
	var best []byte
	for _, f := range fractions {
		split2 := orig.PoolStart + poolSize*f[0]/f[1]
		c, e := compressBINBlocksGo(plain, orig.PoolStart, split2)
		if e != nil {
			return nil, e
		}
		if best == nil || len(c) < len(best) {
			best = c
		}
	}
	// It must fit the OLD wrapper compression budget and preserve its suffix.
	// No unknown trailer bytes may move, even by a single byte.
	result, err := RebuildBINEntryFromDenseZlibFixedWidth(entry, plain, best)
	if err != nil {
		return nil, fmt.Errorf("BIN fixed-width native Go compression: best %d bytes: %w", len(best), err)
	}
	got, err := ParseBINEntry(result)
	if err != nil {
		return nil, err
	}
	readBack, err := got.Strings()
	if err != nil {
		return nil, err
	}
	if len(readBack) != len(stringsAfter) {
		return nil, fmt.Errorf("BIN changed row count")
	}
	for i := range readBack {
		if !bytes.Equal(readBack[i], stringsAfter[i]) {
			return nil, fmt.Errorf("BIN row %d round-trip mismatch", i)
		}
	}
	return result, nil
}
