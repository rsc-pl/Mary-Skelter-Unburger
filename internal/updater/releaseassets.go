package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const finaleExpectedRemuxMovies = 18

type ReleaseStageManifest struct {
	ReleaseVersion              string               `json:"release_version,omitempty"`
	SchemaVersion               int                  `json:"schema_version"`
	ContainerFormat             string               `json:"container_format"`
	ContainerCount              int                  `json:"container_count"`
	RuntimeExternalDependencies []string             `json:"runtime_external_dependencies,omitempty"`
	MovieAudioGames             []string             `json:"movie_audio_games,omitempty"`
	MS2MoviePolicy              string               `json:"ms2_movie_policy,omitempty"`
	Objects                     []ReleaseStageObject `json:"objects"`
	TotalPlainBytes             int64                `json:"total_plain_bytes"`
}

type ReleaseStageObject struct {
	Kind                string `json:"kind"`
	Game                string `json:"game"`
	TargetMovie         string `json:"target_movie,omitempty"`
	AudioOrdinal        int    `json:"audio_ordinal,omitempty"`
	Codec               string `json:"codec,omitempty"`
	SourceMovieENSHA256 string `json:"source_movie_en_sha256,omitempty"`
	SourceMovieJPSHA256 string `json:"source_movie_jp_sha256,omitempty"`
	DecodedPCMSHA256EN  string `json:"decoded_pcm_sha256_en,omitempty"`
	DecodedPCMSHA256JP  string `json:"decoded_pcm_sha256_jp,omitempty"`
	LogicalPath         string `json:"logical_path"`
	SourceFile          string `json:"source_file,omitempty"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256"`
	RemuxTargetSize     int64  `json:"remux_target_size,omitempty"`
	RemuxTargetSHA256   string `json:"remux_target_sha256,omitempty"`
	SourceVideoSHA256   string `json:"source_video_sha256,omitempty"`
}

type FinaleRemuxAsset struct {
	RelativePath      string
	SourceSHA256      string
	PayloadPath       string // development staging only
	PayloadObject     string // .unburger-assets v3 logical object
	PayloadSHA256     string
	TargetSHA256      string
	TargetSize        int64
	SourceVideoSHA256 string
}

func defaultAssetStageRoot(bundleRoot string) string {
	abs, err := filepath.Abs(bundleRoot)
	if err != nil {
		return filepath.Join(filepath.Dir(bundleRoot), "release_asset_stage")
	}
	return filepath.Join(filepath.Dir(abs), "release_asset_stage")
}

func loadReleaseStageManifest(stageRoot string) (ReleaseStageManifest, error) {
	path := filepath.Join(stageRoot, "ASSET_STAGE_MANIFEST.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ReleaseStageManifest{}, fmt.Errorf("release asset stage manifest: %w", err)
	}
	var m ReleaseStageManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return ReleaseStageManifest{}, fmt.Errorf("release asset stage manifest: %w", err)
	}
	if m.ContainerFormat != ".unburger-assets v3" || m.ContainerCount != 1 {
		return ReleaseStageManifest{}, fmt.Errorf("release stage is not the canonical single .unburger-assets v3 input set")
	}
	return m, nil
}

func writeReleaseStageManifest(stageRoot string, m ReleaseStageManifest) error {
	path := filepath.Join(stageRoot, "ASSET_STAGE_MANIFEST.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return err
	}
	var objects []map[string]any
	if err := json.Unmarshal(top["objects"], &objects); err != nil {
		return err
	}
	updates := map[string]ReleaseStageObject{}
	for _, o := range m.Objects {
		if o.Kind == "movie_jp_audio" {
			updates[o.LogicalPath] = o
		}
	}
	seen := 0
	for _, o := range objects {
		kind, _ := o["kind"].(string)
		if kind != "movie_jp_audio" {
			continue
		}
		logical, _ := o["logical_path"].(string)
		u, ok := updates[logical]
		if !ok {
			return fmt.Errorf("release stage movie object disappeared while finalizing: %s", logical)
		}
		o["remux_target_size"] = u.RemuxTargetSize
		o["remux_target_sha256"] = u.RemuxTargetSHA256
		o["source_video_sha256"] = u.SourceVideoSHA256
		seen++
	}
	if seen != len(updates) {
		return fmt.Errorf("updated %d movie stage objects, expected %d", seen, len(updates))
	}
	objRaw, err := json.Marshal(objects)
	if err != nil {
		return err
	}
	top["objects"] = objRaw
	schemaRaw, err := json.Marshal(m.SchemaVersion)
	if err != nil {
		return err
	}
	top["schema_version"] = schemaRaw
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func finaleRemuxAssets(stageRoot string) ([]FinaleRemuxAsset, error) {
	m, err := loadReleaseStageManifest(stageRoot)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []FinaleRemuxAsset
	for _, o := range m.Objects {
		if o.Kind != "movie_jp_audio" {
			continue
		}
		if o.Game != "finale" {
			return nil, fmt.Errorf("non-Finale movie audio object in release stage: %q", o.Game)
		}
		if o.AudioOrdinal != 0 || o.Codec != "aac" {
			return nil, fmt.Errorf("unsupported Finale movie payload %s: ordinal=%d codec=%q", o.TargetMovie, o.AudioOrdinal, o.Codec)
		}
		if !filepath.IsLocal(filepath.FromSlash(o.TargetMovie)) || filepath.Ext(o.TargetMovie) != ".mp4" {
			return nil, fmt.Errorf("unsafe Finale target movie path %q", o.TargetMovie)
		}
		if seen[o.TargetMovie] {
			return nil, fmt.Errorf("duplicate Finale movie payload target %s", o.TargetMovie)
		}
		seen[o.TargetMovie] = true
		if o.SourceMovieENSHA256 == "" || o.SHA256 == "" || o.RemuxTargetSHA256 == "" || o.RemuxTargetSize <= 0 || o.SourceVideoSHA256 == "" {
			return nil, fmt.Errorf("Finale remux metadata incomplete for %s; run build-remux-targets after movie discovery", o.TargetMovie)
		}
		payload := filepath.Join(stageRoot, "objects", filepath.FromSlash(o.LogicalPath))
		info, err := os.Stat(payload)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Finale remux payload unavailable %s: %w", payload, err)
		}
		if info.Size() != o.Size {
			return nil, fmt.Errorf("Finale remux payload size mismatch %s", payload)
		}
		sha, err := SHA256File(payload)
		if err != nil || sha != o.SHA256 {
			return nil, fmt.Errorf("Finale remux payload SHA mismatch %s: got %s err=%v", payload, sha, err)
		}
		out = append(out, FinaleRemuxAsset{
			RelativePath:      o.TargetMovie,
			SourceSHA256:      o.SourceMovieENSHA256,
			PayloadPath:       payload,
			PayloadSHA256:     o.SHA256,
			TargetSHA256:      o.RemuxTargetSHA256,
			TargetSize:        o.RemuxTargetSize,
			SourceVideoSHA256: o.SourceVideoSHA256,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelativePath < out[j].RelativePath })
	if len(out) != finaleExpectedRemuxMovies {
		return nil, fmt.Errorf("Finale release stage has %d remux movies; expected %d proven differing movies", len(out), finaleExpectedRemuxMovies)
	}
	return out, nil
}

// BuildFinaleRemuxTargets finalizes the canonical release staging manifest with
// deterministic native-Go remux target SHA/size metadata. The payload bytes are
// unchanged; this only adds install/idempotence metadata for the same 18 staged
// JP-audio objects that will live in the single .unburger-assets v3 container.
func BuildFinaleRemuxTargets(stageRoot, sourceMovieRoot string) (ReleaseStageManifest, error) {
	m, err := loadReleaseStageManifest(stageRoot)
	if err != nil {
		return ReleaseStageManifest{}, err
	}
	count := 0
	for i := range m.Objects {
		o := &m.Objects[i]
		if o.Kind != "movie_jp_audio" {
			continue
		}
		if o.Game != "finale" || o.AudioOrdinal != 0 || o.Codec != "aac" {
			return ReleaseStageManifest{}, fmt.Errorf("unsupported movie release object for target finalization: %+v", *o)
		}
		name := filepath.Base(filepath.FromSlash(o.TargetMovie))
		source := filepath.Join(sourceMovieRoot, name)
		sha, err := SHA256File(source)
		if err != nil {
			return ReleaseStageManifest{}, fmt.Errorf("source movie %s: %w", source, err)
		}
		if sha != o.SourceMovieENSHA256 {
			return ReleaseStageManifest{}, fmt.Errorf("source movie SHA mismatch %s: got %s expected %s", source, sha, o.SourceMovieENSHA256)
		}
		videoSHA, err := FinaleMovieVideoSHA256(source)
		if err != nil {
			return ReleaseStageManifest{}, fmt.Errorf("source movie video identity %s: %w", source, err)
		}
		o.SourceVideoSHA256 = videoSHA
		payload := filepath.Join(stageRoot, "objects", filepath.FromSlash(o.LogicalPath))
		psha, err := SHA256File(payload)
		if err != nil {
			return ReleaseStageManifest{}, err
		}
		if psha != o.SHA256 {
			return ReleaseStageManifest{}, fmt.Errorf("staged payload SHA mismatch %s", payload)
		}
		workDir := filepath.Join(stageRoot, ".remux-targets.tmp")
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			return ReleaseStageManifest{}, err
		}
		out := filepath.Join(workDir, name)
		_ = os.Remove(out)
		if err := RemuxFinaleMovie(source, payload, out); err != nil {
			return ReleaseStageManifest{}, fmt.Errorf("native remux target %s: %w", name, err)
		}
		info, err := os.Stat(out)
		if err != nil {
			return ReleaseStageManifest{}, err
		}
		tsha, err := SHA256File(out)
		if err != nil {
			return ReleaseStageManifest{}, err
		}
		o.RemuxTargetSize = info.Size()
		o.RemuxTargetSHA256 = tsha
		_ = os.Remove(out)
		count++
	}
	_ = os.RemoveAll(filepath.Join(stageRoot, ".remux-targets.tmp"))
	if count != finaleExpectedRemuxMovies {
		return ReleaseStageManifest{}, fmt.Errorf("finalized %d Finale remux targets; expected %d", count, finaleExpectedRemuxMovies)
	}
	m.SchemaVersion = 2
	if err := writeReleaseStageManifest(stageRoot, m); err != nil {
		return ReleaseStageManifest{}, err
	}
	return m, nil
}
