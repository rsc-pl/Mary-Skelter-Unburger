package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type moviePayloadManifest struct {
	MovieAudioGames []string             `json:"movie_audio_games"`
	Objects         []moviePayloadObject `json:"objects"`
}

type moviePayloadObject struct {
	Game                string `json:"game"`
	TargetMovie         string `json:"target_movie"`
	AudioOrdinal        int    `json:"audio_ordinal"`
	Codec               string `json:"codec"`
	SourceMovieENSHA256 string `json:"source_movie_en_sha256"`
	SourceMovieJPSHA256 string `json:"source_movie_jp_sha256"`
	DecodedPCMSHA256EN  string `json:"decoded_pcm_sha256_en"`
	DecodedPCMSHA256JP  string `json:"decoded_pcm_sha256_jp"`
	File                string `json:"file"`
	Size                int64  `json:"size"`
	SHA256              string `json:"sha256"`
}

// StageReleaseAssets builds the single canonical staging tree consumed by
// BuildAssetContainerV3. It is the Go replacement for toolchain/python-src/scripts/stage_release_assets.py.
func StageReleaseAssets(projectRoot, gdatRoot, movieRoot, outRoot string) (ReleaseStageManifest, error) {
	var result ReleaseStageManifest
	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return result, err
	}
	gdatRoot, err = filepath.Abs(gdatRoot)
	if err != nil {
		return result, err
	}
	movieRoot, err = filepath.Abs(movieRoot)
	if err != nil {
		return result, err
	}
	outRoot, err = filepath.Abs(outRoot)
	if err != nil {
		return result, err
	}
	relOut, err := filepath.Rel(projectRoot, outRoot)
	if err != nil || relOut == "." || strings.HasPrefix(relOut, ".."+string(filepath.Separator)) || relOut == ".." || filepath.IsAbs(relOut) {
		return result, fmt.Errorf("release asset stage must stay inside project tree: %s", outRoot)
	}

	var gm GDATPatchManifest
	gmanifestPath := filepath.Join(gdatRoot, "GDAT_PATCH_MANIFEST.json")
	raw, err := os.ReadFile(gmanifestPath)
	if err != nil {
		return result, fmt.Errorf("read GDAT payload manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &gm); err != nil {
		return result, fmt.Errorf("parse GDAT payload manifest: %w", err)
	}

	var mm moviePayloadManifest
	movieManifestPath := filepath.Join(movieRoot, "MOVIE_AUDIO_PAYLOAD_INPUT.json")
	raw, err = os.ReadFile(movieManifestPath)
	if err != nil {
		return result, fmt.Errorf("read movie payload manifest: %w", err)
	}
	if err := json.Unmarshal(raw, &mm); err != nil {
		return result, fmt.Errorf("parse movie payload manifest: %w", err)
	}
	if len(mm.MovieAudioGames) != 1 || mm.MovieAudioGames[0] != "finale" {
		return result, fmt.Errorf("movie payload must be Finale-only, got %v", mm.MovieAudioGames)
	}

	tmp := outRoot + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return result, err
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(tmp)
		}
	}()

	copyVerified := func(src, dst, wantSHA string, wantSize int64) (int64, string, error) {
		st, err := os.Stat(src)
		if err != nil || !st.Mode().IsRegular() {
			return 0, "", fmt.Errorf("missing asset input %s", src)
		}
		if wantSize >= 0 && st.Size() != wantSize {
			return 0, "", fmt.Errorf("asset size mismatch %s: %d != %d", src, st.Size(), wantSize)
		}
		gotSHA, err := SHA256File(src)
		if err != nil {
			return 0, "", err
		}
		if wantSHA != "" && !strings.EqualFold(gotSHA, wantSHA) {
			return 0, "", fmt.Errorf("asset SHA mismatch %s: %s != %s", src, gotSHA, wantSHA)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, "", err
		}
		if err := copyFileExact(src, dst, 0o644); err != nil {
			return 0, "", err
		}
		return st.Size(), gotSHA, nil
	}

	if _, _, err := copyVerified(gmanifestPath, filepath.Join(tmp, "inputs", "gdat", "GDAT_PATCH_MANIFEST.json"), "", -1); err != nil {
		return result, err
	}
	objects := make([]ReleaseStageObject, 0, 2200)
	for _, archive := range gm.Archives {
		for _, entry := range archive.ChangedEntries {
			rel := filepath.FromSlash(entry.BlobFile)
			if !filepath.IsLocal(rel) {
				return result, fmt.Errorf("unsafe GDAT blob path %q", entry.BlobFile)
			}
			logical := filepath.ToSlash(filepath.Join("gdat", rel))
			size, sha, err := copyVerified(filepath.Join(gdatRoot, rel), filepath.Join(tmp, "objects", filepath.FromSlash(logical)), "", entry.StoredBytes)
			if err != nil {
				return result, err
			}
			objects = append(objects, ReleaseStageObject{
				Kind: "gdat_entry", Game: archive.GameKey, LogicalPath: logical,
				SourceFile: filepath.ToSlash(filepath.Join(relOut, "objects", filepath.FromSlash(logical))),
				Size:       size, SHA256: sha,
			})
		}
	}

	if _, _, err := copyVerified(movieManifestPath, filepath.Join(tmp, "inputs", "movie", "MOVIE_AUDIO_PAYLOAD_INPUT.json"), "", -1); err != nil {
		return result, err
	}
	for _, obj := range mm.Objects {
		if obj.Game != "finale" {
			return result, fmt.Errorf("non-Finale movie-audio release object rejected: %q", obj.Game)
		}
		src := obj.File
		if !filepath.IsAbs(src) {
			src = filepath.Join(projectRoot, filepath.FromSlash(src))
		}
		suffix := filepath.Ext(src)
		stem := strings.TrimSuffix(filepath.Base(filepath.FromSlash(obj.TargetMovie)), filepath.Ext(obj.TargetMovie))
		logical := filepath.ToSlash(filepath.Join("movie_audio", obj.Game, fmt.Sprintf("%s.a%d%s", stem, obj.AudioOrdinal, suffix)))
		size, sha, err := copyVerified(src, filepath.Join(tmp, "objects", filepath.FromSlash(logical)), obj.SHA256, obj.Size)
		if err != nil {
			return result, err
		}
		objects = append(objects, ReleaseStageObject{
			Kind: "movie_jp_audio", Game: obj.Game, TargetMovie: obj.TargetMovie,
			AudioOrdinal: obj.AudioOrdinal, Codec: obj.Codec,
			SourceMovieENSHA256: obj.SourceMovieENSHA256, SourceMovieJPSHA256: obj.SourceMovieJPSHA256,
			DecodedPCMSHA256EN: obj.DecodedPCMSHA256EN, DecodedPCMSHA256JP: obj.DecodedPCMSHA256JP,
			LogicalPath: logical,
			SourceFile:  filepath.ToSlash(filepath.Join(relOut, "objects", filepath.FromSlash(logical))),
			Size:        size, SHA256: sha,
		})
	}
	sort.Slice(objects, func(i, j int) bool {
		if objects[i].Kind != objects[j].Kind {
			return objects[i].Kind < objects[j].Kind
		}
		if objects[i].Game != objects[j].Game {
			return objects[i].Game < objects[j].Game
		}
		return objects[i].LogicalPath < objects[j].LogicalPath
	})
	var total int64
	for _, o := range objects {
		total += o.Size
	}
	var releaseVersion string
	if b, readErr := os.ReadFile(filepath.Join(projectRoot, "version.txt")); readErr == nil {
		releaseVersion = strings.TrimSpace(string(b))
		if err := ValidateReleaseVersion(releaseVersion); err != nil {
			return ReleaseStageManifest{}, err
		}
	} else if !os.IsNotExist(readErr) {
		return ReleaseStageManifest{}, readErr
	}
	result = ReleaseStageManifest{ReleaseVersion: releaseVersion,
		SchemaVersion: 1, ContainerFormat: ".unburger-assets v3", ContainerCount: 1,
		RuntimeExternalDependencies: []string{}, MovieAudioGames: []string{"finale"},
		MS2MoviePolicy: "preserve MOVIE_EN; no MS2 movie-audio release objects; purge the whole verified MOVIE_EN_SUB directory plus Voice_EN.bra when selected",
		Objects:        objects, TotalPlainBytes: total,
	}
	outRaw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ReleaseStageManifest{}, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "ASSET_STAGE_MANIFEST.json"), append(outRaw, '\n'), 0o644); err != nil {
		return ReleaseStageManifest{}, err
	}
	old := outRoot + ".previous"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(outRoot); err == nil {
		if err := os.Rename(outRoot, old); err != nil {
			return ReleaseStageManifest{}, err
		}
	}
	if err := os.Rename(tmp, outRoot); err != nil {
		_ = os.Rename(old, outRoot)
		return ReleaseStageManifest{}, err
	}
	committed = true
	_ = os.RemoveAll(old)
	return result, nil
}
