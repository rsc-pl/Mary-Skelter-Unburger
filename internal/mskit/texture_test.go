package mskit

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func fakeTID(t *testing.T, magic byte, w, h int, format string) []byte {
	t.Helper()
	n, err := TexturePayloadSize(w, h)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 0x80+n)
	copy(b[:4], []byte{'T', 'I', 'D', magic})
	if magic == 0x90 {
		binary.LittleEndian.PutUint32(b[0x44:0x48], uint32(w))
		binary.LittleEndian.PutUint32(b[0x48:0x4c], uint32(h))
		copy(b[0x64:0x68], []byte(format))
	} else {
		binary.BigEndian.PutUint32(b[0x44:0x48], uint32(w))
		binary.BigEndian.PutUint32(b[0x48:0x4c], uint32(h))
		r := []byte(format)
		for i := 0; i < 4; i++ {
			b[0x64+i] = r[3-i]
		}
	}
	for i := 0x80; i < len(b); i++ {
		b[i] = byte((i*37 + 11) & 255)
	}
	return b
}

func TestParseTIDMeta90And91(t *testing.T) {
	for _, tc := range []struct {
		magic     byte
		wantMagic string
	}{{0x90, "TID90"}, {0x91, "TID91"}} {
		b := fakeTID(t, tc.magic, 2048, 1024, "DXT5")
		m, err := ParseTIDMeta(b)
		if err != nil {
			t.Fatal(err)
		}
		if m.Width != 2048 || m.Height != 1024 || m.Format != "DXT5" || m.Magic != tc.wantMagic {
			t.Fatalf("meta = %+v", m)
		}
	}
}

func TestChangedTextureBlocksIgnoresInvisibleRGB(t *testing.T) {
	w, h := 8, 8
	before := make([]byte, w*h*4)
	after := append([]byte(nil), before...)
	// RGB-only drift in fully transparent block (0,0) is deliberately ignored.
	after[0], after[1], after[2] = 250, 19, 91
	// Alpha change in block (1,0) counts.
	o := (0*w + 5) * 4
	after[o], after[o+3] = 33, 17
	// Visible RGB change in block (0,1) counts.
	o = (5*w + 1) * 4
	before[o+3], after[o+3] = 255, 255
	after[o] = 77
	got, err := ChangedTextureBlocksRGBA(before, after, w, h)
	if err != nil {
		t.Fatal(err)
	}
	want := []TextureBlock{{X: 1, Y: 0}, {X: 0, Y: 1}}
	if len(got) != len(want) {
		t.Fatalf("blocks=%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("blocks=%v", got)
		}
	}
}

func TestEncodeBC3BlockCanonicalVectors(t *testing.T) {
	vectors := []struct{ rgba, want string }{
		{"000000000d1d07001a3a0e002757150034741c00419123004eae2a005bcb310068e8380075053f00822246008f3f4d009c5c5400a9795b00b6966200c3b36900", "00000000000000008dc5000000000000"},
		{"b428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ffb428d2ff", "ffff0000000000005bb159b1ffffffff"},
		{"00ff000011f4251322e94a2633de6f3944d3944c55c8b95f66bdde7277b2038588a72898999c4dabaa9172bebb8697d1cc7bbce4dd70e1f7ee65060aff5a2b1d", "f700f9ed96dc24e4dacbe165d46bafb0"},
		{"940f5901271a890e3b75f95f0c42c1bd155c1fe1e3e850163627312d2842ad98e2a7bd807d388b8828396ce2cc1309f734d83254cf7e05dc3c7f3ac88e16b4d9", "f70149a79fa46049a1aa18125c623ee2"},
	}
	for i, tc := range vectors {
		rgba, _ := hex.DecodeString(tc.rgba)
		want, _ := hex.DecodeString(tc.want)
		got, err := EncodeBC3Block(rgba)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("vector %d: %x != %x", i, got, want)
		}
	}
}

func TestRebuildTIDPreservesHeader(t *testing.T) {
	src := fakeTID(t, 0x91, 8, 8, "DXT5")
	payload := append([]byte(nil), src[0x80:]...)
	payload[0] ^= 0xff
	out, err := RebuildTID(src, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(src[:0x80], out[:0x80]) {
		t.Fatal("header changed")
	}
	if !bytes.Equal(payload, out[0x80:]) {
		t.Fatal("payload mismatch")
	}
}

func TestSpliceTextureBlocksOnlyTouchesSelectedBlocks(t *testing.T) {
	w, h := 8, 8
	n, _ := TexturePayloadSize(w, h)
	src := make([]byte, n)
	cand := make([]byte, n)
	for i := range src {
		src[i] = byte(i)
		cand[i] = byte(255 - i)
	}
	out, err := SpliceTextureBlocks(src, cand, w, h, []TextureBlock{{X: 1, Y: 0}, {X: 0, Y: 1}})
	if err != nil {
		t.Fatal(err)
	}
	for block := 0; block < 4; block++ {
		o := block * 16
		useCand := block == 1 || block == 2
		want := src[o : o+16]
		if useCand {
			want = cand[o : o+16]
		}
		if !bytes.Equal(out[o:o+16], want) {
			t.Fatalf("block %d drift", block)
		}
	}
}
