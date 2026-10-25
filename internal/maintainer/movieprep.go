package maintainer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"maryskelter-unburger/internal/updater"
)

type movieStream map[string]any

type movieAudioRow struct {
	Ordinal            int    `json:"ordinal"`
	CodecEN            any    `json:"codec_en,omitempty"`
	CodecJP            any    `json:"codec_jp,omitempty"`
	DecodedPCMSHA256EN string `json:"decoded_pcm_sha256_en"`
	DecodedPCMSHA256JP string `json:"decoded_pcm_sha256_jp"`
	DecodedAudioEqual  bool   `json:"decoded_audio_equal"`
	ComparisonENFile   string `json:"comparison_en_file"`
	ComparisonJPFile   string `json:"comparison_jp_file"`
	ComparisonENSHA256 string `json:"comparison_en_sha256"`
	ComparisonJPSHA256 string `json:"comparison_jp_sha256"`
	PayloadFile        string `json:"payload_file,omitempty"`
	PayloadSize        int64  `json:"payload_size,omitempty"`
	PayloadSHA256      string `json:"payload_sha256,omitempty"`
}

type moviePacketHash struct {
	SHA256  string `json:"sha256"`
	Packets int    `json:"packets"`
}

type finaleMoviePair struct {
	Name                 string           `json:"name"`
	ENRelativePath       string           `json:"en_relative_path"`
	JPRelativePath       string           `json:"jp_relative_path"`
	ENSHA256             string           `json:"en_sha256"`
	JPSHA256             string           `json:"jp_sha256"`
	ENSize               int64            `json:"en_size"`
	JPSize               int64            `json:"jp_size"`
	ENStreams            []movieStream    `json:"en_streams"`
	JPStreams            []movieStream    `json:"jp_streams"`
	VideoPacketHashEN    *moviePacketHash `json:"video_packet_hash_en,omitempty"`
	VideoPacketHashJP    *moviePacketHash `json:"video_packet_hash_jp,omitempty"`
	VideoPacketIdentical *bool            `json:"video_packet_identical,omitempty"`
	Audio                []movieAudioRow  `json:"audio"`
}

type ms2PurgeCandidate struct {
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

type ms2MovieAudioRow struct {
	Ordinal                    int    `json:"ordinal"`
	DecodedPCMSHA256MovieEN    string `json:"decoded_pcm_sha256_movie_en"`
	DecodedPCMSHA256MovieENSUB string `json:"decoded_pcm_sha256_movie_en_sub"`
	DecodedAudioEqual          bool   `json:"decoded_audio_equal"`
}

type ms2MoviePair struct {
	Name                   string             `json:"name"`
	MovieENRelativePath    string             `json:"movie_en_relative_path"`
	MovieENSUBRelativePath string             `json:"movie_en_sub_relative_path"`
	MovieENSHA256          string             `json:"movie_en_sha256"`
	MovieENSUBSHA256       string             `json:"movie_en_sub_sha256"`
	MovieENSize            int64              `json:"movie_en_size"`
	MovieENSUBSize         int64              `json:"movie_en_sub_size"`
	MovieENStreams         []movieStream      `json:"movie_en_streams"`
	MovieENSUBStreams      []movieStream      `json:"movie_en_sub_streams"`
	Audio                  []ms2MovieAudioRow `json:"audio"`
}

type movieGameReport struct {
	Game                     string              `json:"game"`
	Mode                     string              `json:"mode"`
	Root                     string              `json:"root"`
	Status                   string              `json:"status"`
	PreserveRelativePath     string              `json:"preserve_relative_path,omitempty"`
	PurgeRelativePath        string              `json:"purge_relative_path,omitempty"`
	VoicePurgeRelativePath   string              `json:"voice_purge_relative_path,omitempty"`
	MovieAudioReleaseObjects int                 `json:"movie_audio_release_objects,omitempty"`
	PreserveFiles            []string            `json:"preserve_files,omitempty"`
	PurgeFiles               []string            `json:"purge_files,omitempty"`
	OnlyPreserve             []string            `json:"only_preserve,omitempty"`
	OnlyPurge                []string            `json:"only_purge,omitempty"`
	PurgeCandidates          []ms2PurgeCandidate `json:"purge_candidates,omitempty"`
	MS2Pairs                 []ms2MoviePair      `json:"pairs,omitempty"`

	ENSource    string            `json:"movie_en_source,omitempty"`
	JPSource    string            `json:"movie_jp_source,omitempty"`
	OnlyEN      []string          `json:"only_en,omitempty"`
	OnlyJP      []string          `json:"only_jp,omitempty"`
	FinalePairs []finaleMoviePair `json:"-"`
}

// MarshalJSON preserves the historical Python report shape exactly enough for
// existing evidence consumers: empty lists and the explicit zero MS2 payload
// count remain present, while mode-specific fields do not leak across games.
func (g movieGameReport) MarshalJSON() ([]byte, error) {
	if g.Game == "ms2" {
		return json.Marshal(map[string]any{
			"game": g.Game, "mode": g.Mode, "root": g.Root,
			"preserve_relative_path":      g.PreserveRelativePath,
			"purge_relative_path":         g.PurgeRelativePath,
			"voice_purge_relative_path":   g.VoicePurgeRelativePath,
			"movie_audio_release_objects": g.MovieAudioReleaseObjects,
			"pairs":                       g.MS2Pairs, "purge_candidates": g.PurgeCandidates,
			"status": g.Status, "preserve_files": g.PreserveFiles,
			"purge_files": g.PurgeFiles, "only_preserve": g.OnlyPreserve,
			"only_purge": g.OnlyPurge,
		})
	}
	return json.Marshal(map[string]any{
		"game": g.Game, "mode": g.Mode, "root": g.Root,
		"movie_en_source": g.ENSource, "movie_jp_source": g.JPSource,
		"pairs": g.FinalePairs, "only_en": g.OnlyEN, "only_jp": g.OnlyJP,
		"status": g.Status,
	})
}

type moviePayloadInput struct {
	Kind                string `json:"kind"`
	Game                string `json:"game"`
	TargetMovie         string `json:"target_movie"`
	AudioOrdinal        int    `json:"audio_ordinal"`
	SourceMovieENSHA256 string `json:"source_movie_en_sha256"`
	SourceMovieJPSHA256 string `json:"source_movie_jp_sha256"`
	DecodedPCMSHA256EN  string `json:"decoded_pcm_sha256_en"`
	DecodedPCMSHA256JP  string `json:"decoded_pcm_sha256_jp"`
	Codec               any    `json:"codec"`
	File                string `json:"file"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256"`
}

type movieAudioSummary struct {
	SchemaVersion                int    `json:"schema_version"`
	MS2Status                    string `json:"ms2_status"`
	MS2MovieAudioPayloadObjects  int    `json:"ms2_movie_audio_payload_objects"`
	MS2MovieENSUBPurgeCandidates int    `json:"ms2_movie_en_sub_purge_candidates"`
	FinaleStatus                 string `json:"finale_status"`
	FinaleMoviePairs             int    `json:"finale_movie_pairs"`
	FinaleAudioStreamsIdentical  int    `json:"finale_audio_streams_identical"`
	FinaleAudioStreamsDifferent  int    `json:"finale_audio_streams_different"`
	MovieAudioPayloadObjects     int    `json:"movie_audio_payload_objects"`
	MovieAudioPayloadBytes       int64  `json:"movie_audio_payload_bytes"`
	Complete                     bool   `json:"complete"`
}

type movieAudioAudit struct {
	SchemaVersion int               `json:"schema_version"`
	CreatedUTC    string            `json:"created_utc"`
	SourceCache   string            `json:"source_cache"`
	AudioCache    string            `json:"audio_cache"`
	Policy        map[string]string `json:"policy"`
	Games         []movieGameReport `json:"games"`
}

type moviePayloadManifest struct {
	SchemaVersion   int                 `json:"schema_version"`
	ContainerTarget string              `json:"container_target"`
	MovieAudioGames []string            `json:"movie_audio_games"`
	Objects         []moviePayloadInput `json:"objects"`
}

func relOrAbs(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return filepath.ToSlash(rel)
	}
	if err == nil && rel == "." {
		return "."
	}
	return path
}

func jsonWrite(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func slimMovieStream(s movieStream) movieStream {
	keys := []string{"index", "codec_type", "codec_name", "codec_tag_string", "profile", "sample_rate", "channels", "channel_layout", "duration", "bit_rate", "width", "height", "pix_fmt", "avg_frame_rate", "r_frame_rate"}
	out := movieStream{}
	for _, k := range keys {
		if v, ok := s[k]; ok && v != nil && fmt.Sprint(v) != "" {
			out[k] = v
		}
	}
	return out
}

func ffprobeStreams(path string) ([]movieStream, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_streams", "-show_entries",
		"stream=index,codec_type,codec_name,codec_tag_string,profile,sample_rate,channels,channel_layout,duration,bit_rate,width,height,pix_fmt,avg_frame_rate,r_frame_rate",
		"-of", "json", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	var doc struct {
		Streams []movieStream `json:"streams"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, err
	}
	for i := range doc.Streams {
		doc.Streams[i] = slimMovieStream(doc.Streams[i])
	}
	return doc.Streams, nil
}

func streamType(in []movieStream, kind string) []movieStream {
	var out []movieStream
	for _, s := range in {
		if fmt.Sprint(s["codec_type"]) == kind {
			out = append(out, s)
		}
	}
	return out
}

func decodedAudioHash(path string, ordinal int) (string, error) {
	cmd := exec.Command("ffmpeg", "-v", "error", "-i", path, "-map", fmt.Sprintf("0:a:%d", ordinal),
		"-vn", "-sn", "-dn", "-c:a", "pcm_s16le", "-f", "hash", "-hash", "sha256", "-")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ffmpeg decoded hash %s a:%d: %w", path, ordinal, err)
	}
	line := strings.TrimSpace(string(out))
	i := strings.IndexByte(line, '=')
	if i < 0 {
		return "", fmt.Errorf("unexpected ffmpeg hash output for %s: %q", path, line)
	}
	return strings.ToLower(strings.TrimSpace(line[i+1:])), nil
}

func packetHash(path, selector string) (moviePacketHash, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-select_streams", selector, "-show_packets",
		"-show_entries", "packet=size,data_hash", "-show_data_hash", "sha256", "-of", "compact=p=0:nk=0", path)
	out, err := cmd.Output()
	if err != nil {
		return moviePacketHash{}, fmt.Errorf("ffprobe packet hash %s: %w", path, err)
	}
	h := sha256.New()
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		_, _ = h.Write([]byte(line))
		_, _ = h.Write([]byte{'\n'})
		n++
	}
	if err := sc.Err(); err != nil {
		return moviePacketHash{}, err
	}
	return moviePacketHash{SHA256: hex.EncodeToString(h.Sum(nil)), Packets: n}, nil
}

func extractAudio(path string, ordinal int, dstBase string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(dstBase), 0o755); err != nil {
		return "", err
	}
	m4a := dstBase + fmt.Sprintf(".a%d.m4a", ordinal)
	mka := dstBase + fmt.Sprintf(".a%d.mka", ordinal)
	_ = os.Remove(m4a)
	_ = os.Remove(mka)
	args := []string{"-y", "-v", "error", "-i", path, "-map", fmt.Sprintf("0:a:%d", ordinal), "-vn", "-sn", "-dn", "-c:a", "copy", "-map_metadata", "-1", m4a}
	if err := exec.Command("ffmpeg", args...).Run(); err == nil {
		return m4a, nil
	}
	args[len(args)-1] = mka
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("ffmpeg audio extract %s a:%d: %w: %s", path, ordinal, err, strings.TrimSpace(string(out)))
	}
	return mka, nil
}

func mp4Files(dir string) (map[string]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, e := range ents {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			continue
		}
		out[e.Name()] = filepath.Join(dir, e.Name())
	}
	return out, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func diffKeys[A, B any](a map[string]A, b map[string]B) []string {
	out := []string{}
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func processMS2Movies(root, gameRoot string, stdout io.Writer) (movieGameReport, error) {
	mw := filepath.Join(gameRoot, "masterWindows")
	preserveDir, purgeDir := filepath.Join(mw, "MOVIE_EN"), filepath.Join(mw, "MOVIE_EN_SUB")
	g := movieGameReport{Game: "ms2", Mode: "ms2_preserve_movie_en_purge_movie_en_sub", Root: gameRoot,
		PreserveRelativePath: "masterWindows/MOVIE_EN", PurgeRelativePath: "masterWindows/MOVIE_EN_SUB",
		VoicePurgeRelativePath: "masterWindows/Voice_EN.bra", MovieAudioReleaseObjects: 0}
	if !dir(preserveDir) {
		g.Status = "movie_en_missing"
		return g, nil
	}
	if !dir(purgeDir) {
		g.Status = "movie_en_sub_missing"
		return g, nil
	}
	preserve, err := mp4Files(preserveDir)
	if err != nil {
		return g, err
	}
	purge, err := mp4Files(purgeDir)
	if err != nil {
		return g, err
	}
	g.Status = "ok"
	g.PreserveFiles = sortedKeys(preserve)
	g.PurgeFiles = sortedKeys(purge)
	g.OnlyPreserve = diffKeys(preserve, purge)
	g.OnlyPurge = diffKeys(purge, preserve)
	for _, name := range sortedKeys(purge) {
		st, _ := os.Stat(purge[name])
		sum, err := updater.SHA256File(purge[name])
		if err != nil {
			return g, err
		}
		g.PurgeCandidates = append(g.PurgeCandidates, ms2PurgeCandidate{RelativePath: filepath.ToSlash(filepath.Join("masterWindows/MOVIE_EN_SUB", name)), Size: st.Size(), SHA256: sum})
	}
	for _, name := range sortedKeys(preserve) {
		sub, ok := purge[name]
		if !ok {
			continue
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "[ms2] fingerprint MOVIE_EN vs MOVIE_EN_SUB %s\n", name)
		}
		en := preserve[name]
		es, err := ffprobeStreams(en)
		if err != nil {
			return g, err
		}
		ss, err := ffprobeStreams(sub)
		if err != nil {
			return g, err
		}
		ea, sa := streamType(es, "audio"), streamType(ss, "audio")
		if len(ea) != len(sa) {
			return g, fmt.Errorf("ms2/%s: audio stream count differs MOVIE_EN=%d MOVIE_EN_SUB=%d", name, len(ea), len(sa))
		}
		est, _ := os.Stat(en)
		sst, _ := os.Stat(sub)
		eh, err := updater.SHA256File(en)
		if err != nil {
			return g, err
		}
		sh, err := updater.SHA256File(sub)
		if err != nil {
			return g, err
		}
		p := ms2MoviePair{Name: name, MovieENRelativePath: filepath.ToSlash(filepath.Join("masterWindows/MOVIE_EN", name)), MovieENSUBRelativePath: filepath.ToSlash(filepath.Join("masterWindows/MOVIE_EN_SUB", name)), MovieENSHA256: eh, MovieENSUBSHA256: sh, MovieENSize: est.Size(), MovieENSUBSize: sst.Size(), MovieENStreams: es, MovieENSUBStreams: ss}
		for i := range ea {
			a, err := decodedAudioHash(en, i)
			if err != nil {
				return g, err
			}
			b, err := decodedAudioHash(sub, i)
			if err != nil {
				return g, err
			}
			p.Audio = append(p.Audio, ms2MovieAudioRow{Ordinal: i, DecodedPCMSHA256MovieEN: a, DecodedPCMSHA256MovieENSUB: b, DecodedAudioEqual: a == b})
		}
		g.MS2Pairs = append(g.MS2Pairs, p)
	}
	_ = root
	return g, nil
}

func processFinaleMovies(root, gameRoot, enDir, jpDir, tmpAudio, finalAudio string, stdout io.Writer) (movieGameReport, []moviePayloadInput, error) {
	g := movieGameReport{Game: "finale", Mode: "finale_movie_en_vs_movie_jp", Root: gameRoot, ENSource: enDir, JPSource: jpDir}
	if !dir(enDir) {
		g.Status = "no_movie_en"
		return g, nil, nil
	}
	if !dir(jpDir) {
		g.Status = "movie_jp_missing"
		return g, nil, nil
	}
	enFiles, err := mp4Files(enDir)
	if err != nil {
		return g, nil, err
	}
	jpFiles, err := mp4Files(jpDir)
	if err != nil {
		return g, nil, err
	}
	g.Status = "ok"
	g.OnlyEN = diffKeys(enFiles, jpFiles)
	g.OnlyJP = diffKeys(jpFiles, enFiles)
	if len(g.OnlyEN) != 0 {
		return g, nil, fmt.Errorf("finale: MOVIE_EN files without MOVIE_JP counterpart: %v", g.OnlyEN)
	}
	var payloads []moviePayloadInput
	for _, name := range sortedKeys(enFiles) {
		jp, ok := jpFiles[name]
		if !ok {
			continue
		}
		en := enFiles[name]
		if stdout != nil {
			fmt.Fprintf(stdout, "[finale] compare %s\n", name)
		}
		es, err := ffprobeStreams(en)
		if err != nil {
			return g, nil, err
		}
		js, err := ffprobeStreams(jp)
		if err != nil {
			return g, nil, err
		}
		ea, ja := streamType(es, "audio"), streamType(js, "audio")
		ev, jv := streamType(es, "video"), streamType(js, "video")
		if len(ea) != len(ja) {
			return g, nil, fmt.Errorf("finale/%s: audio stream count differs EN=%d JP=%d", name, len(ea), len(ja))
		}
		est, _ := os.Stat(en)
		jst, _ := os.Stat(jp)
		eh, _ := updater.SHA256File(en)
		jh, _ := updater.SHA256File(jp)
		p := finaleMoviePair{Name: name, ENRelativePath: filepath.ToSlash(filepath.Join("masterWindows/MOVIE_EN", name)), JPRelativePath: filepath.ToSlash(filepath.Join("masterWindows/MOVIE_JP", name)), ENSHA256: eh, JPSHA256: jh, ENSize: est.Size(), JPSize: jst.Size(), ENStreams: es, JPStreams: js}
		if len(ev) > 0 && len(jv) > 0 {
			a, err := packetHash(en, "v:0")
			if err != nil {
				return g, nil, err
			}
			b, err := packetHash(jp, "v:0")
			if err != nil {
				return g, nil, err
			}
			eq := a == b
			p.VideoPacketHashEN = &a
			p.VideoPacketHashJP = &b
			p.VideoPacketIdentical = &eq
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		for i := range ea {
			enPCM, err := decodedAudioHash(en, i)
			if err != nil {
				return g, nil, err
			}
			jpPCM, err := decodedAudioHash(jp, i)
			if err != nil {
				return g, nil, err
			}
			cmp := filepath.Join(tmpAudio, "comparison", "finale", stem)
			enEx, err := extractAudio(en, i, filepath.Join(cmp, "en"))
			if err != nil {
				return g, nil, err
			}
			jpEx, err := extractAudio(jp, i, filepath.Join(cmp, "jp"))
			if err != nil {
				return g, nil, err
			}
			enh, _ := updater.SHA256File(enEx)
			jph, _ := updater.SHA256File(jpEx)
			row := movieAudioRow{Ordinal: i, CodecEN: ea[i]["codec_name"], CodecJP: ja[i]["codec_name"], DecodedPCMSHA256EN: enPCM, DecodedPCMSHA256JP: jpPCM, DecodedAudioEqual: enPCM == jpPCM, ComparisonENFile: relOrAbs(root, filepath.Join(finalAudio, strings.TrimPrefix(enEx, tmpAudio+string(filepath.Separator)))), ComparisonJPFile: relOrAbs(root, filepath.Join(finalAudio, strings.TrimPrefix(jpEx, tmpAudio+string(filepath.Separator)))), ComparisonENSHA256: enh, ComparisonJPSHA256: jph}
			if enPCM != jpPCM {
				payload := filepath.Join(tmpAudio, "payload", "finale", fmt.Sprintf("%s.a%d%s", stem, i, filepath.Ext(jpEx)))
				if err := copyMovieFile(jpEx, payload); err != nil {
					return g, nil, err
				}
				st, _ := os.Stat(payload)
				ph, _ := updater.SHA256File(payload)
				row.PayloadFile, row.PayloadSize, row.PayloadSHA256 = relOrAbs(root, filepath.Join(finalAudio, strings.TrimPrefix(payload, tmpAudio+string(filepath.Separator)))), st.Size(), ph
				payloads = append(payloads, moviePayloadInput{Kind: "movie_jp_audio", Game: "finale", TargetMovie: p.ENRelativePath, AudioOrdinal: i, SourceMovieENSHA256: eh, SourceMovieJPSHA256: jh, DecodedPCMSHA256EN: enPCM, DecodedPCMSHA256JP: jpPCM, Codec: ja[i]["codec_name"], File: row.PayloadFile, Size: st.Size(), SHA256: ph})
			}
			p.Audio = append(p.Audio, row)
		}
		g.FinalePairs = append(g.FinalePairs, p)
	}
	return g, payloads, nil
}

// PrepareMovieAudioEvidence replaces the historical Python movie-audio orchestrator.
// ffmpeg/ffprobe remain codec tools only; all inventory, policy, cache lifecycle,
// equality decisions and manifest generation are owned by Go.
func PrepareMovieAudioEvidence(root string, roots SourceRoots, stdout io.Writer) (movieAudioSummary, error) {
	var summary movieAudioSummary
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			return summary, fmt.Errorf("maintainer tool not found in PATH: %s", tool)
		}
	}
	sourceCache := filepath.Join(root, ".local", "movie_audio_source_cache")
	audioCache := filepath.Join(root, ".local", "movie_audio_cache")
	_ = os.RemoveAll(filepath.Join(sourceCache, "ms2"))
	tmp := audioCache + ".build.tmp"
	prev := audioCache + ".build.previous"
	_ = os.RemoveAll(tmp)
	_ = os.RemoveAll(prev)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return summary, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmp)
		}
	}()

	ms2, err := processMS2Movies(root, roots.MS2, stdout)
	if err != nil {
		return summary, err
	}
	finaleEN := filepath.Join(sourceCache, "finale", "MOVIE_EN")
	finaleJP := filepath.Join(sourceCache, "finale", "MOVIE_JP")
	finale, payloads, err := processFinaleMovies(root, roots.Finale, finaleEN, finaleJP, tmp, audioCache, stdout)
	if err != nil {
		return summary, err
	}

	same, different := 0, 0
	var payloadBytes int64
	for _, p := range finale.FinalePairs {
		for _, a := range p.Audio {
			if a.DecodedAudioEqual {
				same++
			} else {
				different++
				payloadBytes += a.PayloadSize
			}
		}
	}
	complete := ms2.Status == "ok" && finale.Status == "ok" && len(finale.OnlyEN) == 0
	summary = movieAudioSummary{SchemaVersion: 2, MS2Status: ms2.Status, MS2MovieAudioPayloadObjects: 0, MS2MovieENSUBPurgeCandidates: len(ms2.PurgeCandidates), FinaleStatus: finale.Status, FinaleMoviePairs: len(finale.FinalePairs), FinaleAudioStreamsIdentical: same, FinaleAudioStreamsDifferent: different, MovieAudioPayloadObjects: len(payloads), MovieAudioPayloadBytes: payloadBytes, Complete: complete}
	audit := movieAudioAudit{SchemaVersion: 3, CreatedUTC: time.Now().UTC().Format(time.RFC3339Nano), SourceCache: relOrAbs(root, sourceCache), AudioCache: relOrAbs(root, audioCache), Policy: map[string]string{"ms2": "preserve MOVIE_EN; audit/purge MOVIE_EN_SUB; never require MOVIE_JP; never stage MS2 movie audio", "finale": "compare MOVIE_EN with MOVIE_JP; stage only differing JP audio for native-Go remux"}, Games: []movieGameReport{ms2, finale}}
	manifest := moviePayloadManifest{SchemaVersion: 2, ContainerTarget: ".unburger-assets v3", MovieAudioGames: []string{"finale"}, Objects: payloads}
	if err := jsonWrite(filepath.Join(tmp, "MOVIE_AUDIO_AUDIT.json"), audit); err != nil {
		return summary, err
	}
	if err := jsonWrite(filepath.Join(tmp, "MOVIE_AUDIO_PAYLOAD_INPUT.json"), manifest); err != nil {
		return summary, err
	}
	if err := jsonWrite(filepath.Join(tmp, "SUMMARY.json"), summary); err != nil {
		return summary, err
	}
	readme := "# Movie audio source/cache reproducibility\n\nThis is maintainer/build documentation, not an end-user installation step.\n\nMS2: preserve `masterWindows/MOVIE_EN`; `MOVIE_EN_SUB` is the known English-dub movie set in the project owner's inspected PC installation and is audited as a purge target together with `Voice_EN.bra`. Do not require `MOVIE_JP` and do not create MS2 movie-audio release objects.\n\nFinale: use an owned Steam install and temporarily select Japanese so Steam downloads `masterWindows/MOVIE_JP`. The Go maintainer builder caches Finale MOVIE_EN/MOVIE_JP, compares decoded audio through ffmpeg/ffprobe, and places only differing Japanese audio in `payload/` for the single `.unburger-assets v3` release container. Do not publish full JP MP4 source files.\n\nThe public end-user updater must remain self-contained and must never require Steam language switching, ffmpeg, ffprobe, Python, or a second private asset archive.\n"
	if err := os.WriteFile(filepath.Join(tmp, "README_REPRODUCIBILITY.md"), []byte(readme), 0o644); err != nil {
		return summary, err
	}
	if !complete {
		return summary, fmt.Errorf("requested movie discovery is incomplete: MS2=%s Finale=%s", ms2.Status, finale.Status)
	}
	if _, err := os.Stat(audioCache); err == nil {
		if err := os.Rename(audioCache, prev); err != nil {
			return summary, err
		}
	}
	if err := os.Rename(tmp, audioCache); err != nil {
		_ = os.Rename(prev, audioCache)
		return summary, err
	}
	committed = true
	_ = os.RemoveAll(prev)
	if stdout != nil {
		fmt.Fprintln(stdout, "\n=== MOVIE AUDIO CACHE SUMMARY ===")
		fmt.Fprintf(stdout, "MS2 status:                %s\nMS2 release audio objects: 0\nMS2 MOVIE_EN_SUB targets:  %d\nFinale status:             %s\nFinale movie pairs:        %d\nFinale audio identical:    %d\nFinale audio different:    %d\nPayload objects:           %d\nPayload bytes:             %d\nSource cache:              %s\nAudio cache:               %s\nAsset input manifest:      %s\nInstalled game files were not modified.\n", summary.MS2Status, summary.MS2MovieENSUBPurgeCandidates, summary.FinaleStatus, summary.FinaleMoviePairs, same, different, len(payloads), payloadBytes, sourceCache, audioCache, filepath.Join(audioCache, "MOVIE_AUDIO_PAYLOAD_INPUT.json"))
	}
	return summary, nil
}
