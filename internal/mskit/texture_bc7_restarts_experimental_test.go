package mskit

import "testing"

func TestPremultipliedRestartValidAndNonregressing(t *testing.T) {
	for _, seed := range []int64{2, 19} {
		target := bc7TestPixels(seed)
		layouts := []BC7SearchLayout{{Mode: 6}, {Mode: 5}, {Mode: 5, Rotation: 1}, {Mode: 7, Partition: 12}, {Mode: 7, Partition: 44}, {Mode: 1, Partition: 7}}
		old, _, _, err := SearchBC7EdgePreservingExperimental(target, layouts, 2)
		if err != nil {
			t.Fatal(err)
		}
		_, qOld, err := ImproveBC7CompositeExperimental(target, old, 2)
		if err != nil {
			t.Fatal(err)
		}
		result, q, err := SearchBC7CompositeRestartExperimental(target, layouts, 2, 2, 2)
		if err != nil || q > qOld {
			t.Fatalf("seed=%d start=%d restart=%d err=%v", seed, qOld, q, err)
		}
		_, mode, err := DecodeBC7Block(result[:])
		if err != nil || mode < 0 || mode > 7 {
			t.Fatalf("invalid result %d: %v", mode, err)
		}
		again, q2, err := SearchBC7CompositeRestartExperimental(target, layouts, 2, 2, 2)
		if err != nil || q2 != q || again != result {
			t.Fatalf("nondeterministic restart %d: %v", seed, err)
		}
	}
}
func TestPremultipliedRestartRejectsInvalid(t *testing.T) {
	pixels := bc7TestPixels(8)
	layouts := []BC7SearchLayout{{Mode: 6}}
	for _, args := range [][3]int{{0, 2, 1}, {33, 2, 1}, {2, 0, 1}, {2, 9, 1}, {2, 2, 0}, {2, 2, 5}} {
		if _, _, err := SearchBC7CompositeRestartExperimental(pixels, layouts, args[0], args[1], args[2]); err == nil {
			t.Fatalf("accepted %+v", args)
		}
	}
	if _, _, err := SearchBC7CompositeRestartExperimental(pixels, []BC7SearchLayout{{Mode: 5, Partition: 9}}, 2, 2, 1); err == nil {
		t.Fatal("accepted invalid layout")
	}
}
