package mskit

import (
	"bytes"
	"fmt"
)

const GBNLHeaderSize = 0x40

const (
	gbnlInt32  = 0
	gbnlInt8   = 1
	gbnlInt16  = 2
	gbnlFloat  = 3
	gbnlString = 5
)

type GBNLItem struct {
	Kind   string
	Size   int
	Offset int
}

type GBNLField struct {
	Kind      string
	Size      int
	Offset    int
	StrRaw    []byte
	StrOffset *int
	IsNull    bool
}

type GBNL struct {
	Raw          []byte
	HeaderOffset int
	GST          bool
	BigEndian    bool
	EndianMarker string
	Field04      uint16
	Field06      uint16
	Field08      uint32
	Field0C      uint32
	Flags        uint32
	DescrOffset  int
	CountMsgs    int
	MsgDescrSize int
	CountTypes   int
	OffsetTypes  int
	Field28      uint32
	OffsetMsgs   int
	Field30      uint32
	Items        []GBNLItem
}

func ParseGBNL(data []byte) (GBNL, error) {
	if len(data) < GBNLHeaderSize {
		return GBNL{}, fmt.Errorf("GBNL blob shorter than header")
	}
	g := GBNL{Raw: data}
	switch {
	case bytes.Equal(data[:3], []byte("GST")):
		g.GST = true
		g.HeaderOffset = 0
	case bytes.Equal(data[len(data)-GBNLHeaderSize:len(data)-GBNLHeaderSize+3], []byte("GBN")):
		g.HeaderOffset = len(data) - GBNLHeaderSize
	default:
		return GBNL{}, fmt.Errorf("no GST header / GBN footer")
	}
	h := data[g.HeaderOffset : g.HeaderOffset+GBNLHeaderSize]
	if h[3] != 'L' && h[3] != 'B' {
		return GBNL{}, fmt.Errorf("invalid GBNL endian marker %q", h[3])
	}
	g.BigEndian = h[3] == 'B'
	g.EndianMarker = "<"
	if g.BigEndian {
		g.EndianMarker = ">"
	}
	bo := endian(g.BigEndian)
	g.Field04 = bo.Uint16(h[4:6])
	g.Field06 = bo.Uint16(h[6:8])
	g.Field08 = bo.Uint32(h[8:12])
	g.Field0C = bo.Uint32(h[12:16])
	g.Flags = bo.Uint32(h[16:20])
	g.DescrOffset = int(bo.Uint32(h[20:24]))
	g.CountMsgs = int(bo.Uint32(h[24:28]))
	g.MsgDescrSize = int(bo.Uint32(h[28:32]))
	g.CountTypes = int(bo.Uint32(h[32:36]))
	g.OffsetTypes = int(bo.Uint32(h[36:40]))
	g.Field28 = bo.Uint32(h[40:44])
	g.OffsetMsgs = int(bo.Uint32(h[44:48]))
	g.Field30 = bo.Uint32(h[48:52])
	if g.Field04 != 1 || g.Field06 != 0 {
		return GBNL{}, fmt.Errorf("unexpected GBNL header words %d/%d", g.Field04, g.Field06)
	}
	if g.Field08 != 16 || g.Field0C != 4 {
		return GBNL{}, fmt.Errorf("unexpected GBNL header words %d/%d", g.Field08, g.Field0C)
	}
	if g.MsgDescrSize <= 0 || g.CountMsgs <= 0 {
		return GBNL{}, fmt.Errorf("empty GBNL message table")
	}
	if g.DescrOffset < 0 || g.CountMsgs > (len(data)-g.DescrOffset)/g.MsgDescrSize {
		return GBNL{}, fmt.Errorf("GBNL message table outside blob")
	}
	items, err := g.buildItems()
	if err != nil {
		return GBNL{}, err
	}
	g.Items = items
	return g, nil
}

func (g GBNL) buildItems() ([]GBNLItem, error) {
	bo := endian(g.BigEndian)
	items := []GBNLItem{}
	calc, pos := 0, 0
	pending := -1
	resolve := func(next int) {
		diff := next - calc
		if diff <= 3 {
			items = append(items, GBNLItem{Kind: "i8", Size: 1, Offset: pending})
			if pending+1 > pos {
				pos = pending + 1
			}
		} else {
			size := diff + 1
			items = append(items, GBNLItem{Kind: "fixstr", Size: size, Offset: pending})
			if pending+size > pos {
				pos = pending + size
			}
		}
		pending = -1
	}
	for i := 0; i < g.CountTypes; i++ {
		rec := g.OffsetTypes + i*4
		if rec < 0 || rec+4 > len(g.Raw) {
			return nil, fmt.Errorf("GBNL type descriptor outside blob")
		}
		tcode := bo.Uint16(g.Raw[rec : rec+2])
		toff := int(bo.Uint16(g.Raw[rec+2 : rec+4]))
		if toff < calc {
			return nil, fmt.Errorf("GBNL type %d not ascending (%d < %d)", i, toff, calc)
		}
		if pending >= 0 {
			resolve(toff)
		}
		if toff > pos {
			items = append(items, GBNLItem{Kind: "pad", Size: toff - pos, Offset: pos})
			pos = toff
		}
		if tcode == gbnlInt8 {
			pending = toff
			calc = toff + 1
			continue
		}
		size, kind := 0, ""
		switch tcode {
		case gbnlInt32:
			size, kind = 4, "i32"
		case gbnlInt16:
			size, kind = 2, "i16"
		case gbnlFloat:
			size, kind = 4, "f32"
		case gbnlString:
			size, kind = 4, "str"
		default:
			return nil, fmt.Errorf("unsupported GBNL type code %d", tcode)
		}
		items = append(items, GBNLItem{Kind: kind, Size: size, Offset: toff})
		pos = toff + size
		calc = toff + size
	}
	if pending >= 0 {
		resolve(g.MsgDescrSize)
	}
	if pos < g.MsgDescrSize {
		items = append(items, GBNLItem{Kind: "pad", Size: g.MsgDescrSize - pos, Offset: pos})
	}
	return items, nil
}

func (g GBNL) Field(messageIndex, fieldIndex int) (GBNLField, error) {
	if messageIndex < 0 || messageIndex >= g.CountMsgs {
		return GBNLField{}, fmt.Errorf("GBNL message %d out of range", messageIndex)
	}
	if fieldIndex < 0 || fieldIndex >= len(g.Items) {
		return GBNLField{}, fmt.Errorf("GBNL field %d out of range", fieldIndex)
	}
	it := g.Items[fieldIndex]
	base := g.DescrOffset + messageIndex*g.MsgDescrSize
	if base+it.Offset < 0 || base+it.Offset+it.Size > len(g.Raw) {
		return GBNLField{}, fmt.Errorf("GBNL field bytes outside blob")
	}
	f := GBNLField{Kind: it.Kind, Size: it.Size, Offset: it.Offset}
	bo := endian(g.BigEndian)
	switch it.Kind {
	case "str":
		off := int(bo.Uint32(g.Raw[base+it.Offset : base+it.Offset+4]))
		if uint32(off) == 0xffffffff {
			f.IsNull = true
			return f, nil
		}
		f.StrOffset = &off
		pos := g.OffsetMsgs + off
		if pos < 0 || pos >= len(g.Raw) {
			return GBNLField{}, fmt.Errorf("GBNL string offset outside blob")
		}
		rel := bytes.IndexByte(g.Raw[pos:], 0)
		if rel < 0 {
			return GBNLField{}, fmt.Errorf("unterminated GBNL string")
		}
		f.StrRaw = append([]byte(nil), g.Raw[pos:pos+rel]...)
	case "fixstr":
		raw := g.Raw[base+it.Offset : base+it.Offset+it.Size]
		if n := bytes.IndexByte(raw, 0); n >= 0 {
			raw = raw[:n]
		}
		f.StrRaw = append([]byte(nil), raw...)
	}
	return f, nil
}

func GBNLBlobKind(data []byte) (string, error) {
	if len(data) >= 3 && bytes.Equal(data[:3], []byte("GST")) {
		return "GSTR", nil
	}
	if len(data) >= GBNLHeaderSize && bytes.Equal(data[len(data)-GBNLHeaderSize:len(data)-GBNLHeaderSize+3], []byte("GBN")) {
		return "GBIN", nil
	}
	return "", fmt.Errorf("not a GBNL/GSTR blob")
}
