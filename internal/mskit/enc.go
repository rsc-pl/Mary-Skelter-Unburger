package mskit

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	OrochiWrapperHeaderSize = 0x80
	OrochiBILZOffset        = 0x80
	OrochiBILZDataOffset    = 0x90
	ENCRowRecordSize        = 12
)

type ENCRow struct {
	Index     int
	SizeField uint32
	Aux       uint32
	Offset    int
	Payload   []byte
	Padding   []byte
}

func (r ENCRow) Length() int         { return int(r.SizeField & 0x7fffffff) }
func (r ENCRow) LZOCompressed() bool { return r.SizeField&0x80000000 != 0 }

func (r ENCRow) DecodedPayload() ([]byte, error) {
	if !r.LZOCompressed() {
		return append([]byte(nil), r.Payload...), nil
	}
	return DecompressLZO1X(r.Payload, int(r.Aux))
}

type ENCFile struct {
	Entry   []byte
	Header  []byte
	Payload []byte
	Count   int
	Rows    []ENCRow
	Tag     string
}

func OrochiWrapperTag(entry []byte) string {
	if len(entry) < 4 || entry[0] != 0 {
		return ""
	}
	tag := []byte{entry[3], entry[2], entry[1], entry[0]}
	if tag[3] != 0 {
		return ""
	}
	for _, b := range tag[:3] {
		if b < 0x20 || b >= 0x7f {
			return ""
		}
	}
	return string(tag[:3])
}

func ParseENCEntry(entry []byte) (ENCFile, error) {
	if len(entry) < OrochiBILZDataOffset {
		return ENCFile{}, fmt.Errorf("ENC entry shorter than wrapper header")
	}
	if tag := OrochiWrapperTag(entry); tag != "enc" {
		return ENCFile{}, fmt.Errorf("wrapper tag is %q, not enc", tag)
	}
	if string(entry[OrochiBILZOffset:OrochiBILZOffset+4]) != "BILZ" {
		return ENCFile{}, fmt.Errorf("no BILZ block at 0x80")
	}
	usize := int(binary.BigEndian.Uint32(entry[0x84:0x88]))
	csize := int(binary.BigEndian.Uint32(entry[0x88:0x8c]))
	algo := int(binary.BigEndian.Uint32(entry[0x8c:0x90]))
	if csize < 0 || OrochiBILZDataOffset+csize > len(entry) {
		return ENCFile{}, fmt.Errorf("BILZ body exceeds ENC entry")
	}
	payload, err := InflateBILZ(entry[OrochiBILZDataOffset:OrochiBILZDataOffset+csize], algo)
	if err != nil {
		return ENCFile{}, err
	}
	if len(payload) != usize {
		return ENCFile{}, fmt.Errorf("ENC BILZ decoded size %d, expected %d", len(payload), usize)
	}
	if len(payload) < 4 {
		return ENCFile{}, fmt.Errorf("ENC payload too short")
	}
	count := int(binary.LittleEndian.Uint32(payload[:4]))
	need := 4 + count*ENCRowRecordSize
	if count < 0 || need > len(payload) {
		return ENCFile{}, fmt.Errorf("ENC row table outside payload")
	}
	records := make([][3]uint32, count)
	for i := 0; i < count; i++ {
		p := 4 + i*ENCRowRecordSize
		records[i] = [3]uint32{
			binary.LittleEndian.Uint32(payload[p : p+4]),
			binary.LittleEndian.Uint32(payload[p+4 : p+8]),
			binary.LittleEndian.Uint32(payload[p+8 : p+12]),
		}
	}
	rows := make([]ENCRow, 0, count)
	for i, rec := range records {
		length := int(rec[0] & 0x7fffffff)
		off := int(rec[2])
		if off < 0 || length < 0 || off > len(payload) || length > len(payload)-off {
			return ENCFile{}, fmt.Errorf("ENC row %d payload outside payload", i)
		}
		next := len(payload)
		if i+1 < len(records) {
			next = int(records[i+1][2])
		}
		if next < off+length || next > len(payload) {
			return ENCFile{}, fmt.Errorf("ENC row %d padding boundary invalid", i)
		}
		rows = append(rows, ENCRow{
			Index: i, SizeField: rec[0], Aux: rec[1], Offset: off,
			Payload: payload[off : off+length], Padding: payload[off+length : next],
		})
	}
	return ENCFile{
		Entry: append([]byte(nil), entry...), Header: append([]byte(nil), entry[:OrochiWrapperHeaderSize]...),
		Payload: payload, Count: count, Rows: rows, Tag: "enc",
	}, nil
}

type ENCString struct {
	RowIndex   int
	Offset     int
	Raw        []byte
	Terminated bool
}

func encTexty(c byte) bool {
	return (c >= 0x20 && c < 0x7f) || c == 0x09 || c == 0x0a || c == 0x0d || c >= 0x80
}

// ScanENCStrings mirrors the conservative legacy scanner exactly: byte offsets
// are preserved and only valid UTF-8 runs with enough letters are emitted.
func ScanENCStrings(payload []byte, minChars, minLetters int) []ENCString {
	if minChars <= 0 {
		minChars = 2
	}
	if minLetters <= 0 {
		minLetters = 3
	}
	out := []ENCString{}
	for i := 0; i < len(payload); {
		if !encTexty(payload[i]) {
			i++
			continue
		}
		j := i
		for j < len(payload) && encTexty(payload[j]) {
			j++
		}
		raw := payload[i:j]
		terminated := j < len(payload) && payload[j] == 0
		text := string(raw)
		if !utf8.Valid(raw) {
			i = j
			continue
		}
		letters := 0
		other := 0
		chars := []rune(text)
		for _, r := range chars {
			if unicode.IsLetter(r) {
				letters++
				continue
			}
			// Python str.isalnum(), used by the canonical legacy scanner, accepts
			// every Unicode Number category (Nd/Nl/No), not only decimal digits.
			// unicode.IsNumber is the Go-equivalent needed for byte-for-byte
			// scanner parity (e.g. circled numerals ①/②/④/⑥/⑧).
			if unicode.IsNumber(r) || strings.ContainsRune(" \t\n\r!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~\u3000\u3001\u3002！ ？（）【】「」『』・―…‘’“”", r) {
				continue
			}
			other++
		}
		if len(chars) >= minChars && letters >= minLetters && float64(other)/float64(maxInt(1, len(chars))) <= 0.25 {
			out = append(out, ENCString{RowIndex: -1, Offset: i, Raw: append([]byte(nil), raw...), Terminated: terminated})
		}
		if j > i {
			i = j
		} else {
			i++
		}
	}
	return out
}

func (e ENCFile) Strings() ([]ENCString, error) {
	out := []ENCString{}
	for _, row := range e.Rows {
		decoded, err := row.DecodedPayload()
		if err != nil {
			return nil, err
		}
		ss := ScanENCStrings(decoded, 2, 3)
		for i := range ss {
			ss[i].RowIndex = row.Index
		}
		out = append(out, ss...)
	}
	return out, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
