package mskit

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestGDATRoundTrip(t *testing.T) {
	entries := [][]byte{[]byte("alpha"), {0, 1, 2, 3}, []byte("omega")}
	raw, err := BuildGDAT(entries)
	if err != nil {
		t.Fatal(err)
	}
	g, err := ParseGDAT(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Entries) != len(entries) {
		t.Fatalf("entry count %d", len(g.Entries))
	}
	for i := range entries {
		if !bytes.Equal(g.Entries[i], entries[i]) {
			t.Fatalf("entry %d drift", i)
		}
	}
	rebuilt, err := BuildGDAT(g.Entries)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, rebuilt) {
		t.Fatal("unchanged GDAT rebuild not byte-identical")
	}
}

func TestBILZPrefixAndRoundTrip(t *testing.T) {
	src := bytes.Repeat([]byte("MarySkelter"), 100)
	for _, algo := range []int{BILZAlgoStored, BILZAlgoZlib} {
		w, err := WrapBILZGo(src, algo, 9)
		if err != nil {
			t.Fatal(err)
		}
		prefixed := append(bytes.Repeat([]byte{0xA5}, 0x80), w...)
		h, err := FindBILZHeader(prefixed, 0, 0x10000)
		if err != nil {
			t.Fatal(err)
		}
		if h == nil || h.Offset != 0x80 || h.Algo != algo || h.USize != len(src) {
			t.Fatalf("header=%+v", h)
		}
		got, h2, err := DecodeBILZWrapper(prefixed)
		if err != nil {
			t.Fatal(err)
		}
		if h2.Offset != 0x80 || !bytes.Equal(got, src) {
			t.Fatal("BILZ round-trip drift")
		}
	}
}

func synthCL3(t *testing.T, big bool) []byte {
	t.Helper()
	bo := binary.ByteOrder(binary.LittleEndian)
	endianChar := byte('L')
	if big {
		bo = binary.BigEndian
		endianChar = 'B'
	}
	const prefix = 0x10
	const sections = 0x30
	const coll = 0xD0
	const count = 2
	dataSize := CL3EntrySize*count + 3 + 5
	total := coll + dataSize + 4
	b := make([]byte, total)
	for i := 0; i < prefix; i++ {
		b[i] = 0xCC
	}
	copy(b[prefix:prefix+3], []byte("CL3"))
	b[prefix+3] = endianChar
	bo.PutUint32(b[prefix+4:prefix+8], 0)
	bo.PutUint32(b[prefix+8:prefix+12], 3)
	bo.PutUint32(b[prefix+12:prefix+16], 2)
	bo.PutUint32(b[prefix+16:prefix+20], sections)
	bo.PutUint32(b[prefix+20:prefix+24], 0x12345678)
	writeSection := func(rec int, name string, n, size, off int) {
		copy(b[rec:rec+0x20], []byte(name))
		bo.PutUint32(b[rec+0x20:rec+0x24], uint32(n))
		bo.PutUint32(b[rec+0x24:rec+0x28], uint32(size))
		bo.PutUint32(b[rec+0x28:rec+0x2c], uint32(off))
	}
	writeSection(sections, "FILE_COLLECTION", count, dataSize, coll)
	writeSection(sections+CL3SectionSize, "FILE_LINK", 0, 0, coll+dataSize)
	payloads := [][]byte{[]byte("AAA"), []byte("BBBBB")}
	cursor := CL3EntrySize * count
	for i, name := range []string{"one.bin", "two.bin"} {
		rec := coll + i*CL3EntrySize
		copy(b[rec:rec+CL3NameSize], append([]byte(name), 0))
		bo.PutUint32(b[rec+0x200:rec+0x204], uint32(0x10+i))
		bo.PutUint32(b[rec+0x204:rec+0x208], uint32(cursor))
		bo.PutUint32(b[rec+0x208:rec+0x20c], uint32(len(payloads[i])))
		bo.PutUint32(b[rec+0x20c:rec+0x210], uint32(i))
		bo.PutUint32(b[rec+0x210:rec+0x214], uint32(i+1))
		for j := 0x214; j < CL3EntrySize; j++ {
			b[rec+j] = byte(0x70 + i)
		}
		copy(b[coll+cursor:], payloads[i])
		cursor += len(payloads[i])
	}
	copy(b[coll+dataSize:], []byte("TAIL"))
	return b
}

func TestCL3ParseAndRebuildBothEndian(t *testing.T) {
	for _, big := range []bool{false, true} {
		raw := synthCL3(t, big)
		c, err := ParseCL3(raw)
		if err != nil {
			t.Fatal(err)
		}
		if c.BigEndian != big || c.MagicOffset != 0x10 || len(c.Files) != 2 || c.Files[1].Name != "two.bin" {
			t.Fatalf("parse=%+v", c)
		}
		p, err := c.FileData(0)
		if err != nil || string(p) != "AAA" {
			t.Fatalf("file0=%q err=%v", p, err)
		}
		out, err := c.Rebuild(map[int][]byte{0: []byte("HELLO-LONGER")})
		if err != nil {
			t.Fatal(err)
		}
		c2, err := ParseCL3(out)
		if err != nil {
			t.Fatal(err)
		}
		p0, _ := c2.FileData(0)
		p1, _ := c2.FileData(1)
		if string(p0) != "HELLO-LONGER" || string(p1) != "BBBBB" {
			t.Fatalf("payloads=%q/%q", p0, p1)
		}
		// Reserved bytes in both FileEntry records must be verbatim preserved.
		for i := 0; i < 2; i++ {
			oldRec := 0xD0 + i*CL3EntrySize
			newRec := 0xD0 + i*CL3EntrySize
			if !bytes.Equal(raw[oldRec+0x214:oldRec+CL3EntrySize], out[newRec+0x214:newRec+CL3EntrySize]) {
				t.Fatalf("reserved bytes drift endian=%v file=%d", big, i)
			}
		}
		if !bytes.Equal(out[len(out)-4:], []byte("TAIL")) {
			t.Fatal("CL3 trailing data drift")
		}
	}
}
