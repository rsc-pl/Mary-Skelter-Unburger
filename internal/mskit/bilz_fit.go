package mskit

// Dense DEFLATE wrapping for source-pinned BIN archives with an immovable opaque
// suffix. The compressor that supplies denseZlib must be independently verified.
// This file does NOT implement an optimal encoder and is not a release path.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
)

// Empty DEFLATE prefixes: each is one dynamic-Huffman no-output block,
// followed by one empty stored block, ending on a byte boundary. They are
// non-final, so the caller's deflate stream follows unchanged.
var emptyDeflatePrefix = map[int]string{
	1: "04c0810800000000207feb070000ffff",       // 16 bytes
	2: "04c0810800000000207fea1e000000ffff",     // 17 bytes
	3: "04c0810800000000207fe6aa1e000000ffff",   // 18 bytes
	4: "04c0810800000000207fe2aaaa1e000000ffff", // 19 bytes
}
var emptyStoredDeflateBlock = []byte{0, 0, 0, 0xff, 0xff}

func strictUnzlib(b []byte) ([]byte, error) {
	r := bytes.NewReader(b)
	z, err := zlib.NewReader(r)
	if err != nil {
		return nil, err
	}
	p, err := io.ReadAll(z)
	closeErr := z.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if r.Len() != 0 {
		return nil, fmt.Errorf("zlib stream has %d unused trailing bytes", r.Len())
	}
	return p, nil
}

// PadDenseZlibWithoutChangingText returns an RFC1950 zlib stream of exactly
// targetSize bytes, using empty non-final DEFLATE blocks. It refuses to truncate
// a stream, to alter its decoded bytes, or to leave bytes after Adler32.
func PadDenseZlibWithoutChangingText(denseZlib []byte, targetSize int) ([]byte, error) {
	if len(denseZlib) < 6 {
		return nil, fmt.Errorf("short zlib stream")
	}
	before, err := strictUnzlib(denseZlib)
	if err != nil {
		return nil, fmt.Errorf("dense input: %w", err)
	}
	delta := targetSize - len(denseZlib)
	if delta < 0 {
		return nil, fmt.Errorf("compressed stream too large: %d > %d", len(denseZlib), targetSize)
	}
	if delta == 0 {
		return append([]byte(nil), denseZlib...), nil
	}
	var extra []byte
	if delta%5 != 0 {
		head, e := hex.DecodeString(emptyDeflatePrefix[delta%5])
		if e != nil {
			return nil, e
		}
		if len(head) > delta {
			return nil, fmt.Errorf("shortfall %d cannot be represented with safe empty blocks", delta)
		}
		extra = append(extra, head...)
	}
	for len(extra) < delta {
		extra = append(extra, emptyStoredDeflateBlock...)
	}
	if len(extra) != delta {
		return nil, fmt.Errorf("internal deflate padding error")
	}
	result := make([]byte, 0, targetSize)
	result = append(result, denseZlib[:2]...)
	result = append(result, extra...)
	result = append(result, denseZlib[2:]...)
	if len(result) != targetSize {
		return nil, fmt.Errorf("padded stream length error")
	}
	after, err := strictUnzlib(result)
	if err != nil {
		return nil, fmt.Errorf("padded stream: %w", err)
	}
	if !bytes.Equal(before, after) {
		return nil, fmt.Errorf("DEFLATE padding modified decoded payload")
	}
	return result, nil
}

// RebuildBINEntryFromDenseZlibFixedWidth accepts a separately compressed zlib
// stream for a fully rebuilt BIN plaintext and preserves the ORIGINAL wrapper
// size, compressed-size field, and all bytes following the compressed stream.
// Passing an independently compressed stream is deliberate: Go's stdlib zlib
// currently cannot produce a dense enough stream for Western MS2 archive 073.
func RebuildBINEntryFromDenseZlibFixedWidth(original, newBIN, denseZlib []byte) ([]byte, error) {
	if len(original) < OrochiBILZDataOffset || OrochiWrapperTag(original) != "bin" || string(original[0x80:0x84]) != "BILZ" {
		return nil, fmt.Errorf("not an Orochi BIN/BILZ entry")
	}
	if binary.BigEndian.Uint32(original[0x8c:0x90]) != BILZAlgoZlib {
		return nil, fmt.Errorf("only zlib BILZ supported")
	}
	oldSize := int(binary.BigEndian.Uint32(original[0x88:0x8c]))
	if oldSize <= 0 || 0x90+oldSize > len(original) || len(newBIN) > int(^uint32(0)) {
		return nil, fmt.Errorf("BIN BILZ bounds unsupported")
	}
	if _, err := DecodeOrochiEntry(original, "bin"); err != nil {
		return nil, fmt.Errorf("bad original: %w", err)
	}
	want, err := strictUnzlib(denseZlib)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(want, newBIN) {
		return nil, fmt.Errorf("dense zlib does not decode to the requested BIN")
	}
	padded, err := PadDenseZlibWithoutChangingText(denseZlib, oldSize)
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), original...)
	binary.BigEndian.PutUint32(out[0x84:0x88], uint32(len(newBIN)))
	copy(out[0x90:0x90+oldSize], padded)
	verified, err := DecodeOrochiEntry(out, "bin")
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(verified, newBIN) {
		return nil, fmt.Errorf("rebuilt fixed-size BIN mismatch")
	}
	if !bytes.Equal(out[0x90+oldSize:], original[0x90+oldSize:]) || len(out) != len(original) {
		return nil, fmt.Errorf("BIN opaque trailer/size changed")
	}
	return out, nil
}
