package mskit

import "testing"

// research-only behavioral guards. No pinned C bytes are used as a seed.
func TestAlphaWeightOnePreservesSearch(t *testing.T) {
	layouts := []BC7SearchLayout{{Mode: 6}, {Mode: 5}, {Mode: 5, Rotation: 2}, {Mode: 7, Partition: 11}, {Mode: 1, Partition: 5}}
	for _, seed := range []int64{1, 18, 77} {
		src := bc7TestPixels(seed)
		old, oldLayout, oldSSE, err := SearchBC7Experimental(src, layouts, 3)
		if err != nil {
			t.Fatal(err)
		}
		newBlock, newLayout, newSSE, err := SearchBC7AlphaWeightedExperimental(src, layouts, 3, 1)
		if err != nil || old != newBlock || oldLayout != newLayout || oldSSE != newSSE {
			t.Fatalf("weight=1 changed legacy search, seed %d", seed)
		}
	}
}

func TestEdgePolicyPreservesObjectiveAndValidBlocks(t *testing.T) {
	layouts := []BC7SearchLayout{{Mode: 6}, {Mode: 5}, {Mode: 5, Rotation: 2}, {Mode: 7, Partition: 11}}
	for _, seed := range []int64{8, 15, 22} {
		src := bc7TestPixels(seed)
		old, _, _, err := SearchBC7Experimental(src, layouts, 3)
		if err != nil {
			t.Fatal(err)
		}
		newBlock, layout, sse, err := SearchBC7EdgePreservingExperimental(src, layouts, 3)
		if err != nil {
			t.Fatal(err)
		}
		qOld, err := bc7EdgeCompositeObjective(src, old)
		if err != nil {
			t.Fatal(err)
		}
		qNew, err := bc7EdgeCompositeObjective(src, newBlock)
		if err != nil || qNew > qOld {
			t.Fatalf("seed %d composite/alpha objective worsened: %d > %d", seed, qNew, qOld)
		}
		unpacked, err := UnpackBC7Block(newBlock[:])
		if err != nil || unpacked.Mode != layout.Mode || unpacked.Partition != layout.Partition || unpacked.Rotation != layout.Rotation {
			t.Fatalf("invalid BC7 edge candidate for seed %d: %v", seed, err)
		}
		rgba, _, err := DecodeBC7Block(newBlock[:])
		if err != nil {
			t.Fatal(err)
		}
		var checked uint64
		for i := range src {
			d := int(src[i]) - int(rgba[i])
			checked += uint64(d * d)
		}
		if checked != sse {
			t.Fatalf("seed %d returned raw score %d != decoded %d", seed, sse, checked)
		}
		again, _, _, err := SearchBC7EdgePreservingExperimental(src, layouts, 3)
		if err != nil || again != newBlock {
			t.Fatalf("nondeterministic seed %d", seed)
		}
	}
}

func TestEdgePolicyRejectsBadLayouts(t *testing.T) {
	if _, _, _, err := SearchBC7EdgePreservingExperimental(bc7TestPixels(5), []BC7SearchLayout{{Mode: 99}}, 3); err == nil {
		t.Fatal("accepted invalid mode")
	}
	if _, _, err := RefineBC7FixedLayoutAlphaWeightedExperimental(bc7TestPixels(5), BC7SearchLayout{Mode: 6}, 3, 0); err == nil {
		t.Fatal("accepted weight zero")
	}
	if _, _, err := RefineBC7FixedLayoutAlphaWeightedExperimental(bc7TestPixels(5), BC7SearchLayout{Mode: 6}, 3, 33); err == nil {
		t.Fatal("accepted unbounded weight")
	}
}
