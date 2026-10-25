package mskit

// Go-native text rewrites. These primitives operate on isolated entries, never
// on the original archives. Unchanged entries are passed through byte-exactly.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
)

type TextFieldID struct{ Message, Field int }

func align16(n int) int { return (n + 15) &^ 15 }

// RebuildGBNL applies a field-keyed edit set while preserving shared string
// references. Existing shared strings are never overwritten: edited fields are
// repointed to unique appended/reused entries. Native footer placement is kept.
func RebuildGBNL(raw []byte, edits map[TextFieldID][]byte) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), raw...), nil
	}
	g, err := ParseGBNL(raw)
	if err != nil {
		return nil, err
	}
	bo := endian(g.BigEndian)
	out := append([]byte(nil), raw...)
	refcount := map[int]int{}
	stringsAt := map[int][]byte{}
	byString := map[string]int{}
	usedEnd := g.OffsetMsgs
	for m := 0; m < g.CountMsgs; m++ {
		for fi, it := range g.Items {
			if it.Kind != "str" {
				continue
			}
			f, e := g.Field(m, fi)
			if e != nil {
				return nil, e
			}
			if f.IsNull {
				continue
			}
			off := *f.StrOffset
			refcount[off]++
			stringsAt[off] = f.StrRaw
			abs := g.OffsetMsgs + off + len(f.StrRaw) + 1
			if abs > usedEnd {
				usedEnd = abs
			}
		}
	}
	keys := make([]int, 0, len(stringsAt))
	for k := range stringsAt {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		s := string(stringsAt[k])
		if _, ok := byString[s]; !ok {
			byString[s] = k
		}
	}
	ids := make([]TextFieldID, 0, len(edits))
	for id := range edits {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Message != ids[j].Message {
			return ids[i].Message < ids[j].Message
		}
		return ids[i].Field < ids[j].Field
	})
	appendData := []byte{}
	for _, id := range ids {
		b := edits[id]
		if bytes.IndexByte(b, 0) >= 0 {
			return nil, fmt.Errorf("GBNL %v: NUL replacement", id)
		}
		if id.Message < 0 || id.Message >= g.CountMsgs || id.Field < 0 || id.Field >= len(g.Items) {
			return nil, fmt.Errorf("GBNL %v: field out of bounds", id)
		}
		field, e := g.Field(id.Message, id.Field)
		if e != nil {
			return nil, e
		}
		pos := g.DescrOffset + id.Message*g.MsgDescrSize + field.Offset
		switch field.Kind {
		case "fixstr":
			if len(b)+1 > field.Size {
				return nil, fmt.Errorf("GBNL %v: fixed slot %d too small for %d", id, field.Size, len(b))
			}
			clear(out[pos : pos+field.Size])
			copy(out[pos:pos+field.Size], b)
		case "str":
			if field.IsNull {
				return nil, fmt.Errorf("GBNL %v: null string", id)
			}
			old := field.StrRaw
			if bytes.Equal(b, old) {
				continue
			}
			oldOff := *field.StrOffset
			if refcount[oldOff] == 1 && len(b) <= len(old) {
				// This original string ceases to exist. Remove its old
				// interning entry before later fields try to reuse it.
				// Otherwise a later edit requesting the former text could
				// accidentally be repointed to the overwritten bytes.
				if byString[string(old)] == oldOff {
					delete(byString, string(old))
				}
				if _, exists := byString[string(b)]; !exists {
					byString[string(b)] = oldOff
				}
				abs := g.OffsetMsgs + oldOff
				clear(out[abs : abs+len(old)+1])
				copy(out[abs:], b)
				continue
			}
			target, found := byString[string(b)]
			if !found {
				target = usedEnd + len(appendData) - g.OffsetMsgs
				if target < 0 || target > math.MaxUint32 {
					return nil, fmt.Errorf("GBNL string offset overflow")
				}
				byString[string(b)] = target
				appendData = append(appendData, b...)
				appendData = append(appendData, 0)
			}
			bo.PutUint32(out[pos:pos+4], uint32(target))
		default:
			return nil, fmt.Errorf("GBNL %v: not a string (%s)", id, field.Kind)
		}
	}
	if len(appendData) > 0 {
		end := len(out)
		if !g.GST {
			end = g.HeaderOffset
		}
		if usedEnd > end || usedEnd < 0 {
			return nil, fmt.Errorf("GBNL invalid string table end")
		}
		if usedEnd+len(appendData) <= end {
			for _, b := range out[usedEnd : usedEnd+len(appendData)] {
				if b != 0 {
					return nil, fmt.Errorf("GBNL append area not empty")
				}
			}
			copy(out[usedEnd:], appendData)
		} else {
			// Keep all data preceding the referenced string-table end; move
			// only the trailing 0x40 GBN footer, never relocate descriptors.
			for _, b := range out[usedEnd:end] {
				if b != 0 {
					return nil, fmt.Errorf("GBNL trailing region not safely growable")
				}
			}
			next := append(append([]byte(nil), out[:usedEnd]...), appendData...)
			next = append(next, make([]byte, align16(len(next))-len(next))...)
			if !g.GST {
				next = append(next, out[g.HeaderOffset:]...)
			}
			out = next
		}
	}
	check, err := ParseGBNL(out)
	if err != nil {
		return nil, fmt.Errorf("GBNL reparse: %w", err)
	}
	for m := 0; m < g.CountMsgs; m++ {
		for fi, it := range g.Items {
			if it.Kind != "str" && it.Kind != "fixstr" {
				continue
			}
			before, e := g.Field(m, fi)
			if e != nil {
				return nil, e
			}
			after, e := check.Field(m, fi)
			if e != nil {
				return nil, e
			}
			want, edited := edits[TextFieldID{m, fi}]
			if !edited {
				want = before.StrRaw
			}
			if before.IsNull != after.IsNull || (!before.IsNull && !bytes.Equal(want, after.StrRaw)) {
				return nil, fmt.Errorf("GBNL verification mismatch at %d/%d", m, fi)
			}
		}
	}
	return out, nil
}

// RebuildBINEntry regenerates native offset table and string pool. All old
// string bytes are checked before mutation and non-string table bytes survive.
func RebuildBINEntry(entry []byte, edits map[int][]byte, encode CanonicalBILZEncoder) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), entry...), nil
	}
	orig, e := ParseBINEntry(entry)
	if e != nil {
		return nil, e
	}
	strs, e := orig.Strings()
	if e != nil {
		return nil, e
	}
	for i, newText := range edits {
		if i < 0 || i >= len(strs) {
			return nil, fmt.Errorf("BIN string %d out of bounds", i)
		}
		if bytes.IndexByte(newText, 0) >= 0 {
			return nil, fmt.Errorf("BIN string %d replacement has NUL", i)
		}
		strs[i] = append([]byte(nil), newText...)
	}
	rebuilt := append([]byte(nil), orig.Payload[:orig.PoolStart]...)
	cursor := orig.PoolStart
	for i, s := range strs {
		binary.LittleEndian.PutUint64(rebuilt[0x10+i*16:0x18+i*16], uint64(cursor))
		rebuilt = append(rebuilt, s...)
		rebuilt = append(rebuilt, 0)
		cursor += len(s) + 1
	}
	result, e := rebuildOrochiEntry(entry, rebuilt, encode)
	if e != nil {
		return nil, e
	}
	check, e := ParseBINEntry(result)
	if e != nil {
		return nil, e
	}
	actual, e := check.Strings()
	if e != nil {
		return nil, e
	}
	if len(actual) != len(strs) {
		return nil, fmt.Errorf("BIN string count drift")
	}
	for i := range strs {
		if !bytes.Equal(actual[i], strs[i]) {
			return nil, fmt.Errorf("BIN string %d roundtrip mismatch", i)
		}
	}
	return result, nil
}

func rebuildOrochiEntry(entry, newPayload []byte, encode CanonicalBILZEncoder) ([]byte, error) {
	if len(entry) < OrochiBILZDataOffset {
		return nil, fmt.Errorf("Orochi entry truncated")
	}
	if encode == nil {
		return nil, fmt.Errorf("native BILZ encoder required")
	}
	algorithm := int(binary.BigEndian.Uint32(entry[0x8c:0x90]))
	wrapped, e := encode(newPayload, algorithm, 7)
	if e != nil {
		return nil, e
	}
	oldSize := int(binary.BigEndian.Uint32(entry[0x88:0x8c]))
	if oldSize < 0 || OrochiBILZDataOffset+oldSize > len(entry) {
		return nil, fmt.Errorf("Orochi original compressed size invalid")
	}
	out := append(append(append([]byte(nil), entry[:0x80]...), wrapped...), entry[0x90+oldSize:]...)
	parsed, e := DecodeOrochiEntry(out, OrochiWrapperTag(entry))
	if e != nil {
		return nil, e
	}
	if !bytes.Equal(parsed, newPayload) {
		return nil, fmt.Errorf("Orochi roundtrip mismatch")
	}
	return out, nil
}

// RebuildGBNLChain rewrites either a directly stored GBNL blob, a BILZ-wrapped
// GBNL blob, or a GBNL member in CL3. STCM2 containing GBNL is NOT handled by
// this function; callers must fail closed rather than patching at guessed offsets.
func RebuildGBNLChain(entry []byte, cl3Index int, fields map[TextFieldID][]byte, encode CanonicalBILZEncoder) ([]byte, error) {
	if len(fields) == 0 {
		return append([]byte(nil), entry...), nil
	}
	if cl3Index < -1 {
		return nil, fmt.Errorf("invalid CL3 index")
	}
	raw := append([]byte(nil), entry...)
	h, err := FindBILZHeader(entry, 0, 0x10000)
	if err != nil {
		return nil, err
	}
	var payload []byte
	if h != nil {
		if h.PayloadOffset()+h.CSize > len(entry) {
			return nil, fmt.Errorf("BILZ payload bounds invalid")
		}
		payload, err = InflateBILZ(entry[h.PayloadOffset():h.PayloadOffset()+h.CSize], h.Algo)
		if err != nil {
			return nil, err
		}
		if len(payload) != h.USize {
			return nil, fmt.Errorf("BILZ decoded size mismatch")
		}
	} else {
		payload = raw
	}
	var updated []byte
	if cl3Index == -1 {
		updated, err = RebuildGBNL(payload, fields)
	} else {
		var c CL3
		c, err = ParseCL3(payload)
		if err != nil {
			return nil, err
		}
		var inner []byte
		inner, err = c.FileData(cl3Index)
		if err != nil {
			return nil, err
		}
		if len(inner) >= 5 && string(inner[:5]) == "STCM2" {
			return nil, fmt.Errorf("STCM2 relayout not yet proven in native Go")
		}
		var rebuilt []byte
		rebuilt, err = RebuildGBNL(inner, fields)
		if err != nil {
			return nil, err
		}
		updated, err = c.Rebuild(map[int][]byte{cl3Index: rebuilt})
		if err == nil {
			var check CL3
			check, err = ParseCL3(updated)
			if err == nil {
				var got []byte
				got, err = check.FileData(cl3Index)
				if err == nil && !bytes.Equal(got, rebuilt) {
					err = fmt.Errorf("CL3 member verification failure")
				}
			}
		}
	}
	if err != nil {
		return nil, err
	}
	if h == nil {
		return updated, nil
	}
	if encode == nil {
		return nil, fmt.Errorf("BILZ encoder required")
	}
	newWrapper, err := encode(updated, h.Algo, 7)
	if err != nil {
		return nil, err
	}
	out := append(append(append([]byte(nil), entry[:h.Offset]...), newWrapper...), entry[h.PayloadOffset()+h.CSize:]...)
	check, _, err := DecodeBILZWrapper(out[h.Offset:])
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(check, updated) {
		return nil, fmt.Errorf("GBNL chain BILZ roundtrip failed")
	}
	return out, nil
}
