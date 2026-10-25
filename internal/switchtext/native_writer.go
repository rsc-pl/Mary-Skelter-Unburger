package switchtext

// Experimental Go-only native Switch text archive writer. The input is always
// the transient output of Stage() from current PC translations, not a second
// translated corpus. This package is deliberately not wired to public TUI.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type NativeWriteOptions struct{ Game, StagedEdits, SourceMaster, OutputDir string }
type NativeArchiveResult struct {
	Number        int    `json:"archive_number"`
	SourceSHA     string `json:"source_sha256"`
	OutputSHA     string `json:"output_sha256"`
	EditedEntries int    `json:"edited_entries"`
	EditedFields  int    `json:"edited_fields"`
}
type NativeWriteReport struct {
	Schema         int                   `json:"schema_version"`
	Status         string                `json:"status"`
	Game           string                `json:"game"`
	FormatBoundary string                `json:"format_boundary"`
	Archives       []NativeArchiveResult `json:"archives"`
}
type nativeRecord struct {
	RecordKey     string `json:"record_key"`
	SourceDataset string `json:"source_dataset"`
	SourceFileSHA string `json:"source_file_sha256"`
	Archive       int    `json:"archive_number"`
	Entry         int    `json:"gdat_index"`
	CL3           *int   `json:"cl3_inner_index"`
	RecordIndex   int    `json:"record_index"`
	FieldIndex    int    `json:"field_index"`
	RawHex        string `json:"raw_hex"`
	Encoding      string `json:"encoding"`
	Text          string `json:"text"`
	Meta          struct {
		Kind         string `json:"blob_type"`
		Row          *int   `json:"row"`
		StrOffset    *int   `json:"str_offset"`
		SlotCapacity *int   `json:"slot_capacity"`
		StringIndex  *int   `json:"string_index"`
		BlobOffset   *int   `json:"blob_offset"`
	} `json:"repack_metadata"`
}
type nativeGroup struct {
	kind  string
	edits []nativeRecord
}
type nativeArchive struct {
	sourceHash string
	entries    map[int]*nativeGroup
}

func nativeArchiveName(n int) string { return fmt.Sprintf("GxArchivedFile%03d.dat", n) }

// WriteNativeCandidate builds isolated archives in a private temporary folder.
// No symlinks, hardlinks, source archive copies or Python subprocesses. All
// sources are opened read-only; any unsafe/unknown chain aborts the entire run.
// Unsupported or structurally unproven STCM2 cases fail closed instead of
// publishing incomplete translated archives.
func WriteNativeCandidate(o NativeWriteOptions) (NativeWriteReport, error) {
	report := NativeWriteReport{Schema: 1, Status: "RESEARCH_CANDIDATE_NOT_RELEASE_READY", Game: o.Game, FormatBoundary: "Go: GDAT/ENC/BIN/GBNL/STCM2/CL3; whole-source and runtime QA pending"}
	expectedDataset, ok := datasets[o.Game]
	if !ok {
		return report, fmt.Errorf("invalid game %q", o.Game)
	}
	if o.StagedEdits == "" || o.SourceMaster == "" || o.OutputDir == "" {
		return report, fmt.Errorf("staged edits, source master and output required")
	}
	outAbs, e := filepath.Abs(o.OutputDir)
	if e != nil {
		return report, e
	}
	masterAbs, e := filepath.Abs(o.SourceMaster)
	if e != nil {
		return report, e
	}
	if outAbs == masterAbs || strings.HasPrefix(outAbs, masterAbs+string(filepath.Separator)) || strings.HasPrefix(masterAbs, outAbs+string(filepath.Separator)) {
		return report, fmt.Errorf("output and source master must be isolated")
	}
	if _, e = os.Lstat(outAbs); e == nil {
		return report, fmt.Errorf("output exists: %s", outAbs)
	} else if !os.IsNotExist(e) {
		return report, e
	}
	file, e := os.Open(o.StagedEdits)
	if e != nil {
		return report, e
	}
	defer file.Close()
	groups := map[int]*nativeArchive{}
	usedKeys := map[string]bool{}
	scan := bufio.NewScanner(file)
	scan.Buffer(make([]byte, 128*1024), 32*1024*1024)
	for scan.Scan() {
		row := scan.Bytes()
		if len(bytes.TrimSpace(row)) == 0 {
			continue
		}
		var r nativeRecord
		if e = json.Unmarshal(row, &r); e != nil {
			return report, e
		}
		if r.SourceDataset != expectedDataset || !strings.HasPrefix(r.RecordKey, expectedDataset+"/") || r.RecordKey == "" || r.Entry < 0 || r.Archive < 0 {
			return report, fmt.Errorf("invalid staged record identity %q", r.RecordKey)
		}
		if usedKeys[r.RecordKey] {
			return report, fmt.Errorf("duplicate staged key %s", r.RecordKey)
		}
		usedKeys[r.RecordKey] = true
		if len(r.SourceFileSHA) != 64 {
			return report, fmt.Errorf("%s: missing original archive SHA", r.RecordKey)
		}
		if r.Encoding != "utf-8" && r.Encoding != "utf8" && r.Encoding != "cp932" {
			return report, fmt.Errorf("%s: unsupported encoding %s", r.RecordKey, r.Encoding)
		}
		if r.Encoding == "cp932" {
			for _, ch := range r.Text {
				if ch > 127 {
					return report, fmt.Errorf("%s: CP932 output needs native encoder", r.RecordKey)
				}
			}
		}
		raw, e := hex.DecodeString(r.RawHex)
		if e != nil {
			return report, fmt.Errorf("%s: bad old raw_hex: %w", r.RecordKey, e)
		}
		if len(r.Text) == 0 || bytes.IndexByte([]byte(r.Text), 0) >= 0 {
			return report, fmt.Errorf("%s: empty or NUL replacement text", r.RecordKey)
		}
		// A source GBNL string may be valid and empty (e.g. Finale's
		// initially blank speaker field for Marchen). Source NULL fields
		// remain rejected by the GBNL parser in rebuildNativeEntry().
		if len(raw) == 0 && r.Meta.Kind != "gnl" {
			return report, fmt.Errorf("%s: empty original only proven for GBNL", r.RecordKey)
		}
		if bytes.Equal(raw, []byte(r.Text)) {
			return report, fmt.Errorf("%s: unchanged edit in stage", r.RecordKey)
		}
		switch r.Meta.Kind {
		case "enc_row":
			if r.Meta.Row == nil || r.Meta.StrOffset == nil {
				return report, fmt.Errorf("%s: no ENC location", r.RecordKey)
			}
		case "bin_string":
			if r.Meta.StringIndex == nil {
				return report, fmt.Errorf("%s: no BIN index", r.RecordKey)
			}
		case "gnl":
		default:
			return report, fmt.Errorf("%s: unsupported container %q", r.RecordKey, r.Meta.Kind)
		}
		a := groups[r.Archive]
		if a == nil {
			a = &nativeArchive{sourceHash: r.SourceFileSHA, entries: map[int]*nativeGroup{}}
			groups[r.Archive] = a
		}
		if a.sourceHash != r.SourceFileSHA {
			return report, fmt.Errorf("archive %d has inconsistent original hashes", r.Archive)
		}
		g := a.entries[r.Entry]
		if g == nil {
			g = &nativeGroup{kind: r.Meta.Kind}
			a.entries[r.Entry] = g
		}
		if g.kind != r.Meta.Kind {
			return report, fmt.Errorf("GDAT %d/%d mixes text container kinds", r.Archive, r.Entry)
		}
		g.edits = append(g.edits, r)
	}
	if e = scan.Err(); e != nil {
		return report, e
	}
	if len(groups) == 0 {
		return report, fmt.Errorf("no staged text edits")
	}
	// Preflight always runs before any output is created, including unsupported
	// STCM2 blobs. No incomplete or misleading 'successful' archives can leak.
	numbers := make([]int, 0, len(groups))
	for n := range groups {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)
	type ready struct {
		num    int
		source []byte
		output []byte
		result NativeArchiveResult
	}
	built := make([]ready, 0, len(numbers))
	for _, number := range numbers {
		a := groups[number]
		srcPath := filepath.Join(masterAbs, nativeArchiveName(number))
		raw, e := os.ReadFile(srcPath)
		if e != nil {
			return report, e
		}
		sourceSum := sha256.Sum256(raw)
		if hex.EncodeToString(sourceSum[:]) != a.sourceHash {
			return report, fmt.Errorf("archive %d source SHA mismatch", number)
		}
		// Only the evidenced MS2 Western a073 source has nonzero bytes beyond
		// its indexed GDAT entries. Preserve these bytes in-place with a pinned
		// whole-file/trailer SHA; do not generalize to arbitrary GDAT footers.
		var g mskit.SwitchGDAT
		g, e = mskit.ParseSwitchGDAT(raw)
		if e != nil && o.Game == "ms2" && number == 73 {
			g, e = mskit.ParseSwitchGDATWithOpaqueTailEvidence(raw, mskit.GDATOpaqueTailEvidence{
				SourceSHA256: "d43b0910fe309da3d7074013d1a8c09ab98a9c22a4ba1d472a1919212783ff18",
				TailSHA256:   "da02829e7ddc75266211a2579f52936ed3a1ead8c78635a6b63ee5f3948c06a0",
				EntryCount:   3, IndexedEnd: 693194, TailLength: 1249,
			})
		}
		if e != nil {
			return report, fmt.Errorf("archive %d parse: %w", number, e)
		}
		entries := make([][]byte, len(g.Entries))
		copy(entries, g.Entries)
		indices := make([]int, 0, len(a.entries))
		for i := range a.entries {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		changed := 0
		fields := 0
		for _, index := range indices {
			if index >= len(entries) {
				return report, fmt.Errorf("archive %d entry %d out of range", number, index)
			}
			gr := a.entries[index]
			var replaced []byte
			if o.Game == "ms2" && number == 73 && index == 0 && gr.kind == "bin_string" {
				// The source-pinned a073 opaque trailer forbids shifting entry 0.
				// Use exact-width pure-Go DEFLATE rather than relocating it.
				replaced, e = rebuildNativeBINFixed(entries[index], gr)
			} else if o.Game == "ms2" && number == 73 && gr.kind == "enc_row" {
				// Every ENC entry in this specific pinned source must keep its
				// original footprint; never relocate the opaque GDAT footer.
				replaced, e = rebuildNativeENCFixed(entries[index], gr)
			} else {
				replaced, e = rebuildNativeEntry(entries[index], gr)
			}
			if e != nil {
				return report, fmt.Errorf("archive %d entry %d: %w", number, index, e)
			}
			if bytes.Equal(replaced, entries[index]) {
				return report, fmt.Errorf("archive %d entry %d edit produced no change", number, index)
			}
			entries[index] = replaced
			changed++
			fields += len(gr.edits)
		}
		if len(g.OpaqueTail) != 0 {
			// Report ALL entry footprint violations in one pass rather than
			// forcing another expensive full-game rebuild for each entry.
			var sizeChanges []string
			for i := range entries {
				if len(entries[i]) != len(g.Entries[i]) {
					sizeChanges = append(sizeChanges, fmt.Sprintf("g%d %d -> %d", i, len(g.Entries[i]), len(entries[i])))
				}
			}
			if len(sizeChanges) != 0 {
				return report, fmt.Errorf("archive %d opaque-tail fixed-footprint violations: %s", number, strings.Join(sizeChanges, "; "))
			}
		}
		rebuilt, e := mskit.BuildSwitchGDATFromSource(g, entries)
		if e != nil {
			return report, e
		}
		check, e := mskit.VerifySwitchGDATRebuild(g, rebuilt)
		if e != nil {
			return report, e
		}
		if !bytes.Equal(check.ZeroTail, g.ZeroTail) || !bytes.Equal(check.OpaqueTail, g.OpaqueTail) {
			return report, fmt.Errorf("archive %d trailing bytes drift", number)
		}
		if len(check.Entries) != len(g.Entries) {
			return report, fmt.Errorf("archive %d entry count drift", number)
		}
		for i := range entries {
			if !bytes.Equal(check.Entries[i], entries[i]) {
				return report, fmt.Errorf("archive %d entry %d verification failure", number, i)
			}
			if a.entries[i] == nil && !bytes.Equal(check.Entries[i], g.Entries[i]) {
				return report, fmt.Errorf("archive %d unchanged entry %d drift", number, i)
			}
		}
		sum := sha256.Sum256(rebuilt)
		built = append(built, ready{number, raw, rebuilt, NativeArchiveResult{Number: number, SourceSHA: a.sourceHash, OutputSHA: hex.EncodeToString(sum[:]), EditedEntries: changed, EditedFields: fields}})
	}
	parent := filepath.Dir(outAbs)
	if e = os.MkdirAll(parent, 0700); e != nil {
		return report, e
	}
	stage, e := os.MkdirTemp(parent, ".switch-native-go-*")
	if e != nil {
		return report, e
	}
	defer os.RemoveAll(stage)
	for _, b := range built {
		p := filepath.Join(stage, nativeArchiveName(b.num))
		if e = os.WriteFile(p, b.output, 0600); e != nil {
			return report, e
		}
		result, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(result, b.output) {
			return report, fmt.Errorf("archive %d disk roundtrip: %v", b.num, e)
		}
		report.Archives = append(report.Archives, b.result)
	}
	meta, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return report, e
	}
	if e = os.WriteFile(filepath.Join(stage, "native-write-report.json"), append(meta, '\n'), 0600); e != nil {
		return report, e
	}
	if _, e = os.Lstat(outAbs); e == nil {
		return report, fmt.Errorf("output created concurrently")
	} else if !os.IsNotExist(e) {
		return report, e
	}
	if e = os.Rename(stage, outAbs); e != nil {
		return report, e
	}
	return report, nil
}

func rebuildNativeEntry(entry []byte, g *nativeGroup) ([]byte, error) {
	switch g.kind {
	case "enc_row":
		return rebuildNativeENC(entry, g, false)
	case "bin_string":
		edits := map[int][]byte{}
		source, e := mskit.ParseBINEntry(entry)
		if e != nil {
			return nil, e
		}
		for _, r := range g.edits {
			i := *r.Meta.StringIndex
			if _, yes := edits[i]; yes {
				return nil, fmt.Errorf("duplicate BIN index %d", i)
			}
			old, e := source.String(i)
			if e != nil {
				return nil, e
			}
			expected, e := hex.DecodeString(r.RawHex)
			if e != nil {
				return nil, e
			}
			if !bytes.Equal(old, expected) {
				return nil, fmt.Errorf("%s: BIN original byte mismatch", r.RecordKey)
			}
			edits[i] = []byte(r.Text)
		}
		return mskit.RebuildBINEntry(entry, edits, mskit.WrapBILZGo)
	case "gnl":
		edits := make([]mskit.GBNLTextEdit, 0, len(g.edits))
		for _, r := range g.edits {
			cl := -1
			if r.CL3 != nil {
				cl = *r.CL3
			}
			blob := -1
			if r.Meta.BlobOffset != nil {
				blob = *r.Meta.BlobOffset
			}
			old, e := hex.DecodeString(r.RawHex)
			if e != nil {
				return nil, e
			}
			edits = append(edits, mskit.GBNLTextEdit{CL3Index: cl, BlobOffset: blob, Message: r.RecordIndex, Field: r.FieldIndex, Old: old, New: []byte(r.Text), RecordKey: r.RecordKey})
		}
		return mskit.RebuildGBNLTree(entry, edits, mskit.WrapBILZGo)
	}
	return nil, fmt.Errorf("unknown native text chain %s", g.kind)
}

// Source-record original bytes are checked before native BIN rewriting.
// This is used only for the SHA-pinned Western MS2 a073/g00000 entry.
func rebuildNativeBINFixed(entry []byte, g *nativeGroup) ([]byte, error) {
	old, e := mskit.ParseBINEntry(entry)
	if e != nil {
		return nil, e
	}
	edits := map[int][]byte{}
	for _, r := range g.edits {
		i := *r.Meta.StringIndex
		if _, ok := edits[i]; ok {
			return nil, fmt.Errorf("duplicate BIN index %d", i)
		}
		got, e := old.String(i)
		if e != nil {
			return nil, e
		}
		want, e := hex.DecodeString(r.RawHex)
		if e != nil {
			return nil, e
		}
		if !bytes.Equal(got, want) {
			return nil, fmt.Errorf("%s: old BIN bytes differ", r.RecordKey)
		}
		edits[i] = []byte(r.Text)
	}
	return mskit.RebuildBINEntryFixedWidthGo(entry, edits)
}

func rebuildNativeENC(entry []byte, g *nativeGroup, fixed bool) ([]byte, error) {
	edits := make([]mskit.ENCEdit, 0, len(g.edits))
	seen := map[string]bool{}
	for _, r := range g.edits {
		k := fmt.Sprintf("%d:%d", *r.Meta.Row, *r.Meta.StrOffset)
		if seen[k] {
			return nil, fmt.Errorf("duplicate ENC slot %s", k)
		}
		seen[k] = true
		old, e := hex.DecodeString(r.RawHex)
		if e != nil {
			return nil, e
		}
		cap := 0
		if r.Meta.SlotCapacity != nil {
			cap = *r.Meta.SlotCapacity
		}
		edits = append(edits, mskit.ENCEdit{Row: *r.Meta.Row, Offset: *r.Meta.StrOffset, Old: old, New: []byte(r.Text), Capacity: cap, RecordKey: r.RecordKey})
	}
	if fixed {
		return mskit.RebuildENCEntryFixedWidthGo(entry, edits)
	}
	return mskit.RebuildENCEntry(entry, edits, mskit.WrapBILZGo)
}
func rebuildNativeENCFixed(entry []byte, g *nativeGroup) ([]byte, error) {
	return rebuildNativeENC(entry, g, true)
}
