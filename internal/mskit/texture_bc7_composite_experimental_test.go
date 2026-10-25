package mskit

import "testing"

func TestCompositeRefinementNeverRegresses(t *testing.T) {
	ls := []BC7SearchLayout{{Mode: 6}, {Mode: 5}, {Mode: 5, Rotation: 2}, {Mode: 7, Partition: 11}, {Mode: 1, Partition: 7}}
	for _, seed := range []int64{4, 13, 76} {
		pix := bc7TestPixels(seed)
		for _, layout := range ls {
			start, _, e := RefineBC7FixedLayout(pix, layout, 2)
			if e != nil {
				t.Fatal(e)
			}
			old, e := bc7EdgeCompositeObjective(pix, start)
			if e != nil {
				t.Fatal(e)
			}
			out, q, e := ImproveBC7CompositeExperimental(pix, start, 2)
			if e != nil || q > old {
				t.Fatalf("seed=%d mode=%d old=%d got=%d err=%v", seed, layout.Mode, old, q, e)
			}
			parsed, e := UnpackBC7Block(out[:])
			if e != nil {
				t.Fatal(e)
			}
			if parsed.Mode != layout.Mode || parsed.Partition != layout.Partition || parsed.Rotation != layout.Rotation {
				t.Fatalf("changed layout: %+v", parsed)
			}
			again, againQ, e := ImproveBC7CompositeExperimental(pix, start, 2)
			if e != nil || again != out || againQ != q {
				t.Fatalf("nondeterministic seed=%d mode=%d", seed, layout.Mode)
			}
		}
	}
}
func TestCompositeRefinementInvalidInputs(t *testing.T) {
	var empty [16]byte
	if _, _, err := ImproveBC7CompositeExperimental([64]byte{}, empty, 2); err == nil {
		t.Fatal("zero block accepted")
	}
	block, _, err := RefineBC7FixedLayout(bc7TestPixels(1), BC7SearchLayout{Mode: 6}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 9} {
		if _, _, err := ImproveBC7CompositeExperimental([64]byte{}, block, n); err == nil {
			t.Fatalf("invalid passes %d accepted", n)
		}
	}
}
