package maintainer

// Experimental, source-pinned PC GDAT text writer. This connects 's typed
// edit plan to existing native game-format writers. It is deliberately NOT
// wired into the production pipeline until actual owner PC DAT/output parity
// and BILZ canonical compression have been independently verified.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"maryskelter-unburger/internal/mskit"
)

// PCTextWriteReport describes the exact scope of a verified GDAT rewrite.
type PCTextWriteReport struct {
	Dataset        string
	Archive        int
	SourceSHA256   string
	OutputSHA256   string
	TotalEntries   int
	ChangedEntries []int
	ChangedFields  int
}

// RebuildPCTextArchiveNative never modifies source. Every changed entry is
// verified against the declared pristine archive SHA and original field bytes.
// The supplied encoder must be independently established as canonical for
// compressed BILZ entries; this API never silently selects Go's zlib writer.
func RebuildPCTextArchiveNative(source []byte, dataset string, archive int, plan PCTextPlan, encode mskit.CanonicalBILZEncoder) ([]byte, PCTextWriteReport, error) {
	report := PCTextWriteReport{Dataset: dataset, Archive: archive}
	if dataset != "MS2_PC_LOCALIZED" && dataset != "MSF_PC_LOCALIZED" && dataset != "MSF_PC_SYSTEM" {
		return nil, report, fmt.Errorf("unknown PC text dataset %q", dataset)
	}
	if archive < 0 || archive > 999 || plan.Dataset != dataset {
		return nil, report, fmt.Errorf("dataset/archive mismatch")
	}
	original, err := mskit.ParseGDAT(source)
	if err != nil {
		return nil, report, fmt.Errorf("source GDAT invalid: %w", err)
	}
	sum := sha256.Sum256(source)
	report.SourceSHA256 = hex.EncodeToString(sum[:])
	report.TotalEntries = len(original.Entries)
	groups := make(map[int][]PCTextEdit)
	seen := make(map[string]bool)
	filename := fmt.Sprintf("GxArchivedFile%03d.dat", archive)
	for _, e := range plan.Edits {
		if e.Archive != archive {
			continue
		}
		if e.SourceFile != filename || !strings.EqualFold(e.SourceSHA256, report.SourceSHA256) {
			return nil, report, fmt.Errorf("%s: source hash/name mismatch; refuse to write", e.RecordKey)
		}
		if e.GDAT < 0 || e.GDAT >= len(original.Entries) || e.RecordKey == "" ||
			!strings.HasPrefix(e.RecordKey, fmt.Sprintf("%s/a%03d/g%05d/", dataset, archive, e.GDAT)) {
			return nil, report, fmt.Errorf("%s: invalid physical edit identity", e.RecordKey)
		}
		if !utf8.Valid(e.New) || bytes.IndexByte(e.New, 0) >= 0 || bytes.Equal(e.Old, e.New) {
			return nil, report, fmt.Errorf("%s: invalid/unchanged replacement", e.RecordKey)
		}
		key := fmt.Sprintf("%d/%s/%d/%d/%d/%d/%d/%d/%d", e.GDAT, e.Type, e.CL3, e.Blob, e.Message, e.Field, e.Row, e.Offset, e.StringIndex)
		if seen[key] {
			return nil, report, fmt.Errorf("%s: duplicate physical edit", e.RecordKey)
		}
		seen[key] = true
		groups[e.GDAT] = append(groups[e.GDAT], e)
	}
	if len(groups) == 0 {
		out := append([]byte(nil), source...)
		report.OutputSHA256 = report.SourceSHA256
		return out, report, nil
	}
	indices := make([]int, 0, len(groups))
	for idx := range groups {
		indices = append(indices, idx)
	}
	sort.Ints(indices)
	entries := make([][]byte, len(original.Entries))
	copy(entries, original.Entries)
	for _, idx := range indices {
		edits := groups[idx]
		kind := edits[0].Type
		for _, e := range edits[1:] {
			if e.Type != kind {
				return nil, report, fmt.Errorf("gdat %d: mixed writer kinds are not proven", idx)
			}
		}
		var rebuilt []byte
		switch kind {
		case "gnl":
			gs := make([]mskit.GBNLTextEdit, 0, len(edits))
			for _, e := range edits {
				if e.CL3 < -1 || e.Blob < -1 || (e.CL3 == -1 && e.Blob != -1) || e.Message < 0 || e.Field < 0 {
					return nil, report, fmt.Errorf("%s: invalid GBNL route", e.RecordKey)
				}
				gs = append(gs, mskit.GBNLTextEdit{CL3Index: e.CL3, BlobOffset: e.Blob, Message: e.Message, Field: e.Field, Old: e.Old, New: e.New, RecordKey: e.RecordKey})
			}
			rebuilt, err = mskit.RebuildGBNLTree(entries[idx], gs, encode)
		case "enc_row":
			es := make([]mskit.ENCEdit, 0, len(edits))
			for _, e := range edits {
				if e.Row < 0 || e.Offset < 0 || e.Capacity < 0 || e.CL3 != -1 {
					return nil, report, fmt.Errorf("%s: invalid ENC route", e.RecordKey)
				}
				es = append(es, mskit.ENCEdit{Row: e.Row, Offset: e.Offset, Old: e.Old, New: e.New, Capacity: e.Capacity, RecordKey: e.RecordKey})
			}
			rebuilt, err = mskit.RebuildENCEntry(entries[idx], es, encode)
		case "bin_string":
			b, parseErr := mskit.ParseBINEntry(entries[idx])
			if parseErr != nil {
				err = parseErr
				break
			}
			old, getErr := b.Strings()
			if getErr != nil {
				err = getErr
				break
			}
			editsBIN := make(map[int][]byte)
			for _, e := range edits {
				if e.StringIndex < 0 || e.StringIndex >= len(old) || !bytes.Equal(old[e.StringIndex], e.Old) {
					return nil, report, fmt.Errorf("%s: BIN original bytes/position mismatch", e.RecordKey)
				}
				if _, exists := editsBIN[e.StringIndex]; exists {
					return nil, report, fmt.Errorf("%s: duplicate BIN string", e.RecordKey)
				}
				editsBIN[e.StringIndex] = e.New
			}
			rebuilt, err = mskit.RebuildBINEntry(entries[idx], editsBIN, encode)
		default:
			return nil, report, fmt.Errorf("gdat %d: unsupported changed text kind %q", idx, kind)
		}
		if err != nil {
			return nil, report, fmt.Errorf("gdat %d %s: %w", idx, kind, err)
		}
		entries[idx] = rebuilt
		report.ChangedEntries = append(report.ChangedEntries, idx)
		report.ChangedFields += len(edits)
	}
	out, err := mskit.BuildGDAT(entries)
	if err != nil {
		return nil, report, err
	}
	check, err := mskit.ParseGDAT(out)
	if err != nil {
		return nil, report, fmt.Errorf("rebuilt GDAT unparseable: %w", err)
	}
	for idx := range original.Entries {
		if _, modified := groups[idx]; !modified && !bytes.Equal(original.Entries[idx], check.Entries[idx]) {
			return nil, report, fmt.Errorf("gdat %d: unchanged entry mutated", idx)
		}
	}
	hash := sha256.Sum256(out)
	report.OutputSHA256 = hex.EncodeToString(hash[:])
	return out, report, nil
}
