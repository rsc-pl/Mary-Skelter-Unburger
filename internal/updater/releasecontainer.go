package updater

import (
	"fmt"
	"sort"
)

// ReleaseAssets is the authenticated runtime view of the single canonical
// .unburger-assets v3 sidecar used by the end-user updater.
type ReleaseAssets struct {
	Path   string
	Reader *AssetV3Reader
}

func OpenReleaseAssets(path string, key []byte) (*ReleaseAssets, error) {
	r, err := OpenAssetContainerV3(path, key)
	if err != nil {
		return nil, err
	}
	a := &ReleaseAssets{Path: path, Reader: r}
	if err := a.validate(); err != nil {
		r.Close()
		return nil, err
	}
	return a, nil
}

func (a *ReleaseAssets) Close() error { return a.Reader.Close() }
func (a *ReleaseAssets) ReadObject(logical string) ([]byte, error) {
	return a.Reader.ReadObject(logical)
}

func (a *ReleaseAssets) validate() error {
	m := a.Reader.Manifest
	if m.GDATManifest.SchemaVersion != 1 || len(m.GDATManifest.Archives) == 0 {
		return fmt.Errorf("release container has no canonical GDAT structural manifest")
	}
	expectedGDAT := 0
	for _, archive := range m.GDATManifest.Archives {
		if _, ok := Profiles[archive.GameKey]; !ok {
			return fmt.Errorf("release container has unknown GDAT game %q", archive.GameKey)
		}
		for _, e := range archive.ChangedEntries {
			expectedGDAT++
			logical := "gdat/" + e.BlobFile
			o := a.Reader.byPath[logical]
			if o == nil || o.Kind != "gdat_entry" || o.Game != archive.GameKey {
				return fmt.Errorf("release container missing/mismatched GDAT object %s", logical)
			}
		}
	}
	gdatCount, movieCount := 0, 0
	for _, o := range m.Objects {
		switch o.Kind {
		case "gdat_entry":
			gdatCount++
		case "movie_jp_audio":
			if o.Game != "finale" {
				return fmt.Errorf("non-Finale movie object in release container")
			}
			movieCount++
		default:
			return fmt.Errorf("unsupported release asset kind %q", o.Kind)
		}
	}
	if gdatCount != expectedGDAT {
		return fmt.Errorf("release container GDAT object count %d, expected %d", gdatCount, expectedGDAT)
	}
	if movieCount != finaleExpectedRemuxMovies {
		return fmt.Errorf("release container has %d Finale movie objects, expected %d", movieCount, finaleExpectedRemuxMovies)
	}
	return nil
}

func (a *ReleaseAssets) GDATArchives(game string) []GDATPatchArchive {
	var out []GDATPatchArchive
	for _, p := range a.Reader.Manifest.GDATManifest.Archives {
		if p.GameKey == game {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelativePath < out[j].RelativePath })
	return out
}

func (a *ReleaseAssets) FinaleRemuxAssets() ([]FinaleRemuxAsset, error) {
	seen := map[string]bool{}
	var out []FinaleRemuxAsset
	for _, o := range a.Reader.Manifest.Objects {
		if o.Kind != "movie_jp_audio" {
			continue
		}
		s := o.Stage
		if o.Game != "finale" || s.Game != "finale" || s.AudioOrdinal != 0 || s.Codec != "aac" {
			return nil, fmt.Errorf("unsupported Finale movie object %s", o.LogicalPath)
		}
		if s.TargetMovie == "" || seen[s.TargetMovie] {
			return nil, fmt.Errorf("invalid/duplicate Finale movie target %q", s.TargetMovie)
		}
		seen[s.TargetMovie] = true
		if s.SourceMovieENSHA256 == "" || s.SHA256 == "" || s.RemuxTargetSHA256 == "" || s.RemuxTargetSize <= 0 || s.SourceVideoSHA256 == "" {
			return nil, fmt.Errorf("Finale remux metadata incomplete for %s", s.TargetMovie)
		}
		if s.SHA256 != o.SHA256 || s.Size != o.PlainSize {
			return nil, fmt.Errorf("Finale movie object/stage identity mismatch %s", o.LogicalPath)
		}
		out = append(out, FinaleRemuxAsset{
			RelativePath: s.TargetMovie, SourceSHA256: s.SourceMovieENSHA256,
			PayloadObject: o.LogicalPath, PayloadSHA256: s.SHA256,
			TargetSHA256: s.RemuxTargetSHA256, TargetSize: s.RemuxTargetSize,
			SourceVideoSHA256: s.SourceVideoSHA256,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelativePath < out[j].RelativePath })
	if len(out) != finaleExpectedRemuxMovies {
		return nil, fmt.Errorf("release container has %d remux movies; expected %d", len(out), finaleExpectedRemuxMovies)
	}
	return out, nil
}
