package maintainer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireMovieTools(t *testing.T) {
	t.Helper()
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(n); err != nil {
			t.Skipf("%s not available", n)
		}
	}
}

func ffmpegTest(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("ffmpeg", append([]string{"-y", "-v", "error"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, out)
	}
}

func makeTestMovie(t *testing.T, path string, freq int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegTest(t,
		"-f", "lavfi", "-i", "color=c=black:s=64x64:r=24:d=0.25",
		"-f", "lavfi", "-i", "sine=frequency="+itoa(freq)+":sample_rate=48000:duration=0.25",
		"-c:v", "mpeg4", "-q:v", "8", "-c:a", "aac", "-b:a", "64k", "-shortest", path)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	b := [20]byte{}
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func TestPrepareMovieAudioEvidenceAtomicAndFinalPaths(t *testing.T) {
	requireMovieTools(t)
	root := t.TempDir()
	ms2 := filepath.Join(root, "games", "ms2")
	finale := filepath.Join(root, "games", "finale")
	ms2en := filepath.Join(ms2, "masterWindows", "MOVIE_EN", "400004.mp4")
	ms2sub := filepath.Join(ms2, "masterWindows", "MOVIE_EN_SUB", "400004.mp4")
	makeTestMovie(t, ms2en, 440)
	makeTestMovie(t, ms2sub, 880)

	enDir := filepath.Join(root, ".local", "movie_audio_source_cache", "finale", "MOVIE_EN")
	jpDir := filepath.Join(root, ".local", "movie_audio_source_cache", "finale", "MOVIE_JP")
	en1, en2 := filepath.Join(enDir, "100001.mp4"), filepath.Join(enDir, "100002.mp4")
	makeTestMovie(t, en1, 440)
	if err := os.MkdirAll(jpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(en1); err != nil {
		t.Fatal(err)
	} else if err := os.WriteFile(filepath.Join(jpDir, "100001.mp4"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	makeTestMovie(t, en2, 440)
	// Pair 2 has the same encoded video but different JP audio.
	ffmpegTest(t, "-i", en2, "-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000:duration=0.25",
		"-map", "0:v:0", "-map", "1:a:0", "-c:v", "copy", "-c:a", "aac", "-b:a", "64k", "-shortest", filepath.Join(jpDir, "100002.mp4"))

	sum, err := PrepareMovieAudioEvidence(root, SourceRoots{MS2: ms2, Finale: finale}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.FinaleMoviePairs != 2 || sum.FinaleAudioStreamsIdentical != 1 || sum.FinaleAudioStreamsDifferent != 1 || sum.MovieAudioPayloadObjects != 1 {
		t.Fatalf("unexpected summary: %+v", sum)
	}
	manifestPath := filepath.Join(root, ".local", "movie_audio_cache", "MOVIE_AUDIO_PAYLOAD_INPUT.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), ".build.tmp") {
		t.Fatalf("temporary cache path leaked into final manifest: %s", raw)
	}
	var mf moviePayloadManifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		t.Fatal(err)
	}
	if len(mf.Objects) != 1 {
		t.Fatalf("payload objects=%d", len(mf.Objects))
	}
	payload := filepath.Join(root, filepath.FromSlash(mf.Objects[0].File))
	if st, err := os.Stat(payload); err != nil || st.Size() == 0 {
		t.Fatalf("final payload missing: %s: %v", payload, err)
	}

	before, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(ms2sub)); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareMovieAudioEvidence(root, SourceRoots{MS2: ms2, Finale: finale}, nil); err == nil {
		t.Fatal("incomplete MS2 source unexpectedly succeeded")
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed refresh replaced the last valid movie cache")
	}
}
