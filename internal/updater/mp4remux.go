package updater

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Finale's shipped movies are non-fragmented ISO-BMFF files with exactly one
// video and one audio track. The JP release payloads are one-track M4A files.
// This remuxer intentionally supports only the layouts proven by the project
// discovery report and fails closed on anything else.

type mp4Top struct {
	Type         string
	Offset, Size int64
	Header       int64
}

type mp4Node struct {
	Type     string
	Data     []byte
	Children []*mp4Node
}

type mp4Chunk struct{ Offset, Size uint64 }

var mp4Containers = map[string]bool{"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true, "edts": true}

func scanMP4(path string) ([]mp4Top, error) {
	f, err := os.Open(path)
	if err != nil { return nil, err }
	defer f.Close()
	st, err := f.Stat()
	if err != nil { return nil, err }
	var out []mp4Top
	for off := int64(0); off < st.Size(); {
		var h [16]byte
		if _, err := f.ReadAt(h[:8], off); err != nil { return nil, err }
		sz := int64(binary.BigEndian.Uint32(h[:4]))
		header := int64(8)
		if sz == 1 {
			if _, err := f.ReadAt(h[8:], off+8); err != nil { return nil, err }
			sz = int64(binary.BigEndian.Uint64(h[8:]))
			header = 16
		} else if sz == 0 {
			sz = st.Size() - off
		}
		if sz < header || off+sz > st.Size() { return nil, fmt.Errorf("invalid MP4 box at %d", off) }
		out = append(out, mp4Top{string(h[4:8]), off, sz, header})
		off += sz
	}
	return out, nil
}

func oneTop(boxes []mp4Top, typ string) (mp4Top, error) {
	var hit mp4Top
	n := 0
	for _, b := range boxes {
		if b.Type == typ {
			hit, n = b, n+1
		}
	}
	if n != 1 { return hit, fmt.Errorf("expected one %s box, got %d", typ, n) }
	return hit, nil
}

func readTop(f *os.File, b mp4Top) ([]byte, error) {
	raw := make([]byte, int(b.Size))
	_, err := f.ReadAt(raw, b.Offset)
	return raw, err
}

func parseNode(raw []byte) (*mp4Node, error) {
	if len(raw) < 8 || binary.BigEndian.Uint32(raw[:4]) != uint32(len(raw)) { return nil, fmt.Errorf("unsupported nested MP4 box header") }
	n := &mp4Node{Type: string(raw[4:8])}
	payload := raw[8:]
	if !mp4Containers[n.Type] {
		n.Data = append([]byte(nil), payload...)
		return n, nil
	}
	for off := 0; off < len(payload); {
		if len(payload)-off < 8 { return nil, fmt.Errorf("truncated %s child", n.Type) }
		sz := int(binary.BigEndian.Uint32(payload[off : off+4]))
		if sz < 8 || off+sz > len(payload) { return nil, fmt.Errorf("invalid %s child", n.Type) }
		c, err := parseNode(payload[off : off+sz])
		if err != nil { return nil, err }
		n.Children = append(n.Children, c)
		off += sz
	}
	return n, nil
}

func (n *mp4Node) clone() *mp4Node {
	c := &mp4Node{Type: n.Type, Data: append([]byte(nil), n.Data...)}
	for _, x := range n.Children {
		c.Children = append(c.Children, x.clone())
	}
	return c
}

func (n *mp4Node) encode() ([]byte, error) {
	var payload []byte
	if mp4Containers[n.Type] {
		var b bytes.Buffer
		for _, c := range n.Children {
			raw, err := c.encode()
			if err != nil { return nil, err }
			b.Write(raw)
		}
		payload = b.Bytes()
	} else {
		payload = n.Data
	}
	if len(payload)+8 > int(^uint32(0)) { return nil, fmt.Errorf("nested MP4 box too large: %s", n.Type) }
	out := make([]byte, len(payload)+8)
	binary.BigEndian.PutUint32(out[:4], uint32(len(out)))
	copy(out[4:8], n.Type)
	copy(out[8:], payload)
	return out, nil
}

func child(n *mp4Node, typ string) *mp4Node {
	for _, c := range n.Children {
		if c.Type == typ { return c }
	}
	return nil
}

func replaceChild(n *mp4Node, typ string, repl *mp4Node) error {
	for i, c := range n.Children {
		if c.Type == typ {
			n.Children[i] = repl
			return nil
		}
	}
	return fmt.Errorf("missing %s/%s", n.Type, typ)
}

func trackHandler(t *mp4Node) (string, error) {
	m := child(t, "mdia")
	if m == nil { return "", fmt.Errorf("trak missing mdia") }
	h := child(m, "hdlr")
	if h == nil || len(h.Data) < 12 || h.Data[0] != 0 { return "", fmt.Errorf("unsupported hdlr") }
	return string(h.Data[8:12]), nil
}

func findTrack(moov *mp4Node, handler string) (*mp4Node, error) {
	var hit *mp4Node
	count := 0
	for _, t := range moov.Children {
		if t.Type != "trak" { continue }
		h, err := trackHandler(t)
		if err != nil { return nil, err }
		if h == handler {
			hit, count = t, count+1
		}
	}
	if count != 1 { return nil, fmt.Errorf("expected one %s track, got %d", handler, count) }
	return hit, nil
}

func moovNode(f *os.File, tops []mp4Top) (*mp4Node, error) {
	b, err := oneTop(tops, "moov")
	if err != nil { return nil, err }
	raw, err := readTop(f, b)
	if err != nil { return nil, err }
	return parseNode(raw)
}

func v0(n *mp4Node, min int) error {
	if n == nil || len(n.Data) < min || n.Data[0] != 0 { return fmt.Errorf("unsupported %s full box", n.Type) }
	return nil
}

func mvhdTime(n *mp4Node) (uint64, uint64, error) {
	if err := v0(n, 20); err != nil { return 0, 0, err }
	return uint64(binary.BigEndian.Uint32(n.Data[12:16])), uint64(binary.BigEndian.Uint32(n.Data[16:20])), nil
}
func setMVHDDuration(n *mp4Node, d uint64) error {
	if err := v0(n, 20); err != nil || d > 0xffffffff { return fmt.Errorf("unsupported mvhd duration") }
	binary.BigEndian.PutUint32(n.Data[16:20], uint32(d))
	return nil
}
func tkhdDuration(n *mp4Node) (uint64, error) {
	if err := v0(n, 24); err != nil { return 0, err }
	return uint64(binary.BigEndian.Uint32(n.Data[20:24])), nil
}
func setTKHDDuration(n *mp4Node, d uint64) error {
	if err := v0(n, 24); err != nil || d > 0xffffffff { return fmt.Errorf("unsupported tkhd duration") }
	binary.BigEndian.PutUint32(n.Data[20:24], uint32(d))
	return nil
}
func elstFirst(n *mp4Node) (uint64, int32, error) {
	if err := v0(n, 20); err != nil || binary.BigEndian.Uint32(n.Data[4:8]) != 1 { return 0, 0, fmt.Errorf("unsupported elst") }
	return uint64(binary.BigEndian.Uint32(n.Data[8:12])), int32(binary.BigEndian.Uint32(n.Data[12:16])), nil
}
func setElstFirst(n *mp4Node, d uint64, media int32) error {
	if err := v0(n, 20); err != nil || d > 0xffffffff || binary.BigEndian.Uint32(n.Data[4:8]) != 1 { return fmt.Errorf("unsupported elst") }
	binary.BigEndian.PutUint32(n.Data[8:12], uint32(d))
	binary.BigEndian.PutUint32(n.Data[12:16], uint32(media))
	return nil
}
func scaleDuration(v, from, to uint64) uint64 { return (v*to + from/2) / from }

func stbl(t *mp4Node) (*mp4Node, error) {
	m := child(t, "mdia")
	if m == nil || child(m, "minf") == nil || child(child(m, "minf"), "stbl") == nil { return nil, fmt.Errorf("track missing stbl") }
	return child(child(m, "minf"), "stbl"), nil
}

func offsets(s *mp4Node) ([]uint64, *mp4Node, error) {
	n := child(s, "stco")
	if n == nil || len(n.Data) < 8 || n.Data[0] != 0 { return nil, nil, fmt.Errorf("shipped layout requires stco") }
	count := int(binary.BigEndian.Uint32(n.Data[4:8]))
	if len(n.Data) != 8+4*count { return nil, nil, fmt.Errorf("stco length mismatch") }
	out := make([]uint64, count)
	for i := range out {
		out[i] = uint64(binary.BigEndian.Uint32(n.Data[8+i*4 : 12+i*4]))
	}
	return out, n, nil
}

func setOffsets(n *mp4Node, off []uint64) error {
	if n == nil || len(n.Data) != 8+4*len(off) { return fmt.Errorf("stco count mismatch") }
	for i, x := range off {
		if x > 0xffffffff { return fmt.Errorf("stco offset overflow") }
		binary.BigEndian.PutUint32(n.Data[8+i*4:12+i*4], uint32(x))
	}
	return nil
}

func sizes(s *mp4Node) ([]uint64, error) {
	n := child(s, "stsz")
	if n == nil || len(n.Data) < 12 || n.Data[0] != 0 { return nil, fmt.Errorf("invalid stsz") }
	fixed := binary.BigEndian.Uint32(n.Data[4:8])
	count := int(binary.BigEndian.Uint32(n.Data[8:12]))
	out := make([]uint64, count)
	if fixed != 0 {
		for i := range out {
			out[i] = uint64(fixed)
		}
		return out, nil
	}
	if len(n.Data) != 12+4*count { return nil, fmt.Errorf("stsz length mismatch") }
	for i := range out {
		out[i] = uint64(binary.BigEndian.Uint32(n.Data[12+i*4 : 16+i*4]))
	}
	return out, nil
}

func chunkSampleCounts(s *mp4Node, count int) ([]uint32, error) {
	n := child(s, "stsc")
	if n == nil || len(n.Data) < 8 || n.Data[0] != 0 { return nil, fmt.Errorf("invalid stsc") }
	ec := int(binary.BigEndian.Uint32(n.Data[4:8]))
	if ec == 0 || len(n.Data) != 8+12*ec { return nil, fmt.Errorf("stsc length mismatch") }
	type ent struct{ first, samples uint32 }
	es := make([]ent, ec)
	for i := range es {
		p := 8 + i*12
		es[i] = ent{binary.BigEndian.Uint32(n.Data[p : p+4]), binary.BigEndian.Uint32(n.Data[p+4 : p+8])}
		if es[i].first == 0 || (i > 0 && es[i].first <= es[i-1].first) { return nil, fmt.Errorf("invalid stsc ordering") }
	}
	out := make([]uint32, count)
	j := 0
	for i := range out {
		chunk := uint32(i + 1)
		for j+1 < len(es) && chunk >= es[j+1].first {
			j++
		}
		if chunk < es[j].first { return nil, fmt.Errorf("stsc does not cover chunk") }
		out[i] = es[j].samples
	}
	return out, nil
}

func chunks(t *mp4Node) ([]mp4Chunk, *mp4Node, error) {
	s, err := stbl(t)
	if err != nil { return nil, nil, err }
	off, box, err := offsets(s)
	if err != nil { return nil, nil, err }
	sz, err := sizes(s)
	if err != nil { return nil, nil, err }
	per, err := chunkSampleCounts(s, len(off))
	if err != nil { return nil, nil, err }
	out := make([]mp4Chunk, len(off))
	si := 0
	for i := range out {
		var total uint64
		for k := uint32(0); k < per[i]; k++ {
			if si >= len(sz) { return nil, nil, fmt.Errorf("stsc consumes too many samples") }
			total += sz[si]
			si++
		}
		out[i] = mp4Chunk{off[i], total}
	}
	if si != len(sz) { return nil, nil, fmt.Errorf("stsc leaves samples unassigned") }
	return out, box, nil
}

func validateChunks(c []mp4Chunk, m mp4Top) error {
	lo, hi := uint64(m.Offset+m.Header), uint64(m.Offset+m.Size)
	for _, x := range c {
		if x.Offset < lo || x.Offset+x.Size > hi { return fmt.Errorf("sample chunk outside mdat") }
	}
	return nil
}

func copyChunks(f *os.File, c []mp4Chunk, w io.Writer) error {
	for _, x := range c {
		if _, err := io.CopyN(w, io.NewSectionReader(f, int64(x.Offset), int64(x.Size)), int64(x.Size)); err != nil { return err }
	}
	return nil
}

// FinaleMovieVideoSHA256 hashes encoded video sample bytes in chunk order.
// Remuxes preserve these bytes exactly, so this identity accepts already-modded
// movies as update inputs without a whole-file hash history.
func FinaleMovieVideoSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil { return "", err }
	defer f.Close()
	tops, err := scanMP4(path)
	if err != nil { return "", err }
	mdat, err := oneTop(tops, "mdat")
	if err != nil { return "", err }
	moov, err := moovNode(f, tops)
	if err != nil { return "", err }
	video, err := findTrack(moov, "vide")
	if err != nil { return "", err }
	vc, _, err := chunks(video)
	if err != nil { return "", err }
	if err := validateChunks(vc, mdat); err != nil { return "", err }
	h := sha256.New()
	if err := copyChunks(f, vc, h); err != nil { return "", err }
	return hex.EncodeToString(h.Sum(nil)), nil
}

func writeMdatHeader(w io.Writer, header int64, size uint64) error {
	if header == 16 {
		var b [16]byte
		binary.BigEndian.PutUint32(b[:4], 1)
		copy(b[4:8], "mdat")
		binary.BigEndian.PutUint64(b[8:], size)
		_, err := w.Write(b[:])
		return err
	}
	if header != 8 || size > 0xffffffff { return fmt.Errorf("unsupported mdat header") }
	var b [8]byte
	binary.BigEndian.PutUint32(b[:4], uint32(size))
	copy(b[4:8], "mdat")
	_, err := w.Write(b[:])
	return err
}

func RemuxFinaleMovie(sourceMP4, payloadM4A, outputMP4 string) error {
	sf, err := os.Open(sourceMP4)
	if err != nil { return err }
	defer sf.Close()
	pf, err := os.Open(payloadM4A)
	if err != nil { return err }
	defer pf.Close()

	stops, err := scanMP4(sourceMP4)
	if err != nil { return err }
	ptops, err := scanMP4(payloadM4A)
	if err != nil { return err }
	for _, b := range stops {
		if b.Type == "moof" || b.Type == "sidx" { return fmt.Errorf("fragmented MP4 is outside the proven Finale layout") }
	}
	smdat, err := oneTop(stops, "mdat")
	if err != nil { return err }
	pmdat, err := oneTop(ptops, "mdat")
	if err != nil { return err }
	smoov, err := moovNode(sf, stops)
	if err != nil { return err }
	pmoov, err := moovNode(pf, ptops)
	if err != nil { return err }
	video, err := findTrack(smoov, "vide")
	if err != nil { return err }
	audio, err := findTrack(smoov, "soun")
	if err != nil { return err }
	paudio, err := findTrack(pmoov, "soun")
	if err != nil { return err }
	if len(childrenByType(smoov, "trak")) != 2 || len(childrenByType(pmoov, "trak")) != 1 { return fmt.Errorf("unsupported track inventory") }

	vchunks, voff, err := chunks(video)
	if err != nil { return err }
	pchunks, _, err := chunks(paudio)
	if err != nil { return err }
	if err := validateChunks(vchunks, smdat); err != nil { return err }
	if err := validateChunks(pchunks, pmdat); err != nil { return err }

	// Preserve EN-side track identity/localized metadata, but transplant the exact
	// JP audio media description and sample tables from the staged M4A payload.
	smdia, pmdia := child(audio, "mdia"), child(paudio, "mdia")
	if smdia == nil || pmdia == nil || child(pmdia, "mdhd") == nil || child(pmdia, "minf") == nil { return fmt.Errorf("audio media boxes missing") }
	if err := replaceChild(smdia, "mdhd", child(pmdia, "mdhd").clone()); err != nil { return err }
	if err := replaceChild(smdia, "minf", child(pmdia, "minf").clone()); err != nil { return err }
	_, aoff, err := chunks(audio)
	if err != nil { return err }

	smv, pmv := child(smoov, "mvhd"), child(pmoov, "mvhd")
	sts, _, err := mvhdTime(smv)
	if err != nil { return err }
	pts, _, err := mvhdTime(pmv)
	if err != nil || pts == 0 { return fmt.Errorf("invalid payload movie timescale") }
	ptd, err := tkhdDuration(child(paudio, "tkhd"))
	if err != nil { return err }
	newAudioDuration := scaleDuration(ptd, pts, sts)
	if err := setTKHDDuration(child(audio, "tkhd"), newAudioDuration); err != nil { return err }
	pedts, sedts := child(paudio, "edts"), child(audio, "edts")
	if pedts == nil || sedts == nil { return fmt.Errorf("audio edit list required by proven layout") }
	pdur, pmedia, err := elstFirst(child(pedts, "elst"))
	if err != nil { return err }
	if err := setElstFirst(child(sedts, "elst"), scaleDuration(pdur, pts, sts), pmedia); err != nil { return err }
	vdur, err := tkhdDuration(child(video, "tkhd"))
	if err != nil { return err }
	movieDuration := vdur
	if newAudioDuration > movieDuration {
		movieDuration = newAudioDuration
	}
	if err := setMVHDDuration(smv, movieDuration); err != nil { return err }

	if err := setOffsets(voff, make([]uint64, len(vchunks))); err != nil { return err }
	if err := setOffsets(aoff, make([]uint64, len(pchunks))); err != nil { return err }
	dummyMoov, err := smoov.encode()
	if err != nil { return err }
	var payloadBytes uint64
	for _, c := range vchunks {
		payloadBytes += c.Size
	}
	for _, c := range pchunks {
		payloadBytes += c.Size
	}
	mdatSize := uint64(smdat.Header) + payloadBytes
	if smdat.Header == 8 && mdatSize > 0xffffffff { return fmt.Errorf("output mdat requires extended size") }

	var pos, mdatPayload uint64
	for _, b := range stops {
		switch b.Type {
		case "moov":
			pos += uint64(len(dummyMoov))
		case "mdat":
			mdatPayload = pos + uint64(smdat.Header)
			pos += mdatSize
		default:
			pos += uint64(b.Size)
		}
	}
	if mdatPayload == 0 { return fmt.Errorf("could not place output mdat") }
	voffs, aoffs := make([]uint64, len(vchunks)), make([]uint64, len(pchunks))
	cur := mdatPayload
	for i, c := range vchunks {
		voffs[i], cur = cur, cur+c.Size
	}
	for i, c := range pchunks {
		aoffs[i], cur = cur, cur+c.Size
	}
	if err := setOffsets(voff, voffs); err != nil { return err }
	if err := setOffsets(aoff, aoffs); err != nil { return err }
	moovBytes, err := smoov.encode()
	if err != nil { return err }
	if len(moovBytes) != len(dummyMoov) { return fmt.Errorf("moov size changed while resolving offsets") }

	if err := os.MkdirAll(filepath.Dir(outputMP4), 0o755); err != nil { return err }
	tmp := outputMP4 + ".tmp"
	_ = os.Remove(tmp)
	out, err := os.Create(tmp)
	if err != nil { return err }
	committed := false
	defer func() {
		_ = out.Close()
		if !committed { _ = os.Remove(tmp) }
	}()
	for _, b := range stops {
		switch b.Type {
		case "moov":
			_, err = out.Write(moovBytes)
		case "mdat":
			if err = writeMdatHeader(out, b.Header, mdatSize); err == nil { err = copyChunks(sf, vchunks, out) }
			if err == nil { err = copyChunks(pf, pchunks, out) }
		default:
			var raw []byte
			raw, err = readTop(sf, b)
			if err == nil { _, err = out.Write(raw) }
		}
		if err != nil { return err }
	}
	if err := out.Sync(); err != nil { return err }
	if err := out.Close(); err != nil { return err }
	if err := os.Rename(tmp, outputMP4); err != nil { return err }
	committed = true
	return nil
}

func childrenByType(n *mp4Node, typ string) []*mp4Node {
	var out []*mp4Node
	for _, c := range n.Children {
		if c.Type == typ { out = append(out, c) }
	}
	return out
}
