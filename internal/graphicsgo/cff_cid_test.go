package graphicsgo

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func cidTestDictInt(n int) []byte {
	b := []byte{29, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(b[1:], uint32(n))
	return b
}
func makeCIDCFF(format byte) []byte {
	head := []byte{1, 0, 4, 4}
	name := cffTestIndex([]byte("CID"))
	top := []byte{}
	top = append(top, 139, 139, 139, 12, 30) // ROS: registered, ordering, supplement
	charAt := len(top) + 1
	top = append(top, cidTestDictInt(0)...)
	top = append(top, 17)
	fdaAt := len(top) + 1
	top = append(top, cidTestDictInt(0)...)
	top = append(top, 12, 36)
	fdsAt := len(top) + 1
	top = append(top, cidTestDictInt(0)...)
	top = append(top, 12, 37)
	topIndex := cffTestIndex(top)
	strings := []byte{0, 0}
	global := []byte{0, 0}
	chars := cffTestIndex([]byte{14}, []byte{32, 10, 14}, []byte{32, 10, 14})
	// Each FD owns a unique local subroutine producing distinct contours.
	rect := cffTestGlyph([][2]int{{200, 0}, {200, 300}, {0, 300}, {0, 0}})
	tri := cffTestGlyph([][2]int{{100, 0}, {50, 200}, {0, 0}})
	rect[len(rect)-1] = 11
	tri[len(tri)-1] = 11
	sub0, sub1 := cffTestIndex(rect), cffTestIndex(tri)
	priv := []byte{141, 19} // Private /Subrs offset=2
	fd0 := append(append(cidTestDictInt(2), cidTestDictInt(0)...), 18)
	fd1 := append(append(cidTestDictInt(2), cidTestDictInt(0)...), 18)
	fdArray := cffTestIndex(fd0, fd1)
	sel := []byte{0, 0, 0, 1}
	if format == 3 {
		sel = []byte{3, 0, 2, 0, 0, 0, 0, 2, 1, 0, 3}
	}
	csOffset := len(head) + len(name) + len(topIndex) + len(strings) + len(global)
	fdOffset := csOffset + len(chars)
	selOffset := fdOffset + len(fdArray)
	priv0 := selOffset + len(sel)
	priv1 := priv0 + len(priv) + len(sub0)
	binary.BigEndian.PutUint32(top[charAt:], uint32(csOffset))
	binary.BigEndian.PutUint32(top[fdaAt:], uint32(fdOffset))
	binary.BigEndian.PutUint32(top[fdsAt:], uint32(selOffset))
	binary.BigEndian.PutUint32(fd0[6:], uint32(priv0))
	binary.BigEndian.PutUint32(fd1[6:], uint32(priv1))
	topIndex = cffTestIndex(top)
	fdArray = cffTestIndex(fd0, fd1)
	out := append([]byte{}, head...)
	out = append(out, name...)
	out = append(out, topIndex...)
	out = append(out, strings...)
	out = append(out, global...)
	out = append(out, chars...)
	out = append(out, fdArray...)
	out = append(out, sel...)
	out = append(out, priv...)
	out = append(out, sub0...)
	out = append(out, priv...)
	out = append(out, sub1...)
	return out
}
func fontWithCFF(data []byte) []byte {
	font := syntheticFont(true)
	result := append(append([]byte(nil), font...), data...)
	records := int(binary.BigEndian.Uint16(result[4:6]))
	for i := 0; i < records; i++ {
		at := 12 + i*16
		if string(result[at:at+4]) == "CFF " {
			binary.BigEndian.PutUint32(result[at+8:], uint32(len(font)))
			binary.BigEndian.PutUint32(result[at+12:], uint32(len(data)))
			return result
		}
	}
	panic("missing CFF")
}
func TestCFFCIDSelectionAndLocalSubrs(t *testing.T) {
	for _, format := range []byte{0, 3} {
		f, e := ParseSFNT(fontWithCFF(makeCIDCFF(format)))
		if e != nil {
			t.Fatalf("format %d SFNT: %v", format, e)
		}
		rect, e := f.GlyphOutlineCFF1(1)
		if e != nil {
			t.Fatalf("format %d FD0: %v", format, e)
		}
		tri, e := f.GlyphOutlineCFF1(2)
		if e != nil {
			t.Fatalf("format %d FD1: %v", format, e)
		}
		if len(rect) == 0 || len(tri) == 0 || len(rect[0]) == len(tri[0]) || bytes.Equal([]byte{byte(len(rect[0]))}, []byte{byte(len(tri[0]))}) {
			t.Fatalf("format %d FD-local contours not distinct", format)
		}
	}
}
func TestCFFFDSelectRejectsMalformed(t *testing.T) {
	for _, tt := range []struct {
		name        string
		data        []byte
		glyphs, fds int
	}{
		{"unknown_format", []byte{99, 0, 0}, 3, 2},
		{"bad_selector", []byte{0, 0, 2, 0}, 3, 2},
		{"truncated_zero", []byte{0, 0, 1}, 3, 2},
		{"bad_first", []byte{3, 0, 1, 0, 1, 0, 0, 3}, 3, 2},
		{"bad_sentinel", []byte{3, 0, 1, 0, 0, 0, 0, 2}, 3, 2},
		{"bad_fd", []byte{3, 0, 1, 0, 0, 2, 0, 3}, 3, 2},
		{"truncated_ranges", []byte{3, 0, 1, 0, 0, 1}, 3, 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, e := cffFDSelect(tt.data, 0, tt.glyphs, tt.fds); e == nil {
				t.Fatal("accepted invalid FDSelect")
			}
		})
	}
}
