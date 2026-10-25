package graphicsgo

import "testing"

func TestRasterLatinRunSupersampledRejectsInvalid(t *testing.T) {
	cases := []struct {
		name, word                        string
		size, factor, samples, w, h, x, y int
	}{
		{"nil", "Test", 34, 4, 4, 640, 160, 24, 105},
		{"bad factor", "Test", 34, 3, 4, 640, 160, 24, 105},
		{"bad size", "Test", 0, 4, 4, 640, 160, 24, 105},
		{"bad sample", "Test", 34, 4, 16, 640, 160, 24, 105},
		{"oversized canvas", "Test", 34, 8, 4, 1024, 256, 24, 105},
		{"illegal text", "é", 34, 4, 4, 640, 160, 24, 105},
		{"long", "abcdefghijklmnopqrstuvwxyzabcdefgh", 34, 4, 4, 640, 160, 24, 105},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f *SFNT
			if _, err := f.RasterLatinRunSupersampled(tc.word, tc.size, tc.factor, tc.samples, tc.w, tc.h, tc.x, tc.y); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}
