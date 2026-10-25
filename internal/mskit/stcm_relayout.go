package mskit

// Strict Go-native STCM2 relayout: parse the complete physical instruction
// stream, including unreachable/dead code, before shifting a single byte.
// Every discovered absolute pointer is repointed and checked after the edit.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

type STCMReference struct {
	Word, Target int
	Kind         string
}
type stcmInsertion struct {
	at    int
	extra []byte
}

func STCMPhysicalReferences(data []byte) ([]STCMReference, error) {
	if len(data) < STCMHeaderSize || string(data[:5]) != "STCM2" {
		return nil, fmt.Errorf("STCM2 header missing")
	}
	var bo binary.ByteOrder
	switch data[5] {
	case 'L':
		bo = binary.LittleEndian
	case 'B':
		bo = binary.BigEndian
	default:
		return nil, fmt.Errorf("STCM endian invalid")
	}
	ref := []STCMReference{}
	add := func(at, target int, kind string) error {
		if at < 0 || at+4 > len(data) || target < 0 || target > len(data) {
			return fmt.Errorf("STCM %s pointer out of bounds: %#x -> %#x", kind, at, target)
		}
		ref = append(ref, STCMReference{at, target, kind})
		return nil
	}
	expOff := int(bo.Uint32(data[0x20:0x24]))
	count := int(bo.Uint32(data[0x24:0x28]))
	cl := int(bo.Uint32(data[0x2c:0x30]))
	if count < 0 || count > 100000 || expOff < 0 || count > (len(data)-expOff)/STCMExportSize {
		return nil, fmt.Errorf("STCM export table invalid")
	}
	if e := add(0x20, expOff, "header_export"); e != nil {
		return nil, e
	}
	if e := add(0x2c, cl, "header_collection"); e != nil {
		return nil, e
	}
	for i := 0; i < count; i++ {
		p := expOff + i*STCMExportSize + 0x24
		target := int(bo.Uint32(data[p : p+4]))
		if e := add(p, target, "export"); e != nil {
			return nil, e
		}
	}
	if cl > 0 && cl+0x40 <= len(data) {
		entries := int(bo.Uint32(data[cl+4 : cl+8]))
		n := int(bo.Uint32(data[cl+8 : cl+12]))
		if e := add(cl+4, entries, "collection_entries"); e != nil {
			return nil, e
		}
		if n > 0 {
			if n > 100000 || entries < 0 || n > (len(data)-entries)/0x20 {
				return nil, fmt.Errorf("STCM collection links invalid")
			}
			for i := 0; i < n; i++ {
				for _, d := range []int{0, 4} {
					p := entries + i*0x20 + d
					target := int(bo.Uint32(data[p : p+4]))
					if target != 0 {
						if target >= len(data) || bytes.IndexByte(data[target:], 0) < 0 {
							return nil, fmt.Errorf("STCM collection name invalid")
						}
						if e := add(p, target, "collection_name"); e != nil {
							return nil, e
						}
					}
				}
			}
		}
	}
	marker := []byte("EXPORT_DATA\x00")
	end := expOff - len(marker)
	if end < STCMHeaderSize || !bytes.Equal(data[end:expOff], marker) {
		end = bytes.LastIndex(data[:expOff], marker)
	}
	if end < STCMHeaderSize {
		return nil, fmt.Errorf("STCM physical EXPORT_DATA marker not found")
	}
	startMarker := []byte("CODE_START_\x00")
	startPos := bytes.LastIndex(data[STCMHeaderSize:end], startMarker)
	if startPos < 0 {
		return nil, fmt.Errorf("STCM physical CODE_START marker not found")
	}
	pos := STCMHeaderSize + startPos + len(startMarker)
	if pos > end {
		return nil, fmt.Errorf("STCM invalid code region")
	}
	for pos < end {
		if pos+STCMInstrHeaderSize > end {
			return nil, fmt.Errorf("STCM physical instruction truncated at %#x", pos)
		}
		isCall := bo.Uint32(data[pos : pos+4])
		opcode := bo.Uint32(data[pos+4 : pos+8])
		pc := int(bo.Uint32(data[pos+8 : pos+12]))
		sz := int(bo.Uint32(data[pos+12 : pos+16]))
		if pc > 16 || sz < STCMInstrHeaderSize+pc*STCMParamSize || sz > end-pos {
			return nil, fmt.Errorf("STCM unparsed physical code at %#x", pos)
		}
		if isCall != 0 {
			if e := add(pos+4, int(opcode), "call_target"); e != nil {
				return nil, e
			}
		}
		for i := 0; i < pc; i++ {
			p := pos + STCMInstrHeaderSize + i*STCMParamSize
			a := bo.Uint32(data[p : p+4])
			b := bo.Uint32(data[p+4 : p+8])
			c := bo.Uint32(data[p+8 : p+12])
			switch stcmTag(a) {
			case stcmTagMemOffset:
				if e := add(p, stcmValue(a), "mem_offset"); e != nil {
					return nil, e
				}
			case stcmTagIndirect:
				if stcmTag(c) == stcmTagMemOffset {
					if e := add(p+8, stcmValue(c), "indirect_mem"); e != nil {
						return nil, e
					}
				}
			case stcmTagSpecial:
				if a == stcmSpecialInstrPtr0 || a == stcmSpecialInstrPtr1 {
					if e := add(p+4, int(b), "instruction_ptr"); e != nil {
						return nil, e
					}
				} else if a == 0xffffff42 || a == 0xffffff43 {
					if e := add(p+4, int(b), "special_ptr"); e != nil {
						return nil, e
					}
				}
			default:
				if stcmTag(b) == stcmTagMemOffset {
					if e := add(p+4, stcmValue(b), "immediate_mem4"); e != nil {
						return nil, e
					}
				}
				if stcmTag(c) == stcmTagMemOffset {
					if e := add(p+8, stcmValue(c), "immediate_mem8"); e != nil {
						return nil, e
					}
				}
			}
		}
		pos += sz
	}
	if pos != end {
		return nil, fmt.Errorf("STCM physical code scan incomplete")
	}
	uniq := make([]STCMReference, 0, len(ref))
	seen := map[int]int{}
	for _, r := range ref {
		if x, ok := seen[r.Word]; ok {
			if x != r.Target {
				return nil, fmt.Errorf("STCM conflicting references at %#x", r.Word)
			}
			continue
		}
		seen[r.Word] = r.Target
		uniq = append(uniq, r)
	}
	return uniq, nil
}

func RebuildSTCMRelayout(raw []byte, edits map[int][]byte) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), raw...), nil
	}
	s, e := ParseSTCM(raw)
	if e != nil {
		return nil, e
	}
	bo := endian(s.BigEndian)
	indices := make([]int, 0, len(edits))
	for off := range edits {
		indices = append(indices, off)
	}
	sort.Ints(indices)
	insertions := []stcmInsertion{}
	for _, off := range indices {
		item, ok := s.DataItems[off]
		if !ok {
			return nil, fmt.Errorf("STCM Data item %#x not reachable", off)
		}
		if len(edits[off]) == 0 {
			return nil, fmt.Errorf("STCM empty replacement at %#x", off)
		}
		if len(edits[off]) > item.Length {
			insertions = append(insertions, stcmInsertion{at: off + STCMDataHeaderSize + item.Length, extra: append([]byte(nil), edits[off][item.Length:]...)})
		}
	}
	// The physical code reference inventory is REQUIRED before any growth. For
	// same-size/shrink cases, there is no relocation and no pointer adjustment.
	refs := []STCMReference{}
	if len(insertions) > 0 {
		refs, e = STCMPhysicalReferences(raw)
		if e != nil {
			return nil, fmt.Errorf("refusing unsafe STCM growth: %w", e)
		}
	}
	shifted := func(off int) int {
		n := off
		for _, in := range insertions {
			if in.at <= off {
				n += len(in.extra)
			}
		}
		return n
	}
	out := make([]byte, 0, len(raw))
	cursor := 0
	for _, in := range insertions {
		out = append(out, raw[cursor:in.at]...)
		out = append(out, in.extra...)
		cursor = in.at
	}
	out = append(out, raw[cursor:]...)
	for _, off := range indices {
		item := s.DataItems[off]
		next := shifted(off)
		bo.PutUint32(out[next:next+4], item.Type)
		bo.PutUint32(out[next+4:next+8], item.OffsetUnit)
		bo.PutUint32(out[next+8:next+12], item.Field8)
		bo.PutUint32(out[next+12:next+16], uint32(len(edits[off])))
		room := item.Length
		newBytes := edits[off]
		n := room
		if len(newBytes) < n {
			n = len(newBytes)
		}
		copy(out[next+STCMDataHeaderSize:next+STCMDataHeaderSize+n], newBytes[:n])
		if n < room {
			clear(out[next+STCMDataHeaderSize+n : next+STCMDataHeaderSize+room])
		}
	}
	for _, r := range refs {
		at := shifted(r.Word)
		newTarget := shifted(r.Target)
		if newTarget > 0xffffffff {
			return nil, fmt.Errorf("STCM relocated pointer overflow")
		}
		bo.PutUint32(out[at:at+4], uint32(newTarget))
	}
	for _, r := range refs {
		at := shifted(r.Word)
		if int(bo.Uint32(out[at:at+4])) != shifted(r.Target) {
			return nil, fmt.Errorf("STCM relocated pointer mismatch")
		}
	}
	check, e := ParseSTCM(out)
	if e != nil {
		return nil, fmt.Errorf("STCM relayout reparse: %w", e)
	}
	if len(check.Exports) != len(s.Exports) || len(check.DataItems) < len(s.DataItems) {
		return nil, fmt.Errorf("STCM structural regression after relayout")
	}
	for _, off := range indices {
		newOff := shifted(off)
		item, e := STCMDataAt(out, newOff)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(item.Payload, edits[off]) {
			return nil, fmt.Errorf("STCM edited Data item mismatch at %#x", off)
		}
	}
	if len(insertions) > 0 {
		if _, e = STCMPhysicalReferences(out); e != nil {
			return nil, fmt.Errorf("STCM physical code regression: %w", e)
		}
	}
	return out, nil
}
