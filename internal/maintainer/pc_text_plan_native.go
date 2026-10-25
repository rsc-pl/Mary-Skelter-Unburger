package maintainer

// Native PC text change planning. This deliberately does not write game archives:
// the compressed-file writer is not promoted until real pristine-PC master
// archives can be compared against the accepted Python rebuild. The planner is
// the first production-ready input validation boundary for that port.

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// PCTextEdit identifies exactly one source-backed field to change.
type PCTextEdit struct {
	RecordKey                          string
	SourceFile                         string
	SourceSHA256                       string
	Archive, GDAT                      int
	Type                               string
	CL3, Blob                          int // -1 represents no wrapper / direct GBNL
	Message, Field                     int
	Row, Offset, StringIndex, Capacity int
	Old, New                           []byte
}

type PCTextPlan struct {
	Dataset        string
	TotalRecords   int
	ChangedRecords int
	Archives       []int
	Edits          []PCTextEdit
}

type pcRecord struct {
	RecordKey     string   `json:"record_key"`
	SourceDataset string   `json:"source_dataset"`
	SourceFile    string   `json:"source_file"`
	SourceSHA     string   `json:"source_file_sha256"`
	Archive       int      `json:"archive_number"`
	GDAT          int      `json:"gdat_index"`
	CL3           *int     `json:"cl3_inner_index"`
	Message       int      `json:"record_index"`
	Field         int      `json:"field_index"`
	Encoding      string   `json:"encoding"`
	Text          string   `json:"text"`
	RawHex        string   `json:"raw_hex"`
	Chain         []string `json:"format_chain"`
	Meta          struct {
		BlobType     string `json:"blob_type"`
		BlobOffset   *int   `json:"blob_offset"`
		Row          *int   `json:"row"`
		StrOffset    *int   `json:"str_offset"`
		StringIndex  *int   `json:"string_index"`
		SlotCapacity *int   `json:"slot_capacity"`
	} `json:"repack_metadata"`
}

// PlanPCTextRecords validates and groups real PC technical JSONL without ever
// changing frozen translations or source archives. Unsupported encodings and
// structures fail closed; no lossy re-encoding or best-effort source inference.
func PlanPCTextRecords(rd io.Reader, dataset string) (PCTextPlan, error) {
	plan := PCTextPlan{Dataset: dataset}
	if dataset != "MS2_PC_LOCALIZED" && dataset != "MSF_PC_LOCALIZED" && dataset != "MSF_PC_SYSTEM" {
		return plan, fmt.Errorf("unsupported PC dataset %q", dataset)
	}
	seen := make(map[string]bool)
	identities := make(map[string]bool)
	sourceHashes := make(map[string]string)
	archives := make(map[int]bool)
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var r pcRecord
		dec := json.NewDecoder(bytes.NewReader(line))
		if err := dec.Decode(&r); err != nil {
			return plan, fmt.Errorf("line %d: %w", n, err)
		}
		plan.TotalRecords++
		if r.SourceDataset != dataset || r.Archive < 0 || r.Archive > 999 || r.GDAT < 0 || r.GDAT > 1000000 || r.RecordKey == "" {
			return plan, fmt.Errorf("line %d: inconsistent record source/identity", n)
		}
		if seen[r.RecordKey] {
			return plan, fmt.Errorf("line %d: duplicate record key %q", n, r.RecordKey)
		}
		seen[r.RecordKey] = true
		expectedFile := fmt.Sprintf("GxArchivedFile%03d.dat", r.Archive)
		if filepath.Base(r.SourceFile) != expectedFile || r.SourceFile != expectedFile || len(r.SourceSHA) != 64 {
			return plan, fmt.Errorf("%s: invalid source file/hash", r.RecordKey)
		}
		if _, err := hex.DecodeString(r.SourceSHA); err != nil {
			return plan, fmt.Errorf("%s: invalid source sha: %w", r.RecordKey, err)
		}
		if sha, ok := sourceHashes[r.SourceFile]; ok && sha != r.SourceSHA {
			return plan, fmt.Errorf("%s: contradictory source SHA for %s", r.RecordKey, r.SourceFile)
		}
		sourceHashes[r.SourceFile] = r.SourceSHA
		if r.Encoding != "utf-8" || !utf8.ValidString(r.Text) || strings.ContainsRune(r.Text, '\x00') {
			return plan, fmt.Errorf("%s: unsupported/unsafe text encoding %q", r.RecordKey, r.Encoding)
		}
		old, err := hex.DecodeString(r.RawHex)
		if err != nil {
			return plan, fmt.Errorf("%s: invalid original raw_hex: %w", r.RecordKey, err)
		}
		if bytes.Equal(old, []byte(r.Text)) {
			continue
		}
		keyPrefix := fmt.Sprintf("%s/a%03d/g%05d/", dataset, r.Archive, r.GDAT)
		if !strings.HasPrefix(r.RecordKey, keyPrefix) {
			return plan, fmt.Errorf("%s: record key and archive location disagree", r.RecordKey)
		}
		e := PCTextEdit{RecordKey: r.RecordKey, SourceFile: r.SourceFile, SourceSHA256: r.SourceSHA,
			Archive: r.Archive, GDAT: r.GDAT, Type: r.Meta.BlobType, CL3: -1, Blob: -1,
			Message: r.Message, Field: r.Field, Row: -1, Offset: -1, StringIndex: -1, Old: old, New: []byte(r.Text)}
		if r.CL3 != nil {
			e.CL3 = *r.CL3
		}
		if r.Meta.BlobOffset != nil {
			e.Blob = *r.Meta.BlobOffset
		}
		id := ""
		switch e.Type {
		case "gnl":
			validChain := false
			for _, k := range r.Chain {
				if k == "GBNL" || k == "GBIN" || k == "GSTR" {
					validChain = true
				}
			}
			if !validChain || e.Message < 0 || e.Field < 0 || e.Blob < -1 || e.CL3 < -1 || (e.CL3 == -1 && e.Blob != -1) {
				return plan, fmt.Errorf("%s: invalid/unsupported GBNL location", r.RecordKey)
			}
			id = fmt.Sprintf("%d/%d/gnl/%d/%d/%d/%d", e.Archive, e.GDAT, e.CL3, e.Blob, e.Message, e.Field)
		case "enc_row":
			if r.Meta.Row == nil || r.Meta.StrOffset == nil || r.Meta.SlotCapacity != nil && *r.Meta.SlotCapacity < len(old) || e.CL3 != -1 {
				return plan, fmt.Errorf("%s: invalid ENC slot metadata", r.RecordKey)
			}
			e.Row = *r.Meta.Row
			e.Offset = *r.Meta.StrOffset
			if e.Row < 0 || e.Offset < 0 {
				return plan, fmt.Errorf("%s: negative ENC location", r.RecordKey)
			}
			if r.Meta.SlotCapacity != nil {
				e.Capacity = *r.Meta.SlotCapacity
			}
			id = fmt.Sprintf("%d/%d/enc/%d/%d", e.Archive, e.GDAT, e.Row, e.Offset)
		case "bin_string":
			if r.Meta.StringIndex == nil || *r.Meta.StringIndex < 0 || e.CL3 != -1 {
				return plan, fmt.Errorf("%s: invalid BIN metadata", r.RecordKey)
			}
			e.StringIndex = *r.Meta.StringIndex
			id = fmt.Sprintf("%d/%d/bin/%d", e.Archive, e.GDAT, e.StringIndex)
		default:
			return plan, fmt.Errorf("%s: changed record uses unsupported blob type %q", r.RecordKey, e.Type)
		}
		if identities[id] {
			return plan, fmt.Errorf("%s: duplicate physical text location %s", r.RecordKey, id)
		}
		identities[id] = true
		archives[e.Archive] = true
		plan.Edits = append(plan.Edits, e)
	}
	if err := sc.Err(); err != nil {
		return plan, err
	}
	plan.ChangedRecords = len(plan.Edits)
	for a := range archives {
		plan.Archives = append(plan.Archives, a)
	}
	sort.Ints(plan.Archives)
	sort.Slice(plan.Edits, func(i, j int) bool {
		a, b := plan.Edits[i], plan.Edits[j]
		if a.Archive != b.Archive {
			return a.Archive < b.Archive
		}
		if a.GDAT != b.GDAT {
			return a.GDAT < b.GDAT
		}
		return a.RecordKey < b.RecordKey
	})
	return plan, nil
}
