package mskit

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	CL3SectionSize = 0x50
	CL3EntrySize   = 0x230
	CL3NameSize    = 0x200
)

type CL3Section struct {
	Index        int
	Name         string
	Count        int
	DataSize     int
	DataOffset   int
	RecordOffset int
}

type CL3File struct {
	Index       int
	Name        string
	Field200    uint32
	LocalOffset int
	Size        int
	LinkStart   uint32
	LinkCount   uint32
}

type CL3 struct {
	Raw            []byte
	MagicOffset    int
	BigEndian      bool
	Field04        uint32
	Field08        uint32
	SectionsOffset int
	Field14        uint32
	Sections       []CL3Section
	Files          []CL3File
	Collection     int
}

func endian(big bool) binary.ByteOrder {
	if big {
		return binary.BigEndian
	}
	return binary.LittleEndian
}

func cstr(raw []byte) string {
	if i := bytes.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	return string(raw)
}

func ParseCL3(data []byte) (CL3, error) {
	pos := bytes.Index(data, []byte("CL3"))
	if pos < 0 || pos+0x18 > len(data) {
		return CL3{}, fmt.Errorf("no/truncated CL3 header")
	}
	e := data[pos+3]
	if e != 'L' && e != 'B' {
		return CL3{}, fmt.Errorf("bad CL3 endian char %q", e)
	}
	big := e == 'B'
	bo := endian(big)
	field04 := bo.Uint32(data[pos+4 : pos+8])
	field08 := bo.Uint32(data[pos+8 : pos+12])
	sectionCount := int(bo.Uint32(data[pos+12 : pos+16]))
	sectionOffset := int(bo.Uint32(data[pos+16 : pos+20]))
	field14 := bo.Uint32(data[pos+20 : pos+24])
	if field04 != 0 {
		return CL3{}, fmt.Errorf("unexpected CL3 field_04=%d", field04)
	}
	if sectionCount <= 0 || sectionCount > 64 {
		return CL3{}, fmt.Errorf("implausible CL3 section count %d", sectionCount)
	}
	c := CL3{Raw: data, MagicOffset: pos, BigEndian: big, Field04: field04, Field08: field08, SectionsOffset: sectionOffset, Field14: field14, Collection: -1}
	for i := 0; i < sectionCount; i++ {
		rec := sectionOffset + i*CL3SectionSize // wrapper coordinates, matching shipped corpus
		if rec < 0 || rec+CL3SectionSize > len(data) {
			return CL3{}, fmt.Errorf("CL3 section %d record outside payload", i)
		}
		name := cstr(data[rec : rec+0x20])
		count := int(bo.Uint32(data[rec+0x20 : rec+0x24]))
		size := int(bo.Uint32(data[rec+0x24 : rec+0x28]))
		off := int(bo.Uint32(data[rec+0x28 : rec+0x2c]))
		c.Sections = append(c.Sections, CL3Section{Index: i, Name: name, Count: count, DataSize: size, DataOffset: off, RecordOffset: rec})
		if name == "FILE_COLLECTION" {
			c.Collection = i
		}
	}
	if c.Collection < 0 {
		return CL3{}, fmt.Errorf("no FILE_COLLECTION section")
	}
	coll := c.Sections[c.Collection]
	table := coll.DataOffset
	need := table + coll.Count*CL3EntrySize
	if table < 0 || need > len(data) {
		return CL3{}, fmt.Errorf("CL3 file table outside payload")
	}
	for i := 0; i < coll.Count; i++ {
		rec := table + i*CL3EntrySize
		name := cstr(data[rec : rec+CL3NameSize])
		f200 := bo.Uint32(data[rec+CL3NameSize : rec+CL3NameSize+4])
		lo := int(bo.Uint32(data[rec+0x204 : rec+0x208]))
		sz := int(bo.Uint32(data[rec+0x208 : rec+0x20c]))
		ls := bo.Uint32(data[rec+0x20c : rec+0x210])
		lc := bo.Uint32(data[rec+0x210 : rec+0x214])
		start := coll.DataOffset + lo
		if start < 0 || sz < 0 || start > len(data) || sz > len(data)-start {
			return CL3{}, fmt.Errorf("CL3 file %d payload outside container", i)
		}
		c.Files = append(c.Files, CL3File{Index: i, Name: name, Field200: f200, LocalOffset: lo, Size: sz, LinkStart: ls, LinkCount: lc})
	}
	return c, nil
}

func (c CL3) FileData(index int) ([]byte, error) {
	if index < 0 || index >= len(c.Files) {
		return nil, fmt.Errorf("CL3 file index %d out of range", index)
	}
	f := c.Files[index]
	coll := c.Sections[c.Collection]
	start := coll.DataOffset + f.LocalOffset
	return c.Raw[start : start+f.Size], nil
}

func (c CL3) FilesNamed(name string) []CL3File {
	var out []CL3File
	for _, f := range c.Files {
		if f.Name == name {
			out = append(out, f)
		}
	}
	return out
}

func encodeCL3Name(name string) ([]byte, error) {
	raw := append([]byte(name), 0)
	if len(raw) > CL3NameSize {
		return nil, fmt.Errorf("CL3 name too long: %q", name)
	}
	out := make([]byte, CL3NameSize)
	copy(out, raw)
	return out, nil
}

func (c CL3) Rebuild(replacements map[int][]byte) ([]byte, error) {
	if len(replacements) == 0 {
		return append([]byte(nil), c.Raw...), nil
	}
	coll := c.Sections[c.Collection]
	collStart := coll.DataOffset
	oldActual := CL3EntrySize * len(c.Files)
	for _, f := range c.Files {
		oldActual += f.Size
	}
	oldBlockEnd := collStart + coll.DataSize
	if oldBlockEnd > len(c.Raw) {
		oldBlockEnd = len(c.Raw)
	}
	if collStart < 0 || collStart+CL3EntrySize*len(c.Files) > len(c.Raw) || collStart+oldActual > len(c.Raw) {
		return nil, fmt.Errorf("CL3 collection bounds invalid")
	}
	table := append([]byte(nil), c.Raw[collStart:collStart+CL3EntrySize*len(c.Files)]...)
	body := make([]byte, 0)
	cursor := CL3EntrySize * len(c.Files)
	bo := endian(c.BigEndian)
	for i, f := range c.Files {
		payload, ok := replacements[i]
		if !ok {
			var err error
			payload, err = c.FileData(i)
			if err != nil {
				return nil, err
			}
		}
		name, err := encodeCL3Name(f.Name)
		if err != nil {
			return nil, err
		}
		base := i * CL3EntrySize
		copy(table[base:base+CL3NameSize], name)
		bo.PutUint32(table[base+0x200:base+0x204], f.Field200)
		bo.PutUint32(table[base+0x204:base+0x208], uint32(cursor))
		bo.PutUint32(table[base+0x208:base+0x20c], uint32(len(payload)))
		bo.PutUint32(table[base+0x20c:base+0x210], f.LinkStart)
		bo.PutUint32(table[base+0x210:base+0x214], f.LinkCount)
		body = append(body, payload...)
		cursor += len(payload)
	}
	// The reference Python CL3 rebuilder uses a slice whose start can lie
	// beyond the logical collection end. Python then returns an empty slice;
	// Go would panic. This occurs in actual PC texture collections where
	// the sum of file sizes exceeds FILE_COLLECTION.DataSize. Preserve the
	// reference behavior without consuming bytes belonging to the suffix.
	tailStart := collStart + oldActual
	if tailStart > oldBlockEnd {
		tailStart = oldBlockEnd
	}
	blockTail := append([]byte(nil), c.Raw[tailStart:oldBlockEnd]...)
	newActual := len(table) + len(body)
	physicalOld := oldBlockEnd - collStart
	physicalNew := newActual + len(blockTail)
	delta := physicalNew - physicalOld

	head := append([]byte(nil), c.Raw[:collStart]...)
	for _, s := range c.Sections {
		rec := s.RecordOffset
		if rec+0x2c > len(head) {
			return nil, fmt.Errorf("CL3 section record outside mutable head")
		}
		if s.Index == c.Collection {
			bo.PutUint32(head[rec+0x24:rec+0x28], uint32(s.DataSize+delta))
		} else if s.DataOffset >= coll.DataOffset {
			bo.PutUint32(head[rec+0x28:rec+0x2c], uint32(s.DataOffset+delta))
		}
	}
	out := make([]byte, 0, len(c.Raw)+delta)
	out = append(out, head...)
	out = append(out, table...)
	out = append(out, body...)
	out = append(out, blockTail...)
	out = append(out, c.Raw[oldBlockEnd:]...)
	return out, nil
}
