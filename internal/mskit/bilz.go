package mskit

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	BILZHeaderSize = 16
	BILZAlgoStored = 0
	BILZAlgoZlib   = 7
)

type BILZHeader struct {
	Offset int
	USize  int
	CSize  int
	Algo   int
}

func (h BILZHeader) PayloadOffset() int { return h.Offset + BILZHeaderSize }

func FindBILZHeader(buf []byte, start, scanLimit int) (*BILZHeader, error) {
	if start < 0 {
		start = 0
	}
	if scanLimit <= 0 {
		scanLimit = 0x10000
	}
	end := start + scanLimit
	if end > len(buf) {
		end = len(buf)
	}
	for pos := start; pos+4 <= end; {
		rel := bytes.Index(buf[pos:end], []byte("BILZ"))
		if rel < 0 {
			return nil, nil
		}
		p := pos + rel
		if p+BILZHeaderSize <= len(buf) {
			usize := int(binary.BigEndian.Uint32(buf[p+4 : p+8]))
			csize := int(binary.BigEndian.Uint32(buf[p+8 : p+12]))
			algo := int(binary.BigEndian.Uint32(buf[p+12 : p+16]))
			if usize > 0 && csize > 0 && usize < 1<<31 && csize < 1<<31 && (algo == BILZAlgoStored || algo == BILZAlgoZlib) {
				return &BILZHeader{Offset: p, USize: usize, CSize: csize, Algo: algo}, nil
			}
		}
		pos = p + 1
	}
	return nil, nil
}

func InflateBILZ(data []byte, algo int) ([]byte, error) {
	switch algo {
	case BILZAlgoStored:
		return append([]byte(nil), data...), nil
	case BILZAlgoZlib:
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(zr)
	default:
		return nil, fmt.Errorf("unsupported BILZ algorithm %d", algo)
	}
}

func DecodeBILZWrapper(wrapper []byte) ([]byte, *BILZHeader, error) {
	h, err := FindBILZHeader(wrapper, 0, len(wrapper))
	if err != nil || h == nil {
		return nil, h, err
	}
	if h.PayloadOffset()+h.CSize > len(wrapper) {
		return nil, h, fmt.Errorf("BILZ body exceeds wrapper")
	}
	raw, err := InflateBILZ(wrapper[h.PayloadOffset():h.PayloadOffset()+h.CSize], h.Algo)
	if err != nil {
		return nil, h, err
	}
	if len(raw) != h.USize {
		return nil, h, fmt.Errorf("BILZ decoded size %d, expected %d", len(raw), h.USize)
	}
	return raw, h, nil
}

// WrapBILZGo produces a semantically valid BILZ wrapper. Go's DEFLATE encoder
// is intentionally not claimed to be byte-identical to CPython/system zlib;
// callers that require historical canonical compressed bytes must not silently
// substitute this for the Python builder without a content-lineage migration.
func WrapBILZGo(data []byte, algo, level int) ([]byte, error) {
	var body []byte
	switch algo {
	case BILZAlgoStored:
		body = append([]byte(nil), data...)
	case BILZAlgoZlib:
		var b bytes.Buffer
		zw, err := zlib.NewWriterLevel(&b, level)
		if err != nil {
			return nil, err
		}
		if _, err := zw.Write(data); err != nil {
			_ = zw.Close()
			return nil, err
		}
		if err := zw.Close(); err != nil {
			return nil, err
		}
		body = b.Bytes()
	default:
		return nil, fmt.Errorf("unsupported BILZ algorithm %d", algo)
	}
	out := make([]byte, BILZHeaderSize+len(body))
	copy(out[:4], []byte("BILZ"))
	binary.BigEndian.PutUint32(out[4:8], uint32(len(data)))
	binary.BigEndian.PutUint32(out[8:12], uint32(len(body)))
	binary.BigEndian.PutUint32(out[12:16], uint32(algo))
	copy(out[16:], body)
	return out, nil
}
