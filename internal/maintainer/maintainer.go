package maintainer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"maryskelter-unburger/internal/updater"
)

const expectedFinaleMovies = 22

type Status struct {
	Root         string
	SourceConfig bool
	SourceCache  bool
	Dist         bool
	MovieCache   bool
	MoviePairs   int
	MovieObjects int
	Stage        bool
	Container    bool
	Key          bool
	Release      bool
	MissingTools []string
}

type SourceRoots struct {
	MS2    string
	Finale string
}

type MovieSummary struct {
	MS2Status                string `json:"ms2_status"`
	FinaleStatus             string `json:"finale_status"`
	FinaleMoviePairs         int    `json:"finale_movie_pairs"`
	MovieAudioPayloadObjects int    `json:"movie_audio_payload_objects"`
	Complete                 bool   `json:"complete"`
}

func FindRoot(start string) (string, error) {
	p, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if st, e := os.Stat(filepath.Join(p, "go.mod")); e == nil && st.Mode().IsRegular() {
			return p, nil
		}
		next := filepath.Dir(p)
		if next == p {
			return "", fmt.Errorf("could not find project go.mod above %s", start)
		}
		p = next
	}
}

func file(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func dir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func ReadMovieSummary(root string) (MovieSummary, error) {
	var s MovieSummary
	raw, err := os.ReadFile(filepath.Join(root, ".local", "movie_audio_cache", "SUMMARY.json"))
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, nil
}

func MovieCacheReady(root string) (MovieSummary, bool) {
	s, err := ReadMovieSummary(root)
	if err != nil {
		return s, false
	}
	ok := s.Complete && s.MS2Status == "ok" && s.FinaleStatus == "ok" &&
		s.FinaleMoviePairs == expectedFinaleMovies && s.MovieAudioPayloadObjects == 18 &&
		dir(filepath.Join(root, ".local", "movie_audio_source_cache", "finale", "MOVIE_EN")) &&
		dir(filepath.Join(root, ".local", "movie_audio_source_cache", "finale", "MOVIE_JP"))
	return s, ok
}

func Inspect(root string) Status {
	root, _ = filepath.Abs(root)
	s := Status{Root: root}
	s.SourceConfig = file(filepath.Join(root, ".local", "source_paths.sh"))
	s.SourceCache = file(filepath.Join(root, ".local", "source_cache", "cache.json"))
	s.Dist = dir(filepath.Join(root, ".local", "dist", "MS2_EN_FIDELITY_PATCH")) && dir(filepath.Join(root, ".local", "dist", "MSF_EN_FIDELITY_PATCH"))
	ms, ok := MovieCacheReady(root)
	s.MovieCache = ok
	s.MoviePairs = ms.FinaleMoviePairs
	s.MovieObjects = ms.MovieAudioPayloadObjects
	s.Stage = file(filepath.Join(root, ".local", "release_asset_stage", "ASSET_STAGE_MANIFEST.json"))
	s.Container = file(filepath.Join(root, ".local", "release", "mary-skelter.unburger-assets"))
	s.Key = file(filepath.Join(root, ".local", "secrets", "unburger-assets.key"))
	release := filepath.Join(root, ".local", "release", "end-user")
	s.Release = file(filepath.Join(release, "MarySkelterUnburger")) && file(filepath.Join(release, "MarySkelterUnburger.exe")) && file(filepath.Join(release, "mary-skelter.unburger-assets")) && file(filepath.Join(release, "SHA256SUMS.txt"))
	s.MissingTools = MissingTools([]string{"go", "ffmpeg", "ffprobe", "zstd"})
	return s
}

func MissingTools(names []string) []string {
	var out []string
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func run(root string, stdout, stderr io.Writer, stdin io.Reader, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = root
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Stdin = stdin
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func LoadSourceRoots(root string) (SourceRoots, error) {
	p, err := ReadSourcePaths(root)
	if err != nil {
		return SourceRoots{}, err
	}
	return SourceRoots{MS2: p.MS2Game, Finale: p.FinaleGame}, nil
}

func countMP4(path string) int {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			n++
		}
	}
	return n
}

func CurrentFinaleMovieCounts(root string) (en, jp int, err error) {
	roots, err := LoadSourceRoots(root)
	if err != nil {
		return 0, 0, err
	}
	mw := filepath.Join(roots.Finale, "masterWindows")
	return countMP4(filepath.Join(mw, "MOVIE_EN")), countMP4(filepath.Join(mw, "MOVIE_JP")), nil
}

func CachedFinaleMovieCounts(root string) (en, jp int) {
	base := filepath.Join(root, ".local", "movie_audio_source_cache", "finale")
	return countMP4(filepath.Join(base, "MOVIE_EN")), countMP4(filepath.Join(base, "MOVIE_JP"))
}

func copyMovieFile(src, dst string) error {
	if st, err := os.Stat(dst); err == nil && st.Mode().IsRegular() {
		if ss, e := os.Stat(src); e == nil && ss.Size() == st.Size() {
			a, ea := updater.SHA256File(src)
			b, eb := updater.SHA256File(dst)
			if ea == nil && eb == nil && a == b {
				return nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	if cp, err := exec.LookPath("cp"); err == nil {
		c := exec.Command(cp, "--reflink=auto", "--preserve=mode,timestamps", "--", src, tmp)
		if c.Run() == nil {
			return os.Rename(tmp, dst)
		}
		_ = os.Remove(tmp)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	cerr := out.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func CaptureFinaleMovieSet(root, set string, stdout io.Writer) (int, error) {
	if set != "MOVIE_EN" && set != "MOVIE_JP" {
		return 0, fmt.Errorf("unsupported Finale movie set %q", set)
	}
	roots, err := LoadSourceRoots(root)
	if err != nil {
		return 0, err
	}
	srcRoot := filepath.Join(roots.Finale, "masterWindows", set)
	if countMP4(srcRoot) != expectedFinaleMovies {
		return 0, fmt.Errorf("%s currently has %d MP4 files; expected %d", srcRoot, countMP4(srcRoot), expectedFinaleMovies)
	}
	dstRoot := filepath.Join(root, ".local", "movie_audio_source_cache", "finale", set)
	if err := os.MkdirAll(filepath.Dir(dstRoot), 0o755); err != nil {
		return 0, err
	}
	tmpRoot := dstRoot + ".capture.tmp"
	_ = os.RemoveAll(tmpRoot)
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmpRoot)
		}
	}()
	entries, err := os.ReadDir(srcRoot)
	if err != nil {
		return 0, err
	}
	copied := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			continue
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "  cache %-8s %s\n", set, e.Name())
		}
		if err := copyMovieFile(filepath.Join(srcRoot, e.Name()), filepath.Join(tmpRoot, e.Name())); err != nil {
			return copied, err
		}
		copied++
	}
	if copied != expectedFinaleMovies {
		return copied, fmt.Errorf("captured %d %s movies; expected %d", copied, set, expectedFinaleMovies)
	}
	oldRoot := dstRoot + ".capture.previous"
	_ = os.RemoveAll(oldRoot)
	if _, err := os.Stat(dstRoot); err == nil {
		if err := os.Rename(dstRoot, oldRoot); err != nil {
			return copied, err
		}
	}
	if err := os.Rename(tmpRoot, dstRoot); err != nil {
		_ = os.Rename(oldRoot, dstRoot)
		return copied, err
	}
	committed = true
	_ = os.RemoveAll(oldRoot)
	return copied, nil
}

func RefreshMovieAudio(root string, stdout, stderr io.Writer) error {
	roots, err := LoadSourceRoots(root)
	if err != nil {
		return err
	}
	en, jp := CachedFinaleMovieCounts(root)
	if en != expectedFinaleMovies || jp != expectedFinaleMovies {
		return fmt.Errorf("Finale movie source cache incomplete: MOVIE_EN=%d/%d MOVIE_JP=%d/%d; use the guided movie-source step first", en, expectedFinaleMovies, jp, expectedFinaleMovies)
	}
	if countMP4(filepath.Join(roots.MS2, "masterWindows", "MOVIE_EN")) == 0 || countMP4(filepath.Join(roots.MS2, "masterWindows", "MOVIE_EN_SUB")) == 0 {
		return fmt.Errorf("MS2 pristine movie audit source is incomplete; MOVIE_EN and MOVIE_EN_SUB must be present when refreshing movie evidence")
	}
	if _, err := PrepareMovieAudioEvidence(root, roots, stdout); err != nil {
		return err
	}
	if summary, ok := MovieCacheReady(root); !ok {
		return fmt.Errorf("movie refresh completed but canonical cache gate failed: MS2=%s Finale=%s pairs=%d payload=%d", summary.MS2Status, summary.FinaleStatus, summary.FinaleMoviePairs, summary.MovieAudioPayloadObjects)
	}
	return nil
}

func PrepareReleaseAssets(root string, refreshMovies bool, stdout, stderr io.Writer) error {
	sourceCache := filepath.Join(root, ".local", "source_cache")
	bundleRoot := filepath.Join(root, ".local", "dist")
	gdatOut := filepath.Join(root, ".local", "gdat_payload")
	if err := os.RemoveAll(gdatOut); err != nil {
		return err
	}
	lineage, err := updater.LoadContentLineage(filepath.Join(root, "config", "content_lineage.json"))
	if err != nil {
		return fmt.Errorf("load content lineage: %w", err)
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "[1/4] Building source-pinned GDAT payload...")
	}
	manifest, err := updater.BuildGDATPatchPayload(sourceCache, bundleRoot, gdatOut, []string{"ms2", "finale"}, &lineage)
	if err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "      %d archives, %s stored payload\n", len(manifest.Archives), updater.FormatBytes(manifest.TotalStoredBytes))
	}
	_, ready := MovieCacheReady(root)
	if refreshMovies || !ready {
		if stdout != nil {
			fmt.Fprintln(stdout, "[2/4] Refreshing MS2 audit + Finale EN/JP movie-audio cache...")
		}
		if err := RefreshMovieAudio(root, stdout, stderr); err != nil {
			return err
		}
	} else if stdout != nil {
		fmt.Fprintln(stdout, "[2/4] Reusing complete verified movie-audio cache (22 Finale pairs / 18 JP payload objects).")
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "[3/4] Staging the single .unburger-assets v3 input set (native Go)...")
	}
	stage := filepath.Join(root, ".local", "release_asset_stage")
	sm, err := updater.StageReleaseAssets(root, gdatOut, filepath.Join(root, ".local", "movie_audio_cache"), stage)
	if err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "      %d objects, %s plain\n", len(sm.Objects), updater.FormatBytes(sm.TotalPlainBytes))
		fmt.Fprintln(stdout, "[4/4] Finalizing deterministic native-Go Finale remux target metadata...")
	}
	fm, err := updater.BuildFinaleRemuxTargets(stage, filepath.Join(root, ".local", "movie_audio_source_cache", "finale", "MOVIE_EN"))
	if err != nil {
		return err
	}
	if stdout != nil {
		count := 0
		for _, o := range fm.Objects {
			if o.Kind == "movie_jp_audio" {
				count++
			}
		}
		fmt.Fprintf(stdout, "RELEASE ASSET STAGE: PASS (%d total objects, %d Finale movies)\n", len(fm.Objects), count)
	}
	return nil
}

func BuildContainer(root string, stdout io.Writer) error {
	stage := filepath.Join(root, ".local", "release_asset_stage")
	out := filepath.Join(root, ".local", "release", "mary-skelter.unburger-assets")
	key := filepath.Join(root, ".local", "secrets", "unburger-assets.key")
	m, err := updater.BuildAssetContainerV3(stage, out, key)
	if err != nil {
		return err
	}
	keyBytes, err := os.ReadFile(key)
	if err != nil {
		return err
	}
	if err := updater.VerifyAssetContainerV3(out, keyBytes); err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "UNBURGER-ASSETS V3: PASS (%d objects, %s plain)\nContainer: %s\n", len(m.Objects), updater.FormatBytes(m.TotalPlainBytes), out)
	}
	return nil
}

func preflightPCReleasePackage(root, version string) error {
	if version == "" {
		raw, err := os.ReadFile(filepath.Join(root, "version.txt"))
		if err != nil {
			return err
		}
		version = strings.TrimSpace(string(raw))
	}
	if err := updater.ValidateReleaseVersion(version); err != nil {
		return err
	}
	directory := filepath.Join(root, ".local", "release", "packages")
	name := "Mary-Skelter-2-Finale-UNBURGER-PC-v" + version + ".zip"
	for _, file := range []string{name, name + ".sha256"} {
		path := filepath.Join(directory, file)
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("PC release already exists: %s; change version.txt before building another public package", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func BuildRelease(root, version string, stdout io.Writer) (updater.ReleaseBuildResult, error) {
	if err := preflightPCReleasePackage(root, version); err != nil {
		return updater.ReleaseBuildResult{}, err
	}
	r, err := updater.BuildUpdaterRelease(
		filepath.Join(root, ".local", "release", "mary-skelter.unburger-assets"),
		filepath.Join(root, ".local", "secrets", "unburger-assets.key"),
		filepath.Join(root, ".local", "release", "end-user"), version)
	if err != nil {
		return r, err
	}
	pcZIP, pcSHA, err := updater.PackagePCRelease(r.OutDir, filepath.Join(root, ".local", "release", "packages"), r.Version)
	if err != nil {
		return r, err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "END-USER RELEASE: PASS (%s)\nLinux:   %s\nWindows: %s\nAssets:  %s\nSHA256:  %s\nPC ZIP: %s\nPC ZIP SHA256: %s\n", r.Version, r.LinuxBinary, r.WindowsBinary, r.Assets, r.Checksums, pcZIP, pcSHA)
		fmt.Fprintln(stdout, "Raw asset key is not included; release binaries reconstruct it from embedded shares.")
	}
	return r, nil
}

func RunAll(root string, refreshMovies bool, version string, stdout, stderr io.Writer) error {
	if err := preflightPCReleasePackage(root, version); err != nil {
		return err
	}
	if err := Verify(root, false, stdout, stderr); err != nil {
		return err
	}
	if err := Rebuild(root, stdout, stderr); err != nil {
		return err
	}
	if err := PrepareReleaseAssets(root, refreshMovies, stdout, stderr); err != nil {
		return err
	}
	if err := BuildContainer(root, stdout); err != nil {
		return err
	}
	_, err := BuildRelease(root, version, stdout)
	return err
}
