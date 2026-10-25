package mskit

// Native graphics-container rewrite for separately approved/testable input.
// The Go zlib stream need not be byte-equal to system zlib. Unknown trailer
// bytes after a compressed BILZ payload are an UNSUPPORTED layout, not padding
// we may silently remove or shift.

import (
	"bytes"
	"fmt"
)

func PatchGraphicsTIDGoSafe(source []byte, gdatIndex int, innerName string, replacement []byte) ([]byte, GraphicsTIDLocation, error) {
	g, err := ParseGDAT(source)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	if gdatIndex < 0 || gdatIndex >= len(g.Entries) {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("GDAT index out of range")
	}
	oldEntry := g.Entries[gdatIndex]
	_, h, _, err := decodeGraphicsGDATEntry(oldEntry)
	if err != nil {
		return nil, GraphicsTIDLocation{}, err
	}
	if h != nil && h.PayloadOffset()+h.CSize != len(oldEntry) {
		return nil, GraphicsTIDLocation{}, fmt.Errorf("BILZ has unknown trailing bytes: native rewrite refused")
	}
	out, loc, err := PatchGraphicsTID(source, gdatIndex, innerName, replacement, WrapBILZGo)
	if err != nil {
		return nil, loc, err
	}
	after, err := ParseGDAT(out)
	if err != nil {
		return nil, loc, err
	}
	if len(after.Entries) != len(g.Entries) {
		return nil, loc, fmt.Errorf("GDAT entry count drift")
	}
	for i := range g.Entries {
		if i != gdatIndex && !bytes.Equal(g.Entries[i], after.Entries[i]) {
			return nil, loc, fmt.Errorf("unmodified entry g%d drift", i)
		}
	}
	if h != nil {
		_, nh, prefix, err := decodeGraphicsGDATEntry(after.Entries[gdatIndex])
		if err != nil || nh == nil || nh.Algo != h.Algo || prefix != h.Offset ||
			nh.PayloadOffset()+nh.CSize != len(after.Entries[gdatIndex]) ||
			!bytes.Equal(oldEntry[:h.Offset], after.Entries[gdatIndex][:h.Offset]) {
			return nil, loc, fmt.Errorf("BILZ rewrite wrapper/opaque prefix mismatch: %v", err)
		}
	}
	got, _, err := ExtractGraphicsTID(out, gdatIndex, innerName)
	if err != nil || !bytes.Equal(got, replacement) {
		return nil, loc, fmt.Errorf("TID post-rewrite verification failed: %v", err)
	}
	return out, loc, nil
}
