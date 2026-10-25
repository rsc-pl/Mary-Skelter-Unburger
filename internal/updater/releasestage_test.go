package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStageReleaseAssetsGoPipeline(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "version.txt"), []byte("1.2.3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gdat := filepath.Join(root, ".local", "gdat_payload")
	movie := filepath.Join(root, ".local", "movie_audio_cache")
	stage := filepath.Join(root, ".local", "release_asset_stage")
	blob := []byte("compressed-entry")
	blobRel := filepath.ToSlash(filepath.Join("blobs", "ms2", "055", "000001.zlib"))
	if err := os.MkdirAll(filepath.Join(gdat, filepath.Dir(filepath.FromSlash(blobRel))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gdat, filepath.FromSlash(blobRel)), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	gm := GDATPatchManifest{SchemaVersion: 1, Archives: []GDATPatchArchive{{GameKey: "ms2", RelativePath: "masterWindows/GxArchivedFile055.dat", ChangedEntries: []GDATPatchEntry{{Index: 1, BlobFile: blobRel, StoredBytes: int64(len(blob))}}}}}
	raw, _ := json.Marshal(gm)
	if err := os.WriteFile(filepath.Join(gdat, "GDAT_PATCH_MANIFEST.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	payload := []byte("jp-audio")
	payloadPath := filepath.Join(root, ".local", "movie_audio_cache", "payload", "finale", "100001.a0.m4a")
	if err := os.MkdirAll(filepath.Dir(payloadPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	psha, _ := SHA256File(payloadPath)
	mm := map[string]any{"movie_audio_games": []string{"finale"}, "objects": []map[string]any{{"game": "finale", "target_movie": "masterWindows/MOVIE_EN/100001.mp4", "audio_ordinal": 0, "codec": "aac", "source_movie_en_sha256": "en", "source_movie_jp_sha256": "jp", "decoded_pcm_sha256_en": "pcm-en", "decoded_pcm_sha256_jp": "pcm-jp", "file": filepath.ToSlash(payloadPath), "size": len(payload), "sha256": psha}}}
	raw, _ = json.Marshal(mm)
	if err := os.WriteFile(filepath.Join(movie, "MOVIE_AUDIO_PAYLOAD_INPUT.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := StageReleaseAssets(root, gdat, movie, stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Objects) != 2 {
		t.Fatalf("objects=%d", len(m.Objects))
	}
	if m.ReleaseVersion != "1.2.3" {
		t.Fatalf("missing stage release version: %q", m.ReleaseVersion)
	}
	if m.ContainerFormat != ".unburger-assets v3" || m.ContainerCount != 1 {
		t.Fatalf("bad contract: %+v", m)
	}
	for _, o := range m.Objects {
		if _, err := os.Stat(filepath.Join(stage, "objects", filepath.FromSlash(o.LogicalPath))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := loadReleaseStageManifest(stage); err != nil {
		t.Fatal(err)
	}
}
