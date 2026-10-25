package mskit

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestLZO1XMatchesRoundtrip(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	patterned := []byte("MarySkelter2_Finale_UNBURGER_component")
	list := [][]byte{
		bytes.Repeat(patterned, 100),
		bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7}, 1000),
		append(bytes.Repeat([]byte("ABCDE"), 70), bytes.Repeat([]byte("FGHIJKLMNOP"), 35)...),
		bytes.Repeat([]byte{0}, 10000),
	}
	for _, n := range []int{4, 5, 9, 18, 19, 238, 239, 255, 256, 1000, 7000} {
		raw := make([]byte, n)
		rng.Read(raw)
		list = append(list, raw)
	}
	for i, raw := range list {
		got, e := CompressLZO1XMatches(raw)
		if e != nil {
			t.Fatalf("case %d encoder: %v", i, e)
		}
		decoded, e := DecompressLZO1X(got, len(raw))
		if e != nil || !bytes.Equal(decoded, raw) {
			t.Fatalf("case %d decoder: %v", i, e)
		}
		got2, e := CompressLZO1XMatches(raw)
		if e != nil || !bytes.Equal(got, got2) {
			t.Fatalf("case %d nondeterministic", i)
		}
	}
}
func TestLZO1XMatchesReduceComparedToLiterals(t *testing.T) {
	raw := bytes.Repeat([]byte("\x00Mary-Skelter Japanese Story Dialogue 1234\x00"), 90)
	literal, e := CompressLZO1XLiterals(raw)
	if e != nil {
		t.Fatal(e)
	}
	match, e := CompressLZO1XMatches(raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(match) >= len(literal)/4 {
		t.Fatalf("match encoding not smaller enough: %d vs %d", len(match), len(literal))
	}
}
