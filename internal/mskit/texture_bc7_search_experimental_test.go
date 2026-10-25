package mskit

import (
	"bytes"
	"math/rand"
	"testing"
)

func bc7TestPixels(seed int64) [64]byte {
	var raw [64]byte
	r := rand.New(rand.NewSource(seed))
	for i := 0; i < 16; i++ {
		// Contrasting/text-like pixels: transparent, opaque and partly covered.
		switch i % 5 {
		case 0:
			raw[4*i] = 255
			raw[4*i+1] = 255
			raw[4*i+2] = 255
			raw[4*i+3] = 255
		case 1:
			raw[4*i] = 0
			raw[4*i+1] = 0
			raw[4*i+2] = 0
			raw[4*i+3] = 0
		default:
			for c := 0; c < 4; c++ {
				raw[4*i+c] = byte(r.Intn(256))
			}
		}
	}
	return raw
}

func TestBC7ExperimentalFixedLayoutAllModes(t *testing.T) {
	cases := []BC7SearchLayout{
		{Mode: 0, Partition: 3}, {Mode: 1, Partition: 9},
		{Mode: 2, Partition: 40}, {Mode: 3, Partition: 62},
		{Mode: 4, Rotation: 0, IndexMode: 0}, {Mode: 4, Rotation: 3, IndexMode: 1},
		{Mode: 5, Rotation: 1}, {Mode: 6}, {Mode: 7, Partition: 52},
	}
	raw := bc7TestPixels(0xBC7)
	for _, layout := range cases {
		block, score, err := RefineBC7FixedLayout(raw, layout, 3)
		if err != nil {
			t.Fatalf("layout %+v: %v", layout, err)
		}
		f, err := UnpackBC7Block(block[:])
		if err != nil {
			t.Fatal(err)
		}
		if f.Mode != layout.Mode || f.Partition != layout.Partition || f.Rotation != layout.Rotation || f.IndexMode != layout.IndexMode {
			t.Fatalf("layout %+v roundtrip %+v", layout, f)
		}
		rgba, mode, err := DecodeBC7Block(block[:])
		if err != nil || mode != layout.Mode {
			t.Fatalf("decoder layout %+v: %v", layout, err)
		}
		var verified uint64
		for i := 0; i < 64; i++ {
			d := int(raw[i]) - int(rgba[i])
			verified += uint64(d * d)
		}
		if score != verified {
			t.Fatalf("layout %+v score %d != RGBA %d", layout, score, verified)
		}
		// Packed bitstream needs to survive independent unpack and repack.
		repacked, err := PackBC7Block(f)
		if err != nil || !bytes.Equal(block[:], repacked[:]) {
			t.Fatalf("layout %+v unstable bitstream %v", layout, err)
		}
		t.Logf("layout %+v SSE=%d", layout, score)
	}
}

func TestBC7ExperimentalSolidAndSearch(t *testing.T) {
	for _, value := range []byte{0, 255} {
		var rgba [64]byte
		for i := range rgba {
			rgba[i] = value
		}
		block, score, err := RefineBC7FixedLayout(rgba, BC7SearchLayout{Mode: 6}, 3)
		if err != nil || score != 0 {
			t.Fatalf("solid %d mode6 must be exact: score=%d err=%v block=%x", value, score, err, block)
		}
	}
	rgba := bc7TestPixels(77)
	layouts := []BC7SearchLayout{{Mode: 4}, {Mode: 4, IndexMode: 1}, {Mode: 5}, {Mode: 6}, {Mode: 7, Partition: 5}}
	a, selected, score, err := SearchBC7Experimental(rgba, layouts, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, selected2, score2, err := SearchBC7Experimental(rgba, layouts, 2)
	if err != nil || a != b || selected != selected2 || score != score2 {
		t.Fatal("nondeterministic BC7 search")
	}
	for _, layout := range layouts {
		_, trial, err := RefineBC7FixedLayout(rgba, layout, 2)
		if err != nil {
			t.Fatal(err)
		}
		if score > trial {
			t.Fatalf("candidate mode %d score %d worse than selected %d", layout.Mode, trial, score)
		}
	}
}

func TestBC7ExperimentalRejectsInvalidLayouts(t *testing.T) {
	rgba := bc7TestPixels(5)
	for _, layout := range []BC7SearchLayout{
		{Mode: -1}, {Mode: 8}, {Mode: 1, Partition: 64}, {Mode: 0, Partition: 16},
		{Mode: 4, IndexMode: 2}, {Mode: 6, Rotation: 1}, {Mode: 5, IndexMode: 1},
	} {
		if _, _, err := RefineBC7FixedLayout(rgba, layout, 2); err == nil {
			t.Fatalf("accepted invalid layout %+v", layout)
		}
	}
	if _, _, err := RefineBC7FixedLayout(rgba, BC7SearchLayout{Mode: 6}, 0); err == nil {
		t.Fatal("accepted zero iterations")
	}
	if _, _, _, err := SearchBC7Experimental(rgba, nil, 2); err == nil {
		t.Fatal("accepted empty search")
	}
}
