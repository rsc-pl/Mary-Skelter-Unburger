package updater

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"maryskelter-unburger/internal/mskit"
)

// GDAT structural patches are the preferred DAT distribution format.
// They replace only source-pinned GDAT entries and copy every unrelated entry
// verbatim from the user's archive. This makes a patch tolerant of unrelated
// archive changes while still failing closed if a managed entry differs.

type GDATPatchEntry struct {
	Index                int      `json:"index"`
	SourceSHA256         string   `json:"source_sha256"`
	AcceptedSourceSHA256 []string `json:"accepted_source_sha256,omitempty"`
	TargetSHA256         string   `json:"target_sha256"`
	SourceSize           int64    `json:"source_size"`
	TargetSize           int64    `json:"target_size"`
	BlobFile             string   `json:"blob_file"`
	StoredBytes          int64    `json:"stored_bytes"`
}

type GDATPatchArchive struct {
	GameKey               string           `json:"game"`
	RelativePath          string           `json:"relative_path"`
	EntryCount            int              `json:"entry_count"`
	CanonicalSourceSHA256 string           `json:"canonical_source_sha256"`
	CanonicalTargetSHA256 string           `json:"canonical_target_sha256"`
	CanonicalSourceSize   int64            `json:"canonical_source_size"`
	CanonicalTargetSize   int64            `json:"canonical_target_size"`
	ChangedEntries        []GDATPatchEntry `json:"changed_entries"`
	StoredBytes           int64            `json:"stored_bytes"`
}

type GDATPatchManifest struct {
	SchemaVersion    int                `json:"schema_version"`
	Archives         []GDATPatchArchive `json:"archives"`
	TotalStoredBytes int64              `json:"total_stored_bytes"`
}

type gdatView struct {
	Raw     []byte
	Entries [][]byte
}

func parseGDAT(raw []byte) (gdatView, error) {
	v, err := mskit.ParseGDAT(raw)
	if err != nil {
		return gdatView{}, err
	}
	return gdatView{Raw: v.Raw, Entries: v.Entries}, nil
}

func buildGDAT(entries [][]byte) ([]byte, error) {
	return mskit.BuildGDAT(entries)
}

func bytesSHA(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func compressEntry(data []byte) ([]byte, error) {
	var out bytes.Buffer
	zw, err := zlib.NewWriterLevel(&out, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(data); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func readCompressedEntry(path string, expected int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	data, err := io.ReadAll(io.LimitReader(zr, expected+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != expected {
		return nil, fmt.Errorf("decoded entry size %d, expected %d", len(data), expected)
	}
	return data, nil
}

func BuildGDATPatch(source, target, payloadRoot, gameKey, relativePath string) (GDATPatchArchive, error) {
	sourceRaw, err := os.ReadFile(source)
	if err != nil {
		return GDATPatchArchive{}, err
	}
	targetRaw, err := os.ReadFile(target)
	if err != nil {
		return GDATPatchArchive{}, err
	}
	src, err := parseGDAT(sourceRaw)
	if err != nil {
		return GDATPatchArchive{}, fmt.Errorf("source %s: %w", source, err)
	}
	dst, err := parseGDAT(targetRaw)
	if err != nil {
		return GDATPatchArchive{}, fmt.Errorf("target %s: %w", target, err)
	}
	if len(src.Entries) != len(dst.Entries) {
		return GDATPatchArchive{}, fmt.Errorf("GDAT entry count changed: %d -> %d", len(src.Entries), len(dst.Entries))
	}
	sourceSHA, sourceSize, err := hashAndSize(source)
	if err != nil {
		return GDATPatchArchive{}, err
	}
	targetSHA, targetSize, err := hashAndSize(target)
	if err != nil {
		return GDATPatchArchive{}, err
	}
	item := GDATPatchArchive{
		GameKey: gameKey, RelativePath: filepath.ToSlash(relativePath), EntryCount: len(src.Entries),
		CanonicalSourceSHA256: sourceSHA, CanonicalTargetSHA256: targetSHA,
		CanonicalSourceSize: sourceSize, CanonicalTargetSize: targetSize,
	}
	for i := range src.Entries {
		if bytes.Equal(src.Entries[i], dst.Entries[i]) {
			continue
		}
		packed, err := compressEntry(dst.Entries[i])
		if err != nil {
			return GDATPatchArchive{}, err
		}
		blobRel := filepath.ToSlash(filepath.Join("pc", gameKey, filepath.Base(relativePath)+".entries", fmt.Sprintf("%06d.zlib", i)))
		blob := filepath.Join(payloadRoot, filepath.FromSlash(blobRel))
		if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
			return GDATPatchArchive{}, err
		}
		if err := os.WriteFile(blob, packed, 0o644); err != nil {
			return GDATPatchArchive{}, err
		}
		e := GDATPatchEntry{
			Index:        i,
			SourceSHA256: bytesSHA(src.Entries[i]), TargetSHA256: bytesSHA(dst.Entries[i]),
			SourceSize: int64(len(src.Entries[i])), TargetSize: int64(len(dst.Entries[i])),
			BlobFile: blobRel, StoredBytes: int64(len(packed)),
		}
		item.ChangedEntries = append(item.ChangedEntries, e)
		item.StoredBytes += int64(len(packed))
	}
	// Keep deterministic file order even if the implementation above changes.
	sort.Slice(item.ChangedEntries, func(i, j int) bool { return item.ChangedEntries[i].Index < item.ChangedEntries[j].Index })
	return item, nil
}

func ApplyGDATPatch(source, out, payloadRoot string, patch GDATPatchArchive) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	view, err := parseGDAT(raw)
	if err != nil {
		return err
	}
	if len(view.Entries) != patch.EntryCount {
		return fmt.Errorf("%s: GDAT entry count %d, expected %d", source, len(view.Entries), patch.EntryCount)
	}
	entries := make([][]byte, len(view.Entries))
	copy(entries, view.Entries)
	seen := map[int]bool{}
	for _, pe := range patch.ChangedEntries {
		if pe.Index < 0 || pe.Index >= len(entries) || seen[pe.Index] {
			return fmt.Errorf("invalid/duplicate GDAT patch index %d", pe.Index)
		}
		seen[pe.Index] = true
		current := entries[pe.Index]
		got := bytesSHA(current)
		if got == pe.TargetSHA256 && int64(len(current)) == pe.TargetSize {
			continue // already patched to this content revision
		}
		accepted := got == pe.SourceSHA256 && int64(len(current)) == pe.SourceSize
		if !accepted {
			for _, prior := range pe.AcceptedSourceSHA256 {
				if got == prior {
					accepted = true
					break
				}
			}
		}
		if !accepted {
			return fmt.Errorf("GDAT entry %d source mismatch: got sha=%s size=%d; not pristine, current target, or a recorded prior mod target", pe.Index, got, len(current))
		}
		replacement, err := readCompressedEntry(filepath.Join(payloadRoot, filepath.FromSlash(pe.BlobFile)), pe.TargetSize)
		if err != nil {
			return fmt.Errorf("GDAT entry %d payload: %w", pe.Index, err)
		}
		if got := bytesSHA(replacement); got != pe.TargetSHA256 {
			return fmt.Errorf("GDAT entry %d target payload SHA mismatch: %s", pe.Index, got)
		}
		entries[pe.Index] = replacement
	}
	rebuilt, err := buildGDAT(entries)
	if err != nil {
		return err
	}
	// On the canonical pristine source we require the canonical whole-archive output.
	if bytesSHA(raw) == patch.CanonicalSourceSHA256 {
		if got := bytesSHA(rebuilt); got != patch.CanonicalTargetSHA256 {
			return fmt.Errorf("canonical GDAT rebuild SHA mismatch: %s", got)
		}
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, rebuilt, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func BuildGDATPatchPayload(sourceCache, bundleRoot, outRoot string, gameKeys []string, lineage *ContentLineage) (GDATPatchManifest, error) {
	if len(gameKeys) == 0 {
		gameKeys = []string{"ms2", "finale"}
	}
	sort.Strings(gameKeys)
	manifest := GDATPatchManifest{SchemaVersion: 1}
	for _, key := range gameKeys {
		p, ok := Profiles[key]
		if !ok {
			return GDATPatchManifest{}, fmt.Errorf("unknown game profile %q", key)
		}
		bundle, err := LoadBundle(bundleRoot, p)
		if err != nil {
			return GDATPatchManifest{}, err
		}
		names := make([]string, 0, len(bundle.DatFiles))
		for name := range bundle.DatFiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			source := filepath.Join(sourceCache, "pristine", key, "masterWindows", name)
			if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
				return GDATPatchManifest{}, fmt.Errorf("missing pristine GDAT source %s", source)
			}
			rel := filepath.ToSlash(filepath.Join("masterWindows", name))
			item, err := BuildGDATPatch(source, bundle.DatFiles[name], outRoot, key, rel)
			if err != nil {
				return GDATPatchManifest{}, fmt.Errorf("%s %s: %w", key, name, err)
			}
			if lineage != nil {
				for i := range item.ChangedEntries {
					e := &item.ChangedEntries[i]
					for _, prior := range lineage.GDATPriorSources(key, rel, e.Index) {
						if prior != e.SourceSHA256 && prior != e.TargetSHA256 {
							e.AcceptedSourceSHA256 = append(e.AcceptedSourceSHA256, prior)
						}
					}
				}
			}
			// Generator roundtrip: canonical source must reproduce exact canonical target.
			verify := filepath.Join(outRoot, ".verify", key, name)
			if err := ApplyGDATPatch(source, verify, outRoot, item); err != nil {
				return GDATPatchManifest{}, fmt.Errorf("verify %s %s: %w", key, name, err)
			}
			got, _, err := hashAndSize(verify)
			if err != nil {
				return GDATPatchManifest{}, err
			}
			_ = os.Remove(verify)
			if got != item.CanonicalTargetSHA256 {
				return GDATPatchManifest{}, fmt.Errorf("verify %s %s produced %s", key, name, got)
			}
			manifest.Archives = append(manifest.Archives, item)
			manifest.TotalStoredBytes += item.StoredBytes
		}
	}
	_ = os.RemoveAll(filepath.Join(outRoot, ".verify"))
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return GDATPatchManifest{}, err
	}
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return GDATPatchManifest{}, err
	}
	if err := os.WriteFile(filepath.Join(outRoot, "GDAT_PATCH_MANIFEST.json"), append(raw, '\n'), 0o644); err != nil {
		return GDATPatchManifest{}, err
	}
	return manifest, nil
}
