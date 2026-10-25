package mskit

import (
	"bytes"
	"fmt"
	"strings"
)

// CanonicalBILZEncoder is the explicit compression boundary used when a GDAT
// entry is BILZ-wrapped. Stored entries can be rebuilt natively; zlib entries
// require the caller to provide the canonical encoder because Go's DEFLATE
// stream is not byte-identical to the project's zlib authority.
type CanonicalBILZEncoder func(data []byte, algo, level int) ([]byte, error)

// GraphicsTIDLocation records the archive/container location of one uniquely
// named texture member.
type GraphicsTIDLocation struct {
	GDATIndex int
	CL3Index  int
	InnerName string
	BILZ      bool
	BILZAlgo  int
	PrefixLen int
}

func decodeGraphicsGDATEntry(entry []byte) ([]byte, *BILZHeader, int, error) {
	h, err := FindBILZHeader(entry, 0, 0x10000)
	if err != nil {
		return nil, nil, 0, err
	}
	if h == nil {
		return append([]byte(nil), entry...), nil, 0, nil
	}
	if h.PayloadOffset()+h.CSize > len(entry) {
		return nil, nil, 0, fmt.Errorf("BILZ body exceeds GDAT entry")
	}
	payload, err := InflateBILZ(entry[h.PayloadOffset():h.PayloadOffset()+h.CSize], h.Algo)
	if err != nil {
		return nil, nil, 0, err
	}
	if len(payload) != h.USize {
		return nil, nil, 0, fmt.Errorf("BILZ decoded size %d, expected %d", len(payload), h.USize)
	}
	return payload, h, h.Offset, nil
}

func uniqueCL3FileFold(c CL3, name string) (CL3File, error) {
	var hit *CL3File
	for i := range c.Files {
		if strings.EqualFold(c.Files[i].Name, name) {
			if hit != nil {
				return CL3File{}, fmt.Errorf("expected one %s, found multiple", name)
			}
			f := c.Files[i]
			hit = &f
		}
	}
	if hit == nil {
		return CL3File{}, fmt.Errorf("expected one %s, found 0", name)
	}
	return *hit, nil
}

// ExtractGraphicsTID mirrors the canonical graphics backend's extract_tid():
// select one GDAT entry, decode its optional BILZ wrapper, locate exactly one
// CL3 member case-insensitively, and return that member byte-for-byte.
func ExtractGraphicsTID(dat []byte, gdatIndex int, innerName string) ([]byte, GraphicsTIDLocation, error) {
	g, err := ParseGDAT(dat)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	if gdatIndex < 0 || gdatIndex >= len(g.Entries) {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("GDAT index %d out of range", gdatIndex)
	}
	payload, h, prefix, err := decodeGraphicsGDATEntry(g.Entries[gdatIndex])
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d: %w", gdatIndex, err)
	}
	c, err := ParseCL3(payload)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d: %w", gdatIndex, err)
	}
	f, err := uniqueCL3FileFold(c, innerName)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d: %w", gdatIndex, err)
	}
	raw, err := c.FileData(f.Index)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	loc := GraphicsTIDLocation{GDATIndex: gdatIndex, CL3Index: f.Index, InnerName: innerName, PrefixLen: prefix}
	if h != nil {
		loc.BILZ = true
		loc.BILZAlgo = h.Algo
	}
	return append([]byte(nil), raw...), loc, nil
}

// PatchGraphicsTID mirrors the canonical graphics backend's patch_dat()
// container semantics. It validates TID metadata, changes only the selected CL3
// member, preserves all other CL3 members and GDAT entries byte-for-byte, and
// rebuilds the surrounding GDAT. For zlib-wrapped target entries an explicit
// canonical encoder is required; Go's stdlib encoder must not be substituted.
func PatchGraphicsTID(dat []byte, gdatIndex int, innerName string, newTID []byte, encode CanonicalBILZEncoder) ([]byte, GraphicsTIDLocation, error) {
	g, err := ParseGDAT(dat)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	if gdatIndex < 0 || gdatIndex >= len(g.Entries) {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("GDAT index %d out of range", gdatIndex)
	}
	originalEntry := g.Entries[gdatIndex]
	payload, h, prefix, err := decodeGraphicsGDATEntry(originalEntry)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d: %w", gdatIndex, err)
	}
	before, err := ParseCL3(payload)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d: %w", gdatIndex, err)
	}
	f, err := uniqueCL3FileFold(before, innerName)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d: %w", gdatIndex, err)
	}
	oldTID, err := before.FileData(f.Index)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	oldMeta, err := ParseTIDMeta(oldTID)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d/%s old TID: %w", gdatIndex, innerName, err)
	}
	newMeta, err := ParseTIDMeta(newTID)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d/%s new TID: %w", gdatIndex, innerName, err)
	}
	if oldMeta != newMeta {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d/%s: TID metadata changed", gdatIndex, innerName)
	}

	newPayload, err := before.Rebuild(map[int][]byte{f.Index: newTID})
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	after, err := ParseCL3(newPayload)
	if err != nil {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("rebuilt g%d CL3: %w", gdatIndex, err)
	}
	if len(before.Files) != len(after.Files) {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d/%s: CL3 file count changed", gdatIndex, innerName)
	}
	for i := range before.Files {
		bf, af := before.Files[i], after.Files[i]
		if bf.Name != af.Name {
			return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d/%s: CL3 name/order drift", gdatIndex, innerName)
		}
		if i == f.Index {
			continue
		}
		b, err := before.FileData(i)
		if err != nil {
			return nil, GraphicsTIDLocation{}, err
		}
		a, err := after.FileData(i)
		if err != nil {
			return nil, GraphicsTIDLocation{}, err
		}
		if !bytes.Equal(b, a) {
			return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d/%s: non-target CL3 member changed: %s", gdatIndex, innerName, bf.Name)
		}
	}

	rebuiltEntry := newPayload
	loc := GraphicsTIDLocation{GDATIndex: gdatIndex, CL3Index: f.Index, InnerName: innerName, PrefixLen: prefix}
	if h != nil {
		loc.BILZ = true
		loc.BILZAlgo = h.Algo
		var wrapped []byte
		switch h.Algo {
		case BILZAlgoStored:
			wrapped, err = WrapBILZGo(newPayload, BILZAlgoStored, 9)
		case BILZAlgoZlib:
			if encode == nil {
				return nil, GraphicsTIDLocation{}, fmt.Errorf("g%d/%s: canonical zlib BILZ encoder required", gdatIndex, innerName)
			}
			wrapped, err = encode(newPayload, h.Algo, 9)
		default:
			err = fmt.Errorf("unsupported BILZ algorithm %d", h.Algo)
		}
		if err != nil {
			return nil, GraphicsTIDLocation{}, err
		}
		rebuiltEntry = make([]byte, 0, prefix+len(wrapped))
		rebuiltEntry = append(rebuiltEntry, originalEntry[:prefix]...)
		rebuiltEntry = append(rebuiltEntry, wrapped...)
	}

	entries := make([][]byte, len(g.Entries))
	copy(entries, g.Entries)
	entries[gdatIndex] = rebuiltEntry
	out, err := BuildGDAT(entries)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	check, err := ParseGDAT(out)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	for i := range g.Entries {
		if i == gdatIndex {
			continue
		}
		if !bytes.Equal(g.Entries[i], check.Entries[i]) {
			return nil, GraphicsTIDLocation{}, fmt.Errorf("non-target GDAT entry changed: g%d", i)
		}
	}
	return out, loc, nil
}
