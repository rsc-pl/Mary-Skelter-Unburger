package updater

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	assetsV3HeaderSize = 96
	assetsV3ChunkSize  = 1 << 20
	assetsV3Version    = 3
)

var assetsV3Magic = [8]byte{'U', 'N', 'B', 'A', 'S', 'V', '3', 0}

type AssetV3Manifest struct {
	ReleaseVersion  string               `json:"release_version,omitempty"`
	SchemaVersion   int                  `json:"schema_version"`
	ContainerFormat string               `json:"container_format"`
	ContainerID     string               `json:"container_id"`
	TotalPlainBytes int64                `json:"total_plain_bytes"`
	StageManifest   ReleaseStageManifest `json:"stage_manifest"`
	GDATManifest    GDATPatchManifest    `json:"gdat_manifest"`
	Objects         []AssetV3Object      `json:"objects"`
}

type AssetV3Object struct {
	ID             string             `json:"id"`
	LogicalPath    string             `json:"logical_path"`
	Kind           string             `json:"kind"`
	Game           string             `json:"game"`
	PlainSize      int64              `json:"plain_size"`
	SHA256         string             `json:"sha256"`
	CompressedSize int64              `json:"compressed_size"`
	Chunks         []AssetV3Chunk     `json:"chunks"`
	Stage          ReleaseStageObject `json:"stage"`
}

type AssetV3Chunk struct {
	Offset          int64 `json:"offset"`
	StoredSize      int64 `json:"stored_size"`
	CompressedBytes int64 `json:"compressed_bytes"`
}

type assetV3Header struct {
	Flags              uint32
	ChunkSize          uint32
	ObjectCount        uint32
	ManifestOffset     uint64
	ManifestStoredSize uint64
	ManifestPlainSize  uint64
	TotalPlainBytes    uint64
	ContainerID        [16]byte
	ManifestNonce      [12]byte
}

type AssetV3Reader struct {
	f         *os.File
	header    assetV3Header
	chunkAEAD cipher.AEAD
	Manifest  AssetV3Manifest
	byPath    map[string]*AssetV3Object
}

func deriveAssetV3Key(master []byte, label string, id [16]byte) ([]byte, error) {
	if len(master) != 32 {
		return nil, fmt.Errorf("asset key must be exactly 32 bytes")
	}
	h := hmac.New(sha256.New, master)
	h.Write([]byte("unburger-assets-v3/" + label + "/"))
	h.Write(id[:])
	return h.Sum(nil), nil
}

func assetV3AEAD(key []byte) (cipher.AEAD, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func encodeAssetV3Header(h assetV3Header) []byte {
	b := make([]byte, assetsV3HeaderSize)
	copy(b[:8], assetsV3Magic[:])
	binary.LittleEndian.PutUint32(b[8:12], assetsV3Version)
	binary.LittleEndian.PutUint32(b[12:16], assetsV3HeaderSize)
	binary.LittleEndian.PutUint32(b[16:20], h.Flags)
	binary.LittleEndian.PutUint32(b[20:24], h.ChunkSize)
	binary.LittleEndian.PutUint32(b[24:28], h.ObjectCount)
	binary.LittleEndian.PutUint64(b[32:40], h.ManifestOffset)
	binary.LittleEndian.PutUint64(b[40:48], h.ManifestStoredSize)
	binary.LittleEndian.PutUint64(b[48:56], h.ManifestPlainSize)
	binary.LittleEndian.PutUint64(b[56:64], h.TotalPlainBytes)
	copy(b[64:80], h.ContainerID[:])
	copy(b[80:92], h.ManifestNonce[:])
	return b
}

func decodeAssetV3Header(b []byte) (assetV3Header, error) {
	var h assetV3Header
	if len(b) != assetsV3HeaderSize || !bytes.Equal(b[:8], assetsV3Magic[:]) {
		return h, fmt.Errorf("not an .unburger-assets v3 container")
	}
	if binary.LittleEndian.Uint32(b[8:12]) != assetsV3Version || binary.LittleEndian.Uint32(b[12:16]) != assetsV3HeaderSize {
		return h, fmt.Errorf("unsupported .unburger-assets version/header")
	}
	h.Flags = binary.LittleEndian.Uint32(b[16:20])
	h.ChunkSize = binary.LittleEndian.Uint32(b[20:24])
	h.ObjectCount = binary.LittleEndian.Uint32(b[24:28])
	h.ManifestOffset = binary.LittleEndian.Uint64(b[32:40])
	h.ManifestStoredSize = binary.LittleEndian.Uint64(b[40:48])
	h.ManifestPlainSize = binary.LittleEndian.Uint64(b[48:56])
	h.TotalPlainBytes = binary.LittleEndian.Uint64(b[56:64])
	copy(h.ContainerID[:], b[64:80])
	copy(h.ManifestNonce[:], b[80:92])
	if h.ChunkSize != assetsV3ChunkSize || h.ManifestOffset < assetsV3HeaderSize || h.ManifestStoredSize < 16 {
		return h, fmt.Errorf("invalid .unburger-assets header")
	}
	return h, nil
}

func assetV3ChunkAAD(containerID, objectID [16]byte, index uint32, n int64) []byte {
	b := make([]byte, 0, 8+16+16+4+8)
	b = append(b, assetsV3Magic[:]...)
	b = append(b, containerID[:]...)
	b = append(b, objectID[:]...)
	var x [12]byte
	binary.LittleEndian.PutUint32(x[:4], index)
	binary.LittleEndian.PutUint64(x[4:], uint64(n))
	return append(b, x[:]...)
}

func random16() ([16]byte, error) {
	var x [16]byte
	_, err := io.ReadFull(rand.Reader, x[:])
	return x, err
}

func LoadOrCreateAssetV3Key(path string) ([]byte, bool, error) {
	if b, err := os.ReadFile(path); err == nil {
		if len(b) != 32 {
			return nil, false, fmt.Errorf("%s must contain exactly 32 raw key bytes", path)
		}
		return b, false, nil
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, false, err
	}
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, false, err
	}
	return b, true, nil
}

func assetZstdFlags() ([]string, error) {
	switch os.Getenv("MARY_SKELTER_ASSETS_ZSTD_LEVEL") {
	case "", "19":
		return []string{"-19"}, nil
	case "22":
		return []string{"--ultra", "-22"}, nil
	default:
		return nil, fmt.Errorf("MARY_SKELTER_ASSETS_ZSTD_LEVEL must be 19 or 22")
	}
}

func zstdCompressStage(objectsRoot, tempRoot string, objects []ReleaseStageObject) error {
	zstd, err := exec.LookPath("zstd")
	if err != nil {
		return fmt.Errorf("maintainer zstd is required to build .unburger-assets v3: %w", err)
	}
	objectsRoot, err = filepath.Abs(objectsRoot)
	if err != nil {
		return fmt.Errorf("resolve stage object root: %w", err)
	}
	tempRoot, err = filepath.Abs(tempRoot)
	if err != nil {
		return fmt.Errorf("resolve zstd output root: %w", err)
	}
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		return err
	}
	list := filepath.Join(tempRoot, "filelist.txt")
	var lines strings.Builder
	for _, o := range objects {
		if !filepath.IsLocal(filepath.FromSlash(o.LogicalPath)) {
			return fmt.Errorf("unsafe stage logical path %q", o.LogicalPath)
		}
		lines.WriteString(filepath.FromSlash(o.LogicalPath))
		lines.WriteByte('\n')
	}
	if err := os.WriteFile(list, []byte(lines.String()), 0o600); err != nil {
		return err
	}
	flags, err := assetZstdFlags()
	if err != nil {
		return err
	}
	cmd := exec.Command(zstd, append([]string{"-q", "-f"}, append(flags, "--no-check", "--filelist", list, "--output-dir-mirror", tempRoot)...)...)
	cmd.Dir = objectsRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zstd-19 stage compression: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func zstdCompressOne(src, dst string) error {
	zstd, err := exec.LookPath("zstd")
	if err != nil {
		return err
	}
	flags, err := assetZstdFlags()
	if err != nil {
		return err
	}
	cmd := exec.Command(zstd, append([]string{"-q", "-f"}, append(flags, "--no-check", src, "-o", dst)...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zstd-19 manifest compression: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func zstdDecodeStdlib(frame []byte, expected uint64) ([]byte, error) {
	const eh, sh, ch = 64, 64, 24
	if expected > 1<<34 {
		return nil, fmt.Errorf("zstd decoded size too large: %d", expected)
	}
	off := eh + 2*sh
	raw := make([]byte, off+ch+len(frame))
	b := binary.LittleEndian
	copy(raw[:4], []byte{0x7f, 'E', 'L', 'F'})
	raw[4] = 2
	raw[5] = 1
	raw[6] = 1
	b.PutUint16(raw[16:18], 1)
	b.PutUint16(raw[18:20], 62)
	b.PutUint32(raw[20:24], 1)
	b.PutUint64(raw[40:48], eh)
	b.PutUint16(raw[52:54], eh)
	b.PutUint16(raw[58:60], sh)
	b.PutUint16(raw[60:62], 2)
	p := eh + sh
	b.PutUint32(raw[p+4:p+8], uint32(elf.SHT_PROGBITS))
	b.PutUint64(raw[p+8:p+16], uint64(elf.SHF_COMPRESSED))
	b.PutUint64(raw[p+24:p+32], uint64(off))
	b.PutUint64(raw[p+32:p+40], uint64(ch+len(frame)))
	b.PutUint64(raw[p+48:p+56], 1)
	b.PutUint32(raw[off:off+4], uint32(elf.COMPRESS_ZSTD))
	b.PutUint64(raw[off+8:off+16], expected)
	b.PutUint64(raw[off+16:off+24], 1)
	copy(raw[off+ch:], frame)
	f, err := elf.NewFile(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out, err := f.Sections[1].Data()
	if err != nil {
		return nil, err
	}
	if uint64(len(out)) != expected {
		return nil, fmt.Errorf("zstd decoded %d bytes, expected %d", len(out), expected)
	}
	return out, nil
}

func BuildAssetContainerV3(stageRoot, out, keyPath string) (AssetV3Manifest, error) {
	stage, err := loadReleaseStageManifest(stageRoot)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	if err := ValidateReleaseVersion(stage.ReleaseVersion); err != nil {
		return AssetV3Manifest{}, fmt.Errorf("release stage requires version.txt metadata: %w", err)
	}
	graw, err := os.ReadFile(filepath.Join(stageRoot, "inputs", "gdat", "GDAT_PATCH_MANIFEST.json"))
	if err != nil {
		return AssetV3Manifest{}, fmt.Errorf("staged GDAT manifest: %w", err)
	}
	var gm GDATPatchManifest
	if err = json.Unmarshal(graw, &gm); err != nil {
		return AssetV3Manifest{}, err
	}
	gcount := 0
	for _, a := range gm.Archives {
		gcount += len(a.ChangedEntries)
	}
	if len(stage.Objects) != gcount+finaleExpectedRemuxMovies {
		return AssetV3Manifest{}, fmt.Errorf("release stage object count %d != GDAT %d + movie %d", len(stage.Objects), gcount, finaleExpectedRemuxMovies)
	}
	key, _, err := LoadOrCreateAssetV3Key(keyPath)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	cid, err := random16()
	if err != nil {
		return AssetV3Manifest{}, err
	}
	chunkKey, err := deriveAssetV3Key(key, "chunk", cid)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	chunkAEAD, err := assetV3AEAD(chunkKey)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	objects := append([]ReleaseStageObject(nil), stage.Objects...)
	sort.Slice(objects, func(i, j int) bool { return objects[i].LogicalPath < objects[j].LogicalPath })
	if err = os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return AssetV3Manifest{}, err
	}
	tmpDir, err := os.MkdirTemp(filepath.Dir(out), ".assetsv3-build-")
	if err != nil {
		return AssetV3Manifest{}, err
	}
	defer os.RemoveAll(tmpDir)
	compressed := filepath.Join(tmpDir, "zstd")
	if err = zstdCompressStage(filepath.Join(stageRoot, "objects"), compressed, objects); err != nil {
		return AssetV3Manifest{}, err
	}
	tmpOut := out + ".tmp"
	_ = os.Remove(tmpOut)
	f, err := os.OpenFile(tmpOut, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o644)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(tmpOut)
		}
	}()
	if _, err = f.Write(make([]byte, assetsV3HeaderSize)); err != nil {
		return AssetV3Manifest{}, err
	}
	manifest := AssetV3Manifest{ReleaseVersion: stage.ReleaseVersion, SchemaVersion: 3, ContainerFormat: ".unburger-assets v3", ContainerID: hex.EncodeToString(cid[:]), TotalPlainBytes: stage.TotalPlainBytes, StageManifest: stage, GDATManifest: gm}
	seen := map[string]bool{}
	for _, o := range objects {
		if seen[o.LogicalPath] {
			return AssetV3Manifest{}, fmt.Errorf("duplicate logical path %s", o.LogicalPath)
		}
		seen[o.LogicalPath] = true
		src := filepath.Join(stageRoot, "objects", filepath.FromSlash(o.LogicalPath))
		sha, size, err := hashAndSize(src)
		if err != nil {
			return AssetV3Manifest{}, err
		}
		if sha != o.SHA256 || size != o.Size {
			return AssetV3Manifest{}, fmt.Errorf("stage object drift %s", o.LogicalPath)
		}
		zpath := filepath.Join(compressed, filepath.FromSlash(o.LogicalPath)+".zst")
		zf, err := os.Open(zpath)
		if err != nil {
			return AssetV3Manifest{}, err
		}
		zinfo, err := zf.Stat()
		if err != nil {
			zf.Close()
			return AssetV3Manifest{}, err
		}
		oid, err := random16()
		if err != nil {
			zf.Close()
			return AssetV3Manifest{}, err
		}
		obj := AssetV3Object{ID: hex.EncodeToString(oid[:]), LogicalPath: o.LogicalPath, Kind: o.Kind, Game: o.Game, PlainSize: o.Size, SHA256: o.SHA256, CompressedSize: zinfo.Size(), Stage: o}
		buf := make([]byte, assetsV3ChunkSize)
		idx := uint32(0)
		for {
			n, er := zf.Read(buf)
			if n > 0 {
				var nonce [12]byte
				if _, err = io.ReadFull(rand.Reader, nonce[:]); err != nil {
					zf.Close()
					return AssetV3Manifest{}, err
				}
				off, _ := f.Seek(0, io.SeekCurrent)
				ct := chunkAEAD.Seal(nil, nonce[:], buf[:n], assetV3ChunkAAD(cid, oid, idx, int64(n)))
				if _, err = f.Write(nonce[:]); err == nil {
					_, err = f.Write(ct)
				}
				if err != nil {
					zf.Close()
					return AssetV3Manifest{}, err
				}
				obj.Chunks = append(obj.Chunks, AssetV3Chunk{Offset: off, StoredSize: int64(len(nonce) + len(ct)), CompressedBytes: int64(n)})
				idx++
			}
			if er == io.EOF {
				break
			}
			if er != nil {
				zf.Close()
				return AssetV3Manifest{}, er
			}
		}
		zf.Close()
		manifest.Objects = append(manifest.Objects, obj)
	}
	mraw, err := json.Marshal(manifest)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	mjson := filepath.Join(tmpDir, "manifest.json")
	mz := filepath.Join(tmpDir, "manifest.zst")
	if err = os.WriteFile(mjson, mraw, 0o600); err != nil {
		return AssetV3Manifest{}, err
	}
	if err = zstdCompressOne(mjson, mz); err != nil {
		return AssetV3Manifest{}, err
	}
	mcomp, err := os.ReadFile(mz)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	mkey, err := deriveAssetV3Key(key, "manifest", cid)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	maead, err := assetV3AEAD(mkey)
	if err != nil {
		return AssetV3Manifest{}, err
	}
	var mn [12]byte
	if _, err = io.ReadFull(rand.Reader, mn[:]); err != nil {
		return AssetV3Manifest{}, err
	}
	moff, _ := f.Seek(0, io.SeekCurrent)
	h := assetV3Header{Flags: 1, ChunkSize: assetsV3ChunkSize, ObjectCount: uint32(len(manifest.Objects)), ManifestOffset: uint64(moff), ManifestStoredSize: uint64(len(mcomp) + maead.Overhead()), ManifestPlainSize: uint64(len(mraw)), TotalPlainBytes: uint64(stage.TotalPlainBytes), ContainerID: cid, ManifestNonce: mn}
	hb := encodeAssetV3Header(h)
	mct := maead.Seal(nil, mn[:], mcomp, hb)
	if uint64(len(mct)) != h.ManifestStoredSize {
		return AssetV3Manifest{}, fmt.Errorf("manifest size accounting failure")
	}
	if _, err = f.Write(mct); err != nil {
		return AssetV3Manifest{}, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return AssetV3Manifest{}, err
	}
	if _, err = f.Write(hb); err != nil {
		return AssetV3Manifest{}, err
	}
	if err = f.Sync(); err != nil {
		return AssetV3Manifest{}, err
	}
	if err = f.Close(); err != nil {
		return AssetV3Manifest{}, err
	}
	if err = os.Rename(tmpOut, out); err != nil {
		return AssetV3Manifest{}, err
	}
	ok = true
	if err = VerifyAssetContainerV3(out, key); err != nil {
		return AssetV3Manifest{}, fmt.Errorf("post-build v3 verification: %w", err)
	}
	return manifest, nil
}

func OpenAssetContainerV3(path string, key []byte) (*AssetV3Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (*AssetV3Reader, error) { f.Close(); return nil, e }
	hb := make([]byte, assetsV3HeaderSize)
	if _, err = io.ReadFull(f, hb); err != nil {
		return fail(err)
	}
	h, err := decodeAssetV3Header(hb)
	if err != nil {
		return fail(err)
	}
	info, err := f.Stat()
	if err != nil {
		return fail(err)
	}
	if h.ManifestOffset+h.ManifestStoredSize > uint64(info.Size()) {
		return fail(fmt.Errorf("manifest outside container"))
	}
	mk, err := deriveAssetV3Key(key, "manifest", h.ContainerID)
	if err != nil {
		return fail(err)
	}
	ma, err := assetV3AEAD(mk)
	if err != nil {
		return fail(err)
	}
	ct := make([]byte, h.ManifestStoredSize)
	if _, err = f.ReadAt(ct, int64(h.ManifestOffset)); err != nil {
		return fail(err)
	}
	comp, err := ma.Open(nil, h.ManifestNonce[:], ct, hb)
	if err != nil {
		return fail(fmt.Errorf("encrypted manifest authentication failed: %w", err))
	}
	raw, err := zstdDecodeStdlib(comp, h.ManifestPlainSize)
	if err != nil {
		return fail(err)
	}
	var m AssetV3Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return fail(err)
	}
	if m.SchemaVersion != 3 || m.ContainerFormat != ".unburger-assets v3" || m.ContainerID != hex.EncodeToString(h.ContainerID[:]) || len(m.Objects) != int(h.ObjectCount) || m.TotalPlainBytes != int64(h.TotalPlainBytes) {
		return fail(fmt.Errorf("encrypted manifest/header mismatch"))
	}
	if m.ReleaseVersion != "" {
		if err := ValidateReleaseVersion(m.ReleaseVersion); err != nil {
			return fail(err)
		}
		if m.StageManifest.ReleaseVersion != m.ReleaseVersion {
			return fail(fmt.Errorf("release version mismatch within authenticated container"))
		}
	}
	ck, err := deriveAssetV3Key(key, "chunk", h.ContainerID)
	if err != nil {
		return fail(err)
	}
	ca, err := assetV3AEAD(ck)
	if err != nil {
		return fail(err)
	}
	r := &AssetV3Reader{f: f, header: h, chunkAEAD: ca, Manifest: m, byPath: map[string]*AssetV3Object{}}
	for i := range r.Manifest.Objects {
		o := &r.Manifest.Objects[i]
		if o.LogicalPath == "" || r.byPath[o.LogicalPath] != nil {
			return fail(fmt.Errorf("invalid/duplicate encrypted logical path"))
		}
		r.byPath[o.LogicalPath] = o
	}
	return r, nil
}

func (r *AssetV3Reader) Close() error { return r.f.Close() }

func (r *AssetV3Reader) ReadObject(logical string) ([]byte, error) {
	o := r.byPath[logical]
	if o == nil {
		return nil, fmt.Errorf("asset object not found: %s", logical)
	}
	idb, err := hex.DecodeString(o.ID)
	if err != nil || len(idb) != 16 {
		return nil, fmt.Errorf("invalid object id")
	}
	var oid [16]byte
	copy(oid[:], idb)
	var comp bytes.Buffer
	for i, c := range o.Chunks {
		if c.Offset < assetsV3HeaderSize || c.StoredSize != c.CompressedBytes+12+int64(r.chunkAEAD.Overhead()) || c.CompressedBytes <= 0 || c.CompressedBytes > assetsV3ChunkSize {
			return nil, fmt.Errorf("invalid chunk metadata")
		}
		raw := make([]byte, c.StoredSize)
		if _, err = r.f.ReadAt(raw, c.Offset); err != nil {
			return nil, err
		}
		pt, err := r.chunkAEAD.Open(nil, raw[:12], raw[12:], assetV3ChunkAAD(r.header.ContainerID, oid, uint32(i), c.CompressedBytes))
		if err != nil {
			return nil, fmt.Errorf("asset chunk authentication failed: %w", err)
		}
		if int64(len(pt)) != c.CompressedBytes {
			return nil, fmt.Errorf("asset chunk size mismatch")
		}
		comp.Write(pt)
	}
	if int64(comp.Len()) != o.CompressedSize {
		return nil, fmt.Errorf("compressed object size mismatch")
	}
	out, err := zstdDecodeStdlib(comp.Bytes(), uint64(o.PlainSize))
	if err != nil {
		return nil, err
	}
	if bytesSHA(out) != o.SHA256 {
		return nil, fmt.Errorf("asset object SHA mismatch")
	}
	return out, nil
}

func VerifyAssetContainerV3(path string, key []byte) error {
	r, err := OpenAssetContainerV3(path, key)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, o := range r.Manifest.Objects {
		if _, err = r.ReadObject(o.LogicalPath); err != nil {
			return fmt.Errorf("verify %s: %w", o.LogicalPath, err)
		}
	}
	return nil
}
