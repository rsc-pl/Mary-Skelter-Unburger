package graphicsgo

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCFFPrepositionedHintInsertionIndependentOracle(t *testing.T) {
	b, err := os.ReadFile(privateFixturePath(t, "oracle-hintmap-insert"))
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Name    string `json:"name"`
			Initial bool   `json:"initial"`
			Hints   []struct {
				Bottom *CFFHintEdgeFixedExperimental `json:"bottom"`
				Top    *CFFHintEdgeFixedExperimental `json:"top"`
			} `json:"hints"`
			Expected      []CFFHintEdgeFixedExperimental `json:"expected"`
			SampleY       []int64                        `json:"sample_y"`
			SampleResults []int64                        `json:"sample_results"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Cases) != 388 {
		t.Fatalf("expected 388 independent cases, got %d", len(corpus.Cases))
	}
	for _, tc := range corpus.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			h := make([]CFFPrepositionedHintExperimental, 0, len(tc.Hints))
			for _, item := range tc.Hints {
				h = append(h, CFFPrepositionedHintExperimental{Bottom: item.Bottom, Top: item.Top})
			}
			var original []CFFPrepositionedHintExperimental
			for _, hint := range h {
				duplicate := CFFPrepositionedHintExperimental{}
				if hint.Bottom != nil {
					x := *hint.Bottom
					duplicate.Bottom = &x
				}
				if hint.Top != nil {
					x := *hint.Top
					duplicate.Top = &x
				}
				original = append(original, duplicate)
			}
			got, err := BuildCFFPrepositionedHintMapExperimental(65536, h, tc.Initial)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.edges, tc.Expected) && !(len(got.edges) == 0 && len(tc.Expected) == 0) {
				t.Fatalf("mismatch: got %+v, expected %+v", got.edges, tc.Expected)
			}
			if !reflect.DeepEqual(h, original) && !(len(h) == 0 && len(original) == 0) {
				t.Fatal("mutated input")
			}
			if len(tc.SampleY) != len(tc.SampleResults) || len(tc.SampleY) < 3 {
				t.Fatal("missing independent mapped sample")
			}
			for i, y := range tc.SampleY {
				v, err := got.MapY(y)
				if err != nil || v != tc.SampleResults[i] {
					t.Fatalf("mapped y=%d got=%d expected=%d err=%v", y, v, tc.SampleResults[i], err)
				}
			}
			if got != nil {
				for _, e := range got.edges {
					if val, err := got.MapY(e.CharacterY); err != nil || val != e.DeviceY {
						// Duplicate source coords intentionally map to last edge.
						last := e.DeviceY
						for _, ee := range got.edges {
							if ee.CharacterY == e.CharacterY {
								last = ee.DeviceY
							}
						}
						if err != nil || val != last {
							t.Fatalf("edge map mismatch %v: %d %v", e, val, err)
						}
					}
				}
			}
		})
	}
}

func TestCFFPrepositionedHintRejectMalformed(t *testing.T) {
	x := &CFFHintEdgeFixedExperimental{CharacterY: 65536, DeviceY: 65536}
	y := &CFFHintEdgeFixedExperimental{CharacterY: 0, DeviceY: 0}
	bad := []CFFPrepositionedHintExperimental{
		{}, {Bottom: x, Top: y},
		{Bottom: &CFFHintEdgeFixedExperimental{CharacterY: 1 << 33}},
		{Bottom: &CFFHintEdgeFixedExperimental{DeviceY: -1 << 33}},
	}
	for _, h := range bad {
		if _, err := BuildCFFPrepositionedHintMapExperimental(65536, []CFFPrepositionedHintExperimental{h}, false); err == nil {
			t.Fatalf("accepted %+v", h)
		}
	}
	if _, err := BuildCFFPrepositionedHintMapExperimental(0, nil, false); err == nil {
		t.Fatal("accepted zero scale")
	}
	tooMany := make([]CFFPrepositionedHintExperimental, 97)
	if _, err := BuildCFFPrepositionedHintMapExperimental(65536, tooMany, false); err == nil {
		t.Fatal("accepted 97 hints")
	}
}
