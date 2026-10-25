package mskit

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type GDATFileEntry struct {
	Offset int64
	Size   int
}

type GDATFile struct {
	f       *os.File
	path    string
	Entries []GDATFileEntry
	size    int64
}

func OpenGDATFile(path string) (*GDATFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*GDATFile, error) {
		_ = f.Close()
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	var head [GDATHeaderSize]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return fail(err)
	}
	if string(head[:4]) != "GDAT" {
		return fail(fmt.Errorf("not a GDAT archive"))
	}
	n := int(binary.LittleEndian.Uint32(head[4:8]))
	if n < 0 || n > 5_000_000 {
		return fail(fmt.Errorf("implausible GDAT entry count %d", n))
	}
	tableBytes := int64(GDATEntrySize * n)
	if int64(GDATHeaderSize)+tableBytes > st.Size() {
		return fail(fmt.Errorf("truncated GDAT table"))
	}
	table := make([]byte, int(tableBytes))
	if _, err := io.ReadFull(f, table); err != nil {
		return fail(err)
	}
	entries := make([]GDATFileEntry, n)
	prevEnd := int64(GDATHeaderSize + GDATEntrySize*n)
	for i := 0; i < n; i++ {
		p := i * GDATEntrySize
		off := int64(binary.LittleEndian.Uint32(table[p : p+4]))
		sz := int(binary.LittleEndian.Uint32(table[p+4 : p+8]))
		if off != prevEnd || off < 0 || sz < 0 || off+int64(sz) > st.Size() {
			return fail(fmt.Errorf("GDAT entry %d has invalid/non-contiguous bounds", i))
		}
		entries[i] = GDATFileEntry{Offset: off, Size: sz}
		prevEnd = off + int64(sz)
	}
	if n == 0 {
		if int64(GDATHeaderSize) != st.Size() {
			return fail(fmt.Errorf("zero-entry GDAT has trailing bytes"))
		}
	} else if prevEnd != st.Size() {
		return fail(fmt.Errorf("GDAT has trailing bytes"))
	}
	return &GDATFile{f: f, path: path, Entries: entries, size: st.Size()}, nil
}

func (g *GDATFile) Close() error {
	if g == nil || g.f == nil {
		return nil
	}
	return g.f.Close()
}

func (g *GDATFile) Count() int { return len(g.Entries) }

func (g *GDATFile) ReadEntry(index int) ([]byte, error) {
	if index < 0 || index >= len(g.Entries) {
		return nil, fmt.Errorf("GDAT index %d out of range", index)
	}
	e := g.Entries[index]
	buf := make([]byte, e.Size)
	if _, err := g.f.ReadAt(buf, e.Offset); err != nil {
		return nil, err
	}
	return buf, nil
}
