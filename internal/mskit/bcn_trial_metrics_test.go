package mskit

import "testing"

func TestBCNTrialStats(t *testing.T) {
	reference := []byte{100, 100, 100, 255, 255, 1, 2, 0, 1, 2, 3, 128, 0, 0, 0, 255}
	candidate := []byte{100, 100, 100, 255, 1, 9, 8, 0, 1, 2, 3, 126, 0, 0, 1, 255}
	s, ok := CompareBCNTrialRGBA(reference, candidate)
	if !ok || s.Pixels != 4 || s.ExactPixels != 1 || s.AlphaDifferentPixels != 1 || s.HiddenRGBDifferentPixels != 1 || s.VisibleDifferentPixels != 2 || s.MaxAlphaDifference != 2 || s.MaxCompositeDifference == 0 {
		t.Fatalf("stats %+v ok=%t", s, ok)
	}
	if _, ok = CompareBCNTrialRGBA(reference, candidate[:len(candidate)-1]); ok {
		t.Fatal("accepted truncated RGBA")
	}
	if _, ok = CompareBCNTrialRGBA(nil, nil); ok {
		t.Fatal("accepted empty RGBA")
	}
	if _, ok = CompareBCNTrialRGBA([]byte{1}, []byte{1}); ok {
		t.Fatal("accepted nonpixel RGBA")
	}
}
func TestBCNTouchedBlocks(t *testing.T) {
	a, b := make([]byte, 64), make([]byte, 64)
	b[16] = 1
	b[48] = 2
	c, p, ok := BCNTouchedBlocks(a, b, 8, 8)
	if !ok || c != 2 || p != 2 {
		t.Fatalf("got %d,%d,%t", c, p, ok)
	}
	if _, _, ok = BCNTouchedBlocks(a, b, 7, 8); ok {
		t.Fatal("accepted dimensions not multiple 4")
	}
	if _, _, ok = BCNTouchedBlocks(a, b[:63], 8, 8); ok {
		t.Fatal("accepted truncated blocks")
	}
}

// regression: hidden RGB differences do not become a visible or alpha
// blocker, but meaningful alpha/visible differences remain separately reported.
func TestBCNTrialCompositeTiers(t *testing.T) {
	ref := []byte{220, 40, 12, 0, 100, 80, 60, 255, 20, 30, 40, 128}
	sameVisible := []byte{1, 2, 3, 0, 100, 80, 60, 255, 20, 30, 40, 128}
	s, ok := CompareBCNTrialRGBA(ref, sameVisible)
	if !ok || s.HiddenRGBDifferentPixels != 1 || s.AlphaDifferentPixels != 0 || s.CompositeDifferentPixels != 0 || s.MaxCompositeDifference != 0 || s.SumCompositeAbsDifference != 0 || s.SumAlphaAbsDifference != 0 {
		t.Fatalf("hidden only: %+v %v", s, ok)
	}
	alphaDelta := append([]byte(nil), ref...)
	alphaDelta[11] = 120
	s, ok = CompareBCNTrialRGBA(ref, alphaDelta)
	if !ok || s.AlphaDifferentPixels != 1 || s.CompositeDifferentPixels != 1 || s.SumAlphaAbsDifference != 8 || s.SumCompositeAbsDifference == 0 {
		t.Fatalf("alpha: %+v %v", s, ok)
	}
	visibleRGB := append([]byte(nil), ref...)
	visibleRGB[4] = 101
	s, ok = CompareBCNTrialRGBA(ref, visibleRGB)
	if !ok || s.CompositeDifferentPixels != 1 || s.MaxCompositeDifference != 1 || s.SumAlphaAbsDifference != 0 {
		t.Fatalf("visible: %+v %v", s, ok)
	}
}
