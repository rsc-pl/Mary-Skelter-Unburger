package mskit

// Bounded Switch GDAT source adapter. This remains separate from the canonical
// production GDAT parser so PC source-cache and existing graphics behavior do
// not change. Unknown nonzero trailers ALWAYS fail unless a specific pristine
// source and exact trailer have independent, pinned evidence. Even then they
// may be preserved only at their original absolute byte position.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

const MaxSwitchGDATZeroTail = 1 << 20

type GDATOpaqueTailEvidence struct {
	SourceSHA256 string
	TailSHA256   string
	EntryCount   int
	IndexedEnd   int
	TailLength   int
}

type SwitchGDAT struct {
	GDAT
	ZeroTail   []byte
	OpaqueTail []byte
	indexedEnd int
	sourceSHA  string // nonempty ONLY following full-source evidence verification
}

func checkSwitchGDATTail(tail []byte) error {
	if len(tail) > MaxSwitchGDATZeroTail {
		return fmt.Errorf("GDAT has %d trailing bytes (limit %d); cannot prove padding", len(tail), MaxSwitchGDATZeroTail)
	}
	for i, v := range tail {
		if v != 0 {
			return fmt.Errorf("GDAT has %d trailing bytes, first nonzero at +%d (0x%02x); unknown footer cannot be relocated", len(tail), i, v)
		}
	}
	return nil
}

func digestHex(p []byte) string { sum := sha256.Sum256(p); return hex.EncodeToString(sum[:]) }

func parseSwitchGDAT(raw []byte, proof *GDATOpaqueTailEvidence) (SwitchGDAT, error) {
	if len(raw) < GDATHeaderSize || string(raw[:4]) != "GDAT" {
		return SwitchGDAT{}, fmt.Errorf("not a GDAT archive")
	}
	count := uint64(binary.LittleEndian.Uint32(raw[4:8]))
	if count > 5_000_000 {
		return SwitchGDAT{}, fmt.Errorf("implausible GDAT entry count %d", count)
	}
	headEnd := uint64(GDATHeaderSize) + count*GDATEntrySize
	if headEnd > uint64(len(raw)) {
		return SwitchGDAT{}, fmt.Errorf("truncated GDAT table")
	}
	lastEnd := headEnd
	if count > 0 {
		off := int(headEnd - GDATEntrySize)
		start := uint64(binary.LittleEndian.Uint32(raw[off : off+4]))
		size := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		lastEnd = start + size
		if lastEnd < headEnd || lastEnd > uint64(len(raw)) {
			return SwitchGDAT{}, fmt.Errorf("GDAT last entry outside archive")
		}
	}
	trailer := raw[int(lastEnd):]
	if e := checkSwitchGDATTail(trailer); e != nil {
		// Fail closed by default. An explicit source-locked witness is necessary
		// merely to *preserve* the unknown bytes, not to move or interpret them.
		if proof == nil || count != uint64(proof.EntryCount) || int(lastEnd) != proof.IndexedEnd ||
			len(trailer) != proof.TailLength || len(trailer) > MaxSwitchGDATZeroTail ||
			digestHex(raw) != proof.SourceSHA256 || digestHex(trailer) != proof.TailSHA256 {
			return SwitchGDAT{}, e
		}
		g, parseErr := ParseGDAT(raw[:int(lastEnd)])
		if parseErr != nil {
			return SwitchGDAT{}, parseErr
		}
		g.Raw = raw
		return SwitchGDAT{GDAT: g, OpaqueTail: trailer, indexedEnd: int(lastEnd), sourceSHA: proof.SourceSHA256}, nil
	}
	if proof != nil {
		return SwitchGDAT{}, fmt.Errorf("opaque-tail evidence does not apply to a zero-filled trailer")
	}
	g, err := ParseGDAT(raw[:int(lastEnd)])
	if err != nil {
		return SwitchGDAT{}, err
	}
	g.Raw = raw
	return SwitchGDAT{GDAT: g, ZeroTail: trailer, indexedEnd: int(lastEnd)}, nil
}

func ParseSwitchGDAT(raw []byte) (SwitchGDAT, error) {
	return parseSwitchGDAT(raw, nil)
}

// ParseSwitchGDATWithOpaqueTailEvidence may only be used for one previously
// inspected Western source, pinned by the SHA-256 of its ENTIRE original file,
// its opaque trailer, its entry count, and the original trailer offset. Other
// archives, updates and modifications fail closed.
func ParseSwitchGDATWithOpaqueTailEvidence(raw []byte, proof GDATOpaqueTailEvidence) (SwitchGDAT, error) {
	return parseSwitchGDAT(raw, &proof)
}

// BuildSwitchGDAT preserves ordinary zero padding; opaque bytes are never
// accepted through this generic API.
func BuildSwitchGDAT(entries [][]byte, sourceZeroTail []byte) ([]byte, error) {
	if err := checkSwitchGDATTail(sourceZeroTail); err != nil {
		return nil, err
	}
	base, err := BuildGDAT(entries)
	if err != nil {
		return nil, err
	}
	if uint64(len(base))+uint64(len(sourceZeroTail)) > uint64(^uint32(0)) {
		return nil, fmt.Errorf("GDAT size overflow")
	}
	return append(base, sourceZeroTail...), nil
}

// BuildSwitchGDATFromSource keeps a source-pinned opaque tail at the SAME
// absolute offset. With an opaque trailer, every indexed entry MUST retain its
// original size, so the index table, all entry offsets and the trailer's
// position are byte-for-byte unchanged. The source remains read-only.
func BuildSwitchGDATFromSource(source SwitchGDAT, entries [][]byte) ([]byte, error) {
	if len(source.OpaqueTail) == 0 {
		return BuildSwitchGDAT(entries, source.ZeroTail)
	}
	if source.sourceSHA == "" || digestHex(source.Raw) != source.sourceSHA || len(entries) != len(source.Entries) {
		return nil, fmt.Errorf("opaque GDAT tail not backed by verified original source")
	}
	for i := range entries {
		if len(entries[i]) != len(source.Entries[i]) {
			return nil, fmt.Errorf("opaque GDAT tail must stay at original offset: entry %d changes size %d -> %d; relocation forbidden", i, len(source.Entries[i]), len(entries[i]))
		}
	}
	base, err := BuildGDAT(entries)
	if err != nil {
		return nil, err
	}
	headEnd := GDATHeaderSize + GDATEntrySize*len(entries)
	if len(base) != source.indexedEnd || len(source.Raw) != source.indexedEnd+len(source.OpaqueTail) ||
		!bytes.Equal(base[:headEnd], source.Raw[:headEnd]) ||
		!bytes.Equal(source.OpaqueTail, source.Raw[source.indexedEnd:]) {
		return nil, fmt.Errorf("opaque GDAT tail relocation or index-table drift forbidden")
	}
	return append(base, source.OpaqueTail...), nil
}

// VerifySwitchGDATRebuild checks the opaque trailer, table, and offsets again
// independently after serialization. It never trusts a rebuilt archive to
// re-qualify as the pinned pristine source, whose hash necessarily changed.
func VerifySwitchGDATRebuild(source SwitchGDAT, rebuilt []byte) (SwitchGDAT, error) {
	if len(source.OpaqueTail) == 0 {
		return ParseSwitchGDAT(rebuilt)
	}
	headEnd := GDATHeaderSize + GDATEntrySize*len(source.Entries)
	if len(rebuilt) != len(source.Raw) || source.sourceSHA == "" || digestHex(source.Raw) != source.sourceSHA ||
		!bytes.Equal(rebuilt[:headEnd], source.Raw[:headEnd]) ||
		!bytes.Equal(rebuilt[source.indexedEnd:], source.OpaqueTail) {
		return SwitchGDAT{}, fmt.Errorf("opaque GDAT trailer position, contents or entry table changed")
	}
	parsed, err := ParseGDAT(rebuilt[:source.indexedEnd])
	if err != nil {
		return SwitchGDAT{}, err
	}
	parsed.Raw = rebuilt
	return SwitchGDAT{GDAT: parsed, OpaqueTail: rebuilt[source.indexedEnd:], indexedEnd: source.indexedEnd, sourceSHA: source.sourceSHA}, nil
}
