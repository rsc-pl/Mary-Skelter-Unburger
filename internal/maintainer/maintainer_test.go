package maintainer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestMovieCacheReadyRequiresCanonicalFinaleSet(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".local", "movie_audio_source_cache", "finale")
	for _, set := range []string{"MOVIE_EN", "MOVIE_JP"} {
		d := filepath.Join(base, set)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 22; i++ {
			if err := os.WriteFile(filepath.Join(d, fmt.Sprintf("%06d.mp4", i)), []byte{1}, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	cache := filepath.Join(root, ".local", "movie_audio_cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	s := MovieSummary{MS2Status: "ok", FinaleStatus: "ok", FinaleMoviePairs: 22, MovieAudioPayloadObjects: 18, Complete: true}
	raw, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(cache, "SUMMARY.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := MovieCacheReady(root); !ok {
		t.Fatal("canonical movie cache should be ready")
	}
	s.FinaleMoviePairs = 21
	raw, _ = json.Marshal(s)
	_ = os.WriteFile(filepath.Join(cache, "SUMMARY.json"), raw, 0o644)
	if _, ok := MovieCacheReady(root); ok {
		t.Fatal("21-pair movie cache must not be accepted")
	}
}
