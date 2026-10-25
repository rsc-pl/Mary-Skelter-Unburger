package updater

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const (
	deltaMagic        = "MSDLT1\r\n"
	deltaMinChunk     = 8 << 10
	deltaAvgChunk     = 32 << 10
	deltaMaxChunk     = 128 << 10
	deltaLiteralFlush = 8 << 20
)

var deltaGear = func() [256]uint64 {
	var out [256]uint64
	x := uint64(0x6d617279736b656c)
	for i := range out {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		out[i] = z ^ (z >> 31)
	}
	return out
}()

type DeltaHeader struct {
	SchemaVersion int    `json:"schema_version"`
	SourceSHA256  string `json:"source_sha256"`
	TargetSHA256  string `json:"target_sha256"`
	SourceSize    int64  `json:"source_size"`
	TargetSize    int64  `json:"target_size"`
	MinChunk      int    `json:"min_chunk"`
	AvgChunk      int    `json:"avg_chunk"`
	MaxChunk      int    `json:"max_chunk"`
}

type DeltaStats struct {
	CopyCommands       int   `json:"copy_commands"`
	LiteralCommands    int   `json:"literal_commands"`
	CopiedBytes        int64 `json:"copied_bytes"`
	LiteralBytes       int64 `json:"literal_bytes"`
	LiteralStoredBytes int64 `json:"literal_stored_bytes"`
	PatchBytes         int64 `json:"patch_bytes"`
}

type DeltaManifestEntry struct {
	GameKey      string      `json:"game"`
	RelativePath string      `json:"relative_path"`
	PatchFile    string      `json:"patch_file"`
	Header       DeltaHeader `json:"header"`
	Stats        DeltaStats  `json:"stats"`
}

type DeltaManifest struct {
	SchemaVersion int                  `json:"schema_version"`
	Entries       []DeltaManifestEntry `json:"entries"`
	TotalBytes    int64                `json:"total_patch_bytes"`
}

type chunkKey struct {
	Hash [32]byte
	Len  uint32
}

type chunkRef struct {
	Offset int64
	Len    uint32
}

func walkChunks(path string, fn func(int64, []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	chunk := make([]byte, 0, deltaAvgChunk)
	var off int64
	var h uint64
	flush := func() error {
		if len(chunk) == 0 {
			return nil
		}
		if err := fn(off-int64(len(chunk)), chunk); err != nil {
			return err
		}
		chunk = make([]byte, 0, deltaAvgChunk)
		h = 0
		return nil
	}
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				chunk = append(chunk, b)
				off++
				h = (h << 1) + deltaGear[b]
				l := len(chunk)
				if l >= deltaMinChunk && ((h&uint64(deltaAvgChunk-1)) == 0 || l >= deltaMaxChunk) {
					if err := flush(); err != nil {
						return err
					}
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return flush()
}

func hashAndSize(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func BuildDelta(source, target, patch string) (DeltaHeader, DeltaStats, error) {
	srcSHA, srcSize, err := hashAndSize(source)
	if err != nil {
		return DeltaHeader{}, DeltaStats{}, err
	}
	dstSHA, dstSize, err := hashAndSize(target)
	if err != nil {
		return DeltaHeader{}, DeltaStats{}, err
	}

	index := map[chunkKey]chunkRef{}
	if err := walkChunks(source, func(off int64, data []byte) error {
		key := chunkKey{Hash: sha256.Sum256(data), Len: uint32(len(data))}
		if _, ok := index[key]; !ok {
			index[key] = chunkRef{Offset: off, Len: uint32(len(data))}
		}
		return nil
	}); err != nil {
		return DeltaHeader{}, DeltaStats{}, err
	}

	dir := filepath.Dir(patch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return DeltaHeader{}, DeltaStats{}, err
	}
	bodyPath := patch + ".body.tmp"
	body, err := os.Create(bodyPath)
	if err != nil {
		return DeltaHeader{}, DeltaStats{}, err
	}
	bw := bufio.NewWriterSize(body, 1<<20)
	stats := DeltaStats{}
	literal := make([]byte, 0, deltaLiteralFlush)
	flushLiteral := func() error {
		if len(literal) == 0 {
			return nil
		}
		var z bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
		if _, err := zw.Write(literal); err != nil {
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if err := bw.WriteByte(2); err != nil {
			return err
		}
		if err := binary.Write(bw, binary.LittleEndian, uint32(len(literal))); err != nil {
			return err
		}
		if err := binary.Write(bw, binary.LittleEndian, uint32(z.Len())); err != nil {
			return err
		}
		if _, err := bw.Write(z.Bytes()); err != nil {
			return err
		}
		stats.LiteralCommands++
		stats.LiteralBytes += int64(len(literal))
		stats.LiteralStoredBytes += int64(z.Len())
		literal = literal[:0]
		return nil
	}
	scanErr := walkChunks(target, func(_ int64, data []byte) error {
		key := chunkKey{Hash: sha256.Sum256(data), Len: uint32(len(data))}
		if ref, ok := index[key]; ok {
			if err := flushLiteral(); err != nil {
				return err
			}
			if err := bw.WriteByte(1); err != nil {
				return err
			}
			if err := binary.Write(bw, binary.LittleEndian, uint64(ref.Offset)); err != nil {
				return err
			}
			if err := binary.Write(bw, binary.LittleEndian, ref.Len); err != nil {
				return err
			}
			stats.CopyCommands++
			stats.CopiedBytes += int64(ref.Len)
			return nil
		}
		literal = append(literal, data...)
		if len(literal) >= deltaLiteralFlush {
			return flushLiteral()
		}
		return nil
	})
	if scanErr == nil {
		scanErr = flushLiteral()
	}
	if scanErr == nil {
		scanErr = bw.WriteByte(0)
	}
	if scanErr == nil {
		scanErr = bw.Flush()
	}
	closeErr := body.Close()
	if scanErr != nil {
		_ = os.Remove(bodyPath)
		return DeltaHeader{}, DeltaStats{}, scanErr
	}
	if closeErr != nil {
		_ = os.Remove(bodyPath)
		return DeltaHeader{}, DeltaStats{}, closeErr
	}

	header := DeltaHeader{SchemaVersion: 1, SourceSHA256: srcSHA, TargetSHA256: dstSHA, SourceSize: srcSize, TargetSize: dstSize,
		MinChunk: deltaMinChunk, AvgChunk: deltaAvgChunk, MaxChunk: deltaMaxChunk}
	rawHeader, _ := json.Marshal(header)
	outTmp := patch + ".tmp"
	out, err := os.Create(outTmp)
	if err != nil {
		_ = os.Remove(bodyPath)
		return DeltaHeader{}, DeltaStats{}, err
	}
	_, err = io.WriteString(out, deltaMagic)
	if err == nil {
		err = binary.Write(out, binary.LittleEndian, uint32(len(rawHeader)))
	}
	if err == nil {
		_, err = out.Write(rawHeader)
	}
	if err == nil {
		in, e := os.Open(bodyPath)
		if e != nil {
			err = e
		} else {
			_, err = io.Copy(out, in)
			_ = in.Close()
		}
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	_ = os.Remove(bodyPath)
	if err != nil {
		_ = os.Remove(outTmp)
		return DeltaHeader{}, DeltaStats{}, err
	}
	if err := os.Rename(outTmp, patch); err != nil {
		return DeltaHeader{}, DeltaStats{}, err
	}
	info, err := os.Stat(patch)
	if err != nil {
		return DeltaHeader{}, DeltaStats{}, err
	}
	stats.PatchBytes = info.Size()
	return header, stats, nil
}

func readDeltaHeader(r io.Reader) (DeltaHeader, error) {
	magic := make([]byte, len(deltaMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return DeltaHeader{}, err
	}
	if string(magic) != deltaMagic {
		return DeltaHeader{}, fmt.Errorf("not a Mary Skelter delta payload")
	}
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return DeltaHeader{}, err
	}
	if n == 0 || n > 1<<20 {
		return DeltaHeader{}, fmt.Errorf("invalid delta header length %d", n)
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return DeltaHeader{}, err
	}
	var h DeltaHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return DeltaHeader{}, err
	}
	if h.SchemaVersion != 1 {
		return DeltaHeader{}, fmt.Errorf("unsupported delta schema %d", h.SchemaVersion)
	}
	return h, nil
}

func ApplyDelta(source, patch, output string) (DeltaHeader, error) {
	srcSHA, srcSize, err := hashAndSize(source)
	if err != nil {
		return DeltaHeader{}, err
	}
	pf, err := os.Open(patch)
	if err != nil {
		return DeltaHeader{}, err
	}
	defer pf.Close()
	h, err := readDeltaHeader(pf)
	if err != nil {
		return DeltaHeader{}, err
	}
	if srcSHA != h.SourceSHA256 || srcSize != h.SourceSize {
		return DeltaHeader{}, fmt.Errorf("delta source mismatch: size=%d sha=%s", srcSize, srcSHA)
	}
	src, err := os.Open(source)
	if err != nil {
		return DeltaHeader{}, err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return DeltaHeader{}, err
	}
	tmp := output + ".delta.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return DeltaHeader{}, err
	}
	br := bufio.NewReaderSize(pf, 1<<20)
	var written int64
	fail := func(e error) (DeltaHeader, error) { _ = out.Close(); _ = os.Remove(tmp); return DeltaHeader{}, e }
	for {
		op, err := br.ReadByte()
		if err != nil {
			return fail(err)
		}
		switch op {
		case 0:
			if err := out.Close(); err != nil {
				return DeltaHeader{}, err
			}
			if written != h.TargetSize {
				_ = os.Remove(tmp)
				return DeltaHeader{}, fmt.Errorf("delta output size %d != %d", written, h.TargetSize)
			}
			got, _, err := hashAndSize(tmp)
			if err != nil {
				_ = os.Remove(tmp)
				return DeltaHeader{}, err
			}
			if got != h.TargetSHA256 {
				_ = os.Remove(tmp)
				return DeltaHeader{}, fmt.Errorf("delta output SHA-256 %s != %s", got, h.TargetSHA256)
			}
			if err := os.Rename(tmp, output); err != nil {
				_ = os.Remove(tmp)
				return DeltaHeader{}, err
			}
			return h, nil
		case 1:
			var off uint64
			var n uint32
			if err := binary.Read(br, binary.LittleEndian, &off); err != nil {
				return fail(err)
			}
			if err := binary.Read(br, binary.LittleEndian, &n); err != nil {
				return fail(err)
			}
			if off+uint64(n) > uint64(h.SourceSize) {
				return fail(fmt.Errorf("delta copy outside source"))
			}
			m, err := io.CopyN(out, io.NewSectionReader(src, int64(off), int64(n)), int64(n))
			written += m
			if err != nil {
				return fail(err)
			}
		case 2:
			var rawN, compN uint32
			if err := binary.Read(br, binary.LittleEndian, &rawN); err != nil {
				return fail(err)
			}
			if err := binary.Read(br, binary.LittleEndian, &compN); err != nil {
				return fail(err)
			}
			if rawN > deltaLiteralFlush+deltaMaxChunk || compN > rawN+1<<20 {
				return fail(fmt.Errorf("implausible delta literal sizes"))
			}
			lr := &io.LimitedReader{R: br, N: int64(compN)}
			zr, err := zlib.NewReader(lr)
			if err != nil {
				return fail(err)
			}
			raw, readErr := io.ReadAll(zr)
			closeErr := zr.Close()
			if readErr != nil {
				return fail(readErr)
			}
			if closeErr != nil {
				return fail(closeErr)
			}
			if lr.N != 0 || len(raw) != int(rawN) {
				return fail(fmt.Errorf("delta literal length mismatch"))
			}
			m, err := out.Write(raw)
			written += int64(m)
			if err != nil {
				return fail(err)
			}
		default:
			return fail(fmt.Errorf("unknown delta opcode %d", op))
		}
	}
}

func BuildDeltaPayload(sourceCache, bundleRoot, outRoot string, gameKeys []string) (DeltaManifest, error) {
	if len(gameKeys) == 0 {
		gameKeys = []string{"ms2", "finale"}
	}
	sort.Strings(gameKeys)
	manifest := DeltaManifest{SchemaVersion: 1}
	for _, key := range gameKeys {
		p, ok := Profiles[key]
		if !ok {
			return DeltaManifest{}, fmt.Errorf("unknown game profile %q", key)
		}
		bundle, err := LoadBundle(bundleRoot, p)
		if err != nil {
			return DeltaManifest{}, err
		}
		names := make([]string, 0, len(bundle.DatFiles))
		for name := range bundle.DatFiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			source := filepath.Join(sourceCache, "pristine", key, "masterWindows", name)
			if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
				return DeltaManifest{}, fmt.Errorf("missing pristine delta source %s", source)
			}
			relPatch := filepath.ToSlash(filepath.Join("pc", key, name+".msdelta"))
			patch := filepath.Join(outRoot, filepath.FromSlash(relPatch))
			h, stats, err := BuildDelta(source, bundle.DatFiles[name], patch)
			if err != nil {
				return DeltaManifest{}, fmt.Errorf("%s %s: %w", key, name, err)
			}
			verify := patch + ".verify.tmp"
			if _, err := ApplyDelta(source, patch, verify); err != nil {
				return DeltaManifest{}, fmt.Errorf("verify %s %s: %w", key, name, err)
			}
			got, _, _ := hashAndSize(verify)
			_ = os.Remove(verify)
			if got != h.TargetSHA256 {
				return DeltaManifest{}, fmt.Errorf("verify %s %s produced %s", key, name, got)
			}
			manifest.Entries = append(manifest.Entries, DeltaManifestEntry{GameKey: key, RelativePath: "masterWindows/" + name, PatchFile: relPatch, Header: h, Stats: stats})
			manifest.TotalBytes += stats.PatchBytes
		}
	}
	raw, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.MkdirAll(outRoot, 0o755); err != nil {
		return DeltaManifest{}, err
	}
	if err := os.WriteFile(filepath.Join(outRoot, "DAT_DELTA_MANIFEST.json"), append(raw, '\n'), 0o644); err != nil {
		return DeltaManifest{}, err
	}
	return manifest, nil
}
