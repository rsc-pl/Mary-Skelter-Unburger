package maintainer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"maryskelter-unburger/internal/mskit"
)

// canonicalGraphicsBILZEncoder delegates only the canonical compression boundary
// to the retained Python/system-zlib authority. GDAT/BILZ discovery/CL3 rebuild
// remains native Go.
func canonicalGraphicsBILZEncoder(root string) mskit.CanonicalBILZEncoder {
	return func(data []byte, algo, level int) ([]byte, error) {
		cmd, err := pythonTool(root, "toolchain/python-src/scripts/graphics/canonical_bilz_wrap.py", strconv.Itoa(algo), strconv.Itoa(level))
		if err != nil {
			return nil, err
		}
		cmd.Dir = root

		cmd.Stdin = bytes.NewReader(data)
		var out, errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("canonical graphics BILZ encoder: %w: %s", err, errOut.String())
		}
		return out.Bytes(), nil
	}
}

func ExtractGraphicsTIDFile(datPath string, gdatIndex int, innerName string) ([]byte, mskit.GraphicsTIDLocation, error) {
	raw, err := os.ReadFile(datPath)
	if err != nil {
		return nil, mskit.GraphicsTIDLocation{}, err
	}
	return mskit.ExtractGraphicsTID(raw, gdatIndex, innerName)
}

// publishGraphicsGDAT writes a complete verified GDAT without ever replacing an
// existing path. Both native and legacy codecs use the same safe publisher.
// Hard-link publication is atomic and will reject a destination symlink or a
// concurrent writer. The private temporary file is cleaned on every failure.
func publishGraphicsGDAT(input, output string, rebuilt []byte) error {
	a, err := filepath.Abs(input)
	if err != nil {
		return err
	}
	b, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if a == b {
		return errors.New("graphics output aliases input")
	}
	if _, err = os.Lstat(b); err == nil {
		return fmt.Errorf("graphics output already exists: %s", b)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(b)
	// The caller creates only its requested output parent, never erases it.
	if err = os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(parent, ".graphics-gdat-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	// Preserve the established output file permissions of both producers.
	if err = f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(rebuilt); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	// Link(2) fails if the destination exists, unlike Rename(2).
	if err = os.Link(f.Name(), b); err != nil {
		return fmt.Errorf("publish graphics GDAT: %w", err)
	}
	return nil
}

// PatchGraphicsTIDFile retains the historical system-zlib reference path.
// Production callers can choose PatchGraphicsTIDFileNative explicitly.
func PatchGraphicsTIDFile(root, datPath, outPath string, gdatIndex int, innerName string, newTID []byte) (mskit.GraphicsTIDLocation, error) {
	raw, err := os.ReadFile(datPath)
	if err != nil {
		return mskit.GraphicsTIDLocation{}, err
	}
	rebuilt, loc, err := mskit.PatchGraphicsTID(raw, gdatIndex, innerName, newTID, canonicalGraphicsBILZEncoder(root))
	if err != nil {
		return mskit.GraphicsTIDLocation{}, err
	}
	return loc, publishGraphicsGDAT(datPath, outPath, rebuilt)
}
