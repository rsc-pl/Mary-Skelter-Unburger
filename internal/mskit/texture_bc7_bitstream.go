package mskit

import (
	"fmt"
	"math/bits"
)

// BC7BlockFields is the on-disk bitstream representation of one BC7 block.
// Endpoints contain raw quantized components, BEFORE p-bit application or
// endpoint expansion. The writer is lossless and does not choose endpoints,
// partitions, or indices; that search is a separate, still-open encoder task.
//
// Component order is R,G,B,A. Endpoint ordering follows the BC7 specification;
// index arrays are row-major. PBits are in on-disk order (mode 1 has 2 shared
// per-subset pbits; modes 0/3/7 have one for each endpoint).
type BC7BlockFields struct {
	Mode         int
	Partition    int
	Rotation     int
	IndexMode    int
	Endpoints    [6][4]uint8
	PBits        [6]uint8
	ColorIndices [16]uint8
	AlphaIndices [16]uint8
}

type bc7Writer struct {
	data [16]byte
	pos  int
}

func (w *bc7Writer) put(v, n int) error {
	if n < 0 || n > 8 || w.pos+n > 128 || v < 0 || v >= 1<<uint(n) {
		return fmt.Errorf("BC7 bitstream invalid value %d for %d bits at %d", v, n, w.pos)
	}
	for k := 0; k < n; k++ {
		if v&(1<<uint(k)) != 0 {
			w.data[(w.pos+k)/8] |= byte(1 << uint((w.pos+k)%8))
		}
	}
	w.pos += n
	return nil
}

// UnpackBC7Block parses *all eight* block modes, including anchor-index
// reductions. It rejects the reserved all-zero block prefix and truncation.
func UnpackBC7Block(block []byte) (BC7BlockFields, error) {
	var f BC7BlockFields
	if len(block) != 16 || block[0] == 0 {
		return f, fmt.Errorf("BC7 block requires 16 bytes and a valid mode prefix")
	}
	f.Mode = bits.TrailingZeros8(block[0])
	r := bc7Bits{d: block}
	if r.read(f.Mode+1) != 1<<uint(f.Mode) {
		return f, fmt.Errorf("BC7 block invalid mode prefix")
	}
	readEP := func(count, components, n int) {
		for c := 0; c < components; c++ {
			for e := 0; e < count; e++ {
				f.Endpoints[e][c] = uint8(r.read(n))
			}
		}
	}
	readPI := func(count int) {
		for e := 0; e < count; e++ {
			f.PBits[e] = uint8(r.read(1))
		}
	}
	readIndices := func(dst *[16]uint8, n int, anchor func(int) bool) {
		for i := 0; i < 16; i++ {
			k := n
			if anchor(i) {
				k--
			}
			dst[i] = uint8(r.read(k))
		}
	}
	switch f.Mode {
	case 0, 2:
		partitionBits, indexBits, endpointBits, pbits := 6, 2, 5, 0
		if f.Mode == 0 {
			partitionBits, indexBits, endpointBits, pbits = 4, 3, 4, 6
		}
		f.Partition = r.read(partitionBits)
		readEP(6, 3, endpointBits)
		readPI(pbits)
		readIndices(&f.ColorIndices, indexBits, func(i int) bool {
			return i == 0 || i == int(bc7Anchor3a[f.Partition]) || i == int(bc7Anchor3b[f.Partition])
		})
	case 1, 3, 7:
		f.Partition = r.read(6)
		components, indexBits, endpointBits, pbits := 3, 2, 7, 4
		if f.Mode == 1 {
			indexBits, endpointBits, pbits = 3, 6, 2
		}
		if f.Mode == 7 {
			components, endpointBits = 4, 5
		}
		readEP(4, components, endpointBits)
		readPI(pbits)
		readIndices(&f.ColorIndices, indexBits, func(i int) bool { return i == 0 || i == int(bc7Anchor2[f.Partition]) })
	case 4, 5:
		f.Rotation = r.read(2)
		if f.Mode == 4 {
			f.IndexMode = r.read(1)
		}
		eb, ab, aw := 7, 8, 2
		if f.Mode == 4 {
			eb, ab, aw = 5, 6, 3
		}
		for c := 0; c < 4; c++ {
			n := eb
			if c == 3 {
				n = ab
			}
			for e := 0; e < 2; e++ {
				f.Endpoints[e][c] = uint8(r.read(n))
			}
		}
		if f.IndexMode == 0 {
			readIndices(&f.ColorIndices, 2, func(i int) bool { return i == 0 })
			readIndices(&f.AlphaIndices, aw, func(i int) bool { return i == 0 })
		} else {
			readIndices(&f.AlphaIndices, 2, func(i int) bool { return i == 0 })
			readIndices(&f.ColorIndices, aw, func(i int) bool { return i == 0 })
		}
	case 6:
		readEP(2, 4, 7)
		readPI(2)
		readIndices(&f.ColorIndices, 4, func(i int) bool { return i == 0 })
	default:
		return f, fmt.Errorf("BC7 unsupported mode %d", f.Mode)
	}
	if r.pos != 128 {
		return f, fmt.Errorf("BC7 mode %d consumed %d/128 bits", f.Mode, r.pos)
	}
	return f, nil
}

// PackBC7Block serializes the exact canonical BC7 bitstream. Every field is
// validated against its mode-specific width. It does NOT perform compression.
func PackBC7Block(f BC7BlockFields) ([16]byte, error) {
	var w bc7Writer
	if f.Mode < 0 || f.Mode > 7 {
		return w.data, fmt.Errorf("BC7 unsupported mode %d", f.Mode)
	}
	put := func(v, n int) error { return w.put(v, n) }
	if err := put(1<<uint(f.Mode), f.Mode+1); err != nil {
		return w.data, err
	}
	putEP := func(count, comp, n int) error {
		for c := 0; c < comp; c++ {
			for e := 0; e < count; e++ {
				if err := put(int(f.Endpoints[e][c]), n); err != nil {
					return err
				}
			}
		}
		return nil
	}
	putPI := func(count int) error {
		for e := 0; e < count; e++ {
			if err := put(int(f.PBits[e]), 1); err != nil {
				return err
			}
		}
		return nil
	}
	putI := func(src [16]uint8, n int, anchor func(int) bool) error {
		for i := 0; i < 16; i++ {
			k := n
			if anchor(i) {
				k--
			}
			if err := put(int(src[i]), k); err != nil {
				return err
			}
		}
		return nil
	}
	var err error
	switch f.Mode {
	case 0, 2:
		partitionBits, indexBits, endpointBits, pbits := 6, 2, 5, 0
		if f.Mode == 0 {
			partitionBits, indexBits, endpointBits, pbits = 4, 3, 4, 6
		}
		if err = put(f.Partition, partitionBits); err == nil {
			err = putEP(6, 3, endpointBits)
		}
		if err == nil {
			err = putPI(pbits)
		}
		if err == nil && f.Partition >= 0 && f.Partition < 1<<uint(partitionBits) {
			err = putI(f.ColorIndices, indexBits, func(i int) bool {
				return i == 0 || i == int(bc7Anchor3a[f.Partition]) || i == int(bc7Anchor3b[f.Partition])
			})
		}
	case 1, 3, 7:
		components, indexBits, endpointBits, pbits := 3, 2, 7, 4
		if f.Mode == 1 {
			indexBits, endpointBits, pbits = 3, 6, 2
		}
		if f.Mode == 7 {
			components, endpointBits = 4, 5
		}
		if err = put(f.Partition, 6); err == nil {
			err = putEP(4, components, endpointBits)
		}
		if err == nil {
			err = putPI(pbits)
		}
		if err == nil && f.Partition >= 0 && f.Partition < 64 {
			err = putI(f.ColorIndices, indexBits, func(i int) bool { return i == 0 || i == int(bc7Anchor2[f.Partition]) })
		}
	case 4, 5:
		if err = put(f.Rotation, 2); err != nil {
			break
		}
		if f.Mode == 4 {
			if err = put(f.IndexMode, 1); err != nil {
				break
			}
		}
		if f.Mode == 5 && f.IndexMode != 0 {
			err = fmt.Errorf("BC7 mode 5 index selector must be 0")
			break
		}
		eb, ab, aw := 7, 8, 2
		if f.Mode == 4 {
			eb, ab, aw = 5, 6, 3
		}
		for c := 0; c < 4 && err == nil; c++ {
			n := eb
			if c == 3 {
				n = ab
			}
			for e := 0; e < 2; e++ {
				err = put(int(f.Endpoints[e][c]), n)
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			if f.IndexMode == 0 {
				err = putI(f.ColorIndices, 2, func(i int) bool { return i == 0 })
			} else {
				err = putI(f.AlphaIndices, 2, func(i int) bool { return i == 0 })
			}
		}
		if err == nil {
			if f.IndexMode == 0 {
				err = putI(f.AlphaIndices, aw, func(i int) bool { return i == 0 })
			} else {
				err = putI(f.ColorIndices, aw, func(i int) bool { return i == 0 })
			}
		}
	case 6:
		if err = putEP(2, 4, 7); err == nil {
			err = putPI(2)
		}
		if err == nil {
			err = putI(f.ColorIndices, 4, func(i int) bool { return i == 0 })
		}
	}
	if err != nil {
		return [16]byte{}, err
	}
	if w.pos != 128 {
		return [16]byte{}, fmt.Errorf("BC7 mode %d emitted %d/128 bits", f.Mode, w.pos)
	}
	return w.data, nil
}
