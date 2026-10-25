package mskit

import (
	"bytes"
	"fmt"
	"sort"
)

// GBNLTextEdit is one verified field target in the native Switch container
// hierarchy. CL3Index=-1 means a directly stored GBNL; BlobOffset=-1 means a
// direct CL3 member, otherwise it addresses an STCM2 Data item.
type GBNLTextEdit struct {
	CL3Index   int
	BlobOffset int
	Message    int
	Field      int
	Old        []byte
	New        []byte
	RecordKey  string
}

func RebuildGBNLTree(entry []byte, edits []GBNLTextEdit, encode CanonicalBILZEncoder) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), entry...), nil
	}
	h, e := FindBILZHeader(entry, 0, 0x10000)
	if e != nil {
		return nil, e
	}
	payload := append([]byte(nil), entry...)
	if h != nil {
		if h.PayloadOffset()+h.CSize > len(entry) {
			return nil, fmt.Errorf("GBNL BILZ bounds invalid")
		}
		payload, e = InflateBILZ(entry[h.PayloadOffset():h.PayloadOffset()+h.CSize], h.Algo)
		if e != nil {
			return nil, e
		}
		if len(payload) != h.USize {
			return nil, fmt.Errorf("GBNL BILZ decoded size mismatch")
		}
	}
	byCl := map[int]map[int][]GBNLTextEdit{}
	seen := map[string]bool{}
	for _, ed := range edits {
		if ed.CL3Index < -1 || ed.BlobOffset < -1 {
			return nil, fmt.Errorf("invalid GBNL location %s", ed.RecordKey)
		}
		if ed.CL3Index == -1 && ed.BlobOffset != -1 {
			return nil, fmt.Errorf("bare GBNL cannot have STCM blob offset")
		}
		id := fmt.Sprintf("%d/%d/%d/%d", ed.CL3Index, ed.BlobOffset, ed.Message, ed.Field)
		if seen[id] {
			return nil, fmt.Errorf("duplicate GBNL target %s", id)
		}
		seen[id] = true
		if byCl[ed.CL3Index] == nil {
			byCl[ed.CL3Index] = map[int][]GBNLTextEdit{}
		}
		byCl[ed.CL3Index][ed.BlobOffset] = append(byCl[ed.CL3Index][ed.BlobOffset], ed)
	}
	patchGBNL := func(blob []byte, grp []GBNLTextEdit) ([]byte, error) {
		g, e := ParseGBNL(blob)
		if e != nil {
			return nil, e
		}
		fields := map[TextFieldID][]byte{}
		for _, ed := range grp {
			fld, e := g.Field(ed.Message, ed.Field)
			if e != nil {
				return nil, fmt.Errorf("%s: %w", ed.RecordKey, e)
			}
			if fld.IsNull || !bytes.Equal(fld.StrRaw, ed.Old) {
				return nil, fmt.Errorf("%s: GBNL original bytes drift", ed.RecordKey)
			}
			fields[TextFieldID{Message: ed.Message, Field: ed.Field}] = ed.New
		}
		return RebuildGBNL(blob, fields)
	}
	if standalone, yes := byCl[-1]; yes {
		if len(byCl) != 1 || len(standalone) != 1 {
			return nil, fmt.Errorf("bare GBNL cannot mix other container routes")
		}
		grp, ok := standalone[-1]
		if !ok {
			return nil, fmt.Errorf("bare GBNL route missing blob -1")
		}
		payload, e = patchGBNL(payload, grp)
		if e != nil {
			return nil, e
		}
	} else {
		c, e := ParseCL3(payload)
		if e != nil {
			return nil, e
		}
		replacements := map[int][]byte{}
		clIndices := make([]int, 0, len(byCl))
		for id := range byCl {
			clIndices = append(clIndices, id)
		}
		sort.Ints(clIndices)
		for _, cl := range clIndices {
			inner, e := c.FileData(cl)
			if e != nil {
				return nil, e
			}
			route := byCl[cl]
			if len(inner) >= 5 && string(inner[:5]) == "STCM2" {
				stcm, e := ParseSTCM(inner)
				if e != nil {
					return nil, e
				}
				editsSTCM := map[int][]byte{}
				blobIDs := make([]int, 0, len(route))
				for k := range route {
					blobIDs = append(blobIDs, k)
				}
				sort.Ints(blobIDs)
				for _, off := range blobIDs {
					if off < 0 {
						return nil, fmt.Errorf("STCM2 location missing Data offset")
					}
					item, ok := stcm.DataItems[off]
					if !ok {
						return nil, fmt.Errorf("unreachable STCM2 GBNL item %#x", off)
					}
					edited, e := patchGBNL(item.Payload, route[off])
					if e != nil {
						return nil, e
					}
					if !bytes.Equal(edited, item.Payload) {
						editsSTCM[off] = edited
					}
				}
				if len(editsSTCM) > 0 {
					inner, e = RebuildSTCMRelayout(inner, editsSTCM)
					if e != nil {
						return nil, e
					}
				}
			} else {
				if len(route) != 1 {
					return nil, fmt.Errorf("direct CL3 GBNL expects one route")
				}
				grp, ok := route[-1]
				if !ok {
					return nil, fmt.Errorf("direct CL3 GBNL requires blob -1")
				}
				inner, e = patchGBNL(inner, grp)
				if e != nil {
					return nil, e
				}
			}
			replacements[cl] = inner
		}
		payload, e = c.Rebuild(replacements)
		if e != nil {
			return nil, e
		}
		check, e := ParseCL3(payload)
		if e != nil {
			return nil, fmt.Errorf("rebuilt CL3 invalid: %w", e)
		}
		for index, expected := range replacements {
			got, e := check.FileData(index)
			if e != nil {
				return nil, e
			}
			if !bytes.Equal(got, expected) {
				return nil, fmt.Errorf("CL3 member %d rebuild not preserved", index)
			}
		}
	}
	if h == nil {
		return payload, nil
	}
	if encode == nil {
		return nil, fmt.Errorf("GBNL compressed source requires BILZ encoder")
	}
	wrapper, e := encode(payload, h.Algo, 7)
	if e != nil {
		return nil, e
	}
	out := append(append(append([]byte(nil), entry[:h.Offset]...), wrapper...), entry[h.PayloadOffset()+h.CSize:]...)
	decoded, _, e := DecodeBILZWrapper(out[h.Offset:])
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(decoded, payload) {
		return nil, fmt.Errorf("GBNL nested BILZ roundtrip mismatch")
	}
	return out, nil
}
