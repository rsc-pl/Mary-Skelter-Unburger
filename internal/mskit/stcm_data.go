package mskit

import (
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	STCMHeaderSize      = 0x30
	STCMExportSize      = 0x28
	STCMInstrHeaderSize = 0x10
	STCMParamSize       = 0x0c
	STCMDataHeaderSize  = 0x10

	stcmTagMemOffset = 0
	stcmTagIndirect  = 2
	stcmTagSpecial   = 3

	stcmSpecialInstrPtr0 = 0xffffff40
	stcmSpecialInstrPtr1 = 0xffffff41
)

type STCMDataItem struct {
	Offset     int
	Type       uint32
	OffsetUnit uint32
	Field8     uint32
	Length     int
	Payload    []byte
	IsString   bool
}

type STCMExport struct {
	Index  int
	Type   uint32
	Name   string
	Offset int
}

type STCM struct {
	Raw                  []byte
	BigEndian            bool
	ExportOffset         int
	ExportCount          int
	Field28              uint32
	CollectionLinkOffset int
	Exports              []STCMExport
	DataItems            map[int]STCMDataItem
	DataOrder            []int
}

func stcmTag(x uint32) uint32 { return x >> 30 }
func stcmValue(x uint32) int  { return int(x & 0x3fffffff) }

func STCMDataAt(data []byte, offset int) (STCMDataItem, error) {
	if len(data) < STCMHeaderSize || string(data[:5]) != "STCM2" {
		return STCMDataItem{}, fmt.Errorf("not an STCM2 file")
	}
	var bo binary.ByteOrder
	switch data[5] {
	case 'L':
		bo = binary.LittleEndian
	case 'B':
		bo = binary.BigEndian
	default:
		return STCMDataItem{}, fmt.Errorf("bad STCM endian marker %q", data[5])
	}
	if offset < 0 || offset+STCMDataHeaderSize > len(data) {
		return STCMDataItem{}, fmt.Errorf("STCM data header at %#x outside file", offset)
	}
	typ := bo.Uint32(data[offset : offset+4])
	ou := bo.Uint32(data[offset+4 : offset+8])
	f8 := bo.Uint32(data[offset+8 : offset+12])
	n := int(bo.Uint32(data[offset+12 : offset+16]))
	if n < 0 || offset+STCMDataHeaderSize+n > len(data) {
		return STCMDataItem{}, fmt.Errorf("STCM data payload at %#x outside file", offset)
	}
	payload := append([]byte(nil), data[offset+STCMDataHeaderSize:offset+STCMDataHeaderSize+n]...)
	item := STCMDataItem{Offset: offset, Type: typ, OffsetUnit: ou, Field8: f8, Length: n, Payload: payload}
	if typ == 0 && ou > 1 && f8 == 1 {
		need := int(ou) * 4
		if len(payload) == need && need > 0 {
			end := -1
			for i, b := range payload {
				if b == 0 {
					end = i
					break
				}
			}
			if end >= 0 {
				pad := payload[end+1:]
				zero := true
				for _, b := range pad {
					if b != 0 {
						zero = false
						break
					}
				}
				if len(pad) <= 4 && zero {
					item.IsString = true
				}
			}
		}
	}
	return item, nil
}

func ParseSTCM(data []byte) (STCM, error) {
	if len(data) < STCMHeaderSize || string(data[:5]) != "STCM2" {
		return STCM{}, fmt.Errorf("not an STCM2 file")
	}
	big := false
	switch data[5] {
	case 'L':
	case 'B':
		big = true
	default:
		return STCM{}, fmt.Errorf("bad STCM endian marker %q", data[5])
	}
	bo := endian(big)
	exportOff := int(bo.Uint32(data[0x20:0x24]))
	exportCount := int(bo.Uint32(data[0x24:0x28]))
	field28 := bo.Uint32(data[0x28:0x2c])
	coll := int(bo.Uint32(data[0x2c:0x30]))
	if exportCount < 0 || exportCount > 100000 || exportOff < 0 || exportOff+exportCount*STCMExportSize > len(data) {
		return STCM{}, fmt.Errorf("STCM export table outside file")
	}
	s := STCM{Raw: data, BigEndian: big, ExportOffset: exportOff, ExportCount: exportCount, Field28: field28, CollectionLinkOffset: coll, DataItems: map[int]STCMDataItem{}}
	type node struct {
		off  int
		data bool
	}
	queue := make([]node, 0, exportCount)
	for i := 0; i < exportCount; i++ {
		rec := exportOff + i*STCMExportSize
		typ := bo.Uint32(data[rec : rec+4])
		nameRaw := data[rec+4 : rec+0x24]
		name := cstr(nameRaw)
		off := int(bo.Uint32(data[rec+0x24 : rec+0x28]))
		s.Exports = append(s.Exports, STCMExport{Index: i, Type: typ, Name: name, Offset: off})
		queue = append(queue, node{off: off, data: typ == 1})
	}
	seenData := map[int]bool{}
	seenInstr := map[int]bool{}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n.off <= 0 || n.off >= len(data) {
			continue
		}
		if n.data {
			if seenData[n.off] {
				continue
			}
			seenData[n.off] = true
			item, err := STCMDataAt(data, n.off)
			if err != nil {
				continue
			}
			s.DataItems[n.off] = item
			s.DataOrder = append(s.DataOrder, n.off)
			continue
		}
		if seenInstr[n.off] {
			continue
		}
		seenInstr[n.off] = true
		if n.off+STCMInstrHeaderSize > len(data) {
			continue
		}
		isCall := bo.Uint32(data[n.off : n.off+4])
		opcode := bo.Uint32(data[n.off+4 : n.off+8])
		pc := int(bo.Uint32(data[n.off+8 : n.off+12]))
		size := int(bo.Uint32(data[n.off+12 : n.off+16]))
		head := STCMInstrHeaderSize + pc*STCMParamSize
		if pc > 16 || size < head || n.off+size > len(data) {
			continue
		}
		if isCall != 0 {
			queue = append(queue, node{off: int(opcode), data: false})
		}
		for i := 0; i < pc; i++ {
			p := n.off + STCMInstrHeaderSize + i*STCMParamSize
			p0 := bo.Uint32(data[p : p+4])
			p4 := bo.Uint32(data[p+4 : p+8])
			p8 := bo.Uint32(data[p+8 : p+12])
			switch stcmTag(p0) {
			case stcmTagMemOffset:
				queue = append(queue, node{off: stcmValue(p0), data: true})
			case stcmTagIndirect:
				if stcmTag(p8) == stcmTagMemOffset {
					queue = append(queue, node{off: stcmValue(p8), data: true})
				}
			case stcmTagSpecial:
				if p0 == stcmSpecialInstrPtr0 || p0 == stcmSpecialInstrPtr1 {
					queue = append(queue, node{off: int(p4), data: false})
				}
			default:
				if stcmTag(p4) == stcmTagMemOffset {
					queue = append(queue, node{off: stcmValue(p4), data: true})
				}
				if stcmTag(p8) == stcmTagMemOffset {
					queue = append(queue, node{off: stcmValue(p8), data: true})
				}
			}
		}
		if isCall != 0 || (opcode != 0 && opcode != 6) {
			queue = append(queue, node{off: n.off + size, data: false})
		}
	}
	return s, nil
}

func (s STCM) StringItems() []STCMDataItem {
	out := []STCMDataItem{}
	for _, off := range s.DataOrder {
		if d := s.DataItems[off]; d.IsString {
			out = append(out, d)
		}
	}
	return out
}
func (s STCM) GBNLItems() []STCMDataItem {
	out := []STCMDataItem{}
	for _, off := range s.DataOrder {
		d := s.DataItems[off]
		if d.IsString || len(d.Payload) < GBNLHeaderSize {
			continue
		}
		if len(d.Payload) >= 3 && string(d.Payload[:3]) == "GST" || (len(d.Payload) >= GBNLHeaderSize && string(d.Payload[len(d.Payload)-GBNLHeaderSize:len(d.Payload)-GBNLHeaderSize+3]) == "GBN") {
			out = append(out, d)
		}
	}
	return out
}
func (d STCMDataItem) StringBytes() []byte {
	if !d.IsString {
		return nil
	}
	n := len(d.Payload)
	for i, b := range d.Payload {
		if b == 0 {
			n = i
			break
		}
	}
	return append([]byte(nil), d.Payload[:n]...)
}

// SortedDataOffsets is useful for structural diagnostics without changing the
// legacy semantic traversal order used by StringItems/GBNLItems.
func (s STCM) SortedDataOffsets() []int {
	out := append([]int(nil), s.DataOrder...)
	sort.Ints(out)
	return out
}
