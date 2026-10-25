package updater

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func gdatEntryState(current []byte, pe GDATPatchEntry) (target, accepted bool) {
	got := bytesSHA(current)
	if got == pe.TargetSHA256 && int64(len(current)) == pe.TargetSize {
		return true, true
	}
	if got == pe.SourceSHA256 && int64(len(current)) == pe.SourceSize {
		return false, true
	}
	for _, prior := range pe.AcceptedSourceSHA256 {
		if got == prior {
			return false, true
		}
	}
	return false, false
}

// InspectGDATPatchState validates managed entries without caring about unrelated
// archive bytes. It accepts pristine, current target, or recorded prior targets.
func InspectGDATPatchState(path string, patch GDATPatchArchive) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	view, err := parseGDAT(raw)
	if err != nil {
		return false, err
	}
	if len(view.Entries) != patch.EntryCount {
		return false, fmt.Errorf("%s: GDAT entry count %d, expected %d", path, len(view.Entries), patch.EntryCount)
	}
	needs := false
	seen := map[int]bool{}
	for _, pe := range patch.ChangedEntries {
		if pe.Index < 0 || pe.Index >= len(view.Entries) || seen[pe.Index] {
			return false, fmt.Errorf("invalid/duplicate GDAT patch index %d", pe.Index)
		}
		seen[pe.Index] = true
		target, accepted := gdatEntryState(view.Entries[pe.Index], pe)
		if !accepted {
			return false, fmt.Errorf("GDAT entry %d source mismatch: got sha=%s size=%d", pe.Index, bytesSHA(view.Entries[pe.Index]), len(view.Entries[pe.Index]))
		}
		if !target {
			needs = true
		}
	}
	return needs, nil
}

func decodeGDATObject(blob []byte, expected int64) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(blob))
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

// ApplyGDATPatchObjects materializes only this archive's authenticated compressed
// entry blobs to a temporary directory, then reuses the proven structural patcher.
func ApplyGDATPatchObjects(source, out string, patch GDATPatchArchive, readObject func(string) ([]byte, error)) error {
	tmp, err := os.MkdirTemp(filepath.Dir(out), ".unburger-gdat-payload-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for _, pe := range patch.ChangedEntries {
		logical := filepath.ToSlash(filepath.Join("gdat", filepath.FromSlash(pe.BlobFile)))
		packed, err := readObject(logical)
		if err != nil {
			return fmt.Errorf("GDAT entry %d container object %s: %w", pe.Index, logical, err)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(pe.BlobFile))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, packed, 0o600); err != nil {
			return err
		}
	}
	return ApplyGDATPatch(source, out, tmp, patch)
}
