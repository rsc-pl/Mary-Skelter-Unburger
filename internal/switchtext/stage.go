// Package switchtext generates transient Western Switch text edits from the
// canonical PC translations. It does not maintain a second translation tree.
// This research API is intentionally not exposed by the production Go TUI.
package switchtext

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

var canonical = map[string][]string{
	"ms2":    {"MS2_STORY.jsonl", "MS2_SYSTEM_BIN_055.jsonl", "MS2_SYSTEM_ENC_052.jsonl", "MS2_SYSTEM_ENC_061.jsonl"},
	"finale": {"MSF_STORY.jsonl", "MSF_ARCHIVE_088.jsonl", "MSF_SYSTEM_ENC_055.jsonl", "MSF_SYSTEM_ENC_072.jsonl", "MSF_SYSTEM_ENC_088.jsonl", "MSF_SYSTEM_GDAT0.jsonl"},
}
var datasets = map[string]string{"ms2": "MS2_SWITCH_WESTERN", "finale": "MSF_SWITCH_WESTERN"}

type Mapping struct {
	TargetKey    string `json:"target_key"`
	PCFile       string `json:"pc_file"`
	PCRecordID   string `json:"pc_record_id"`
	FieldOrdinal int    `json:"field_ordinal"`
	OldSHA       string `json:"old_text_sha256"`
}
type Manifest struct {
	SchemaVersion int  `json:"schema_version"`
	ReleaseReady  bool `json:"release_ready"`
	Games         map[string]struct {
		MapFile      string `json:"map_file"`
		MapSHA       string `json:"map_sha256"`
		NativeSHA    string `json:"native_records_sha256"`
		MappedFields int    `json:"mapped_fields"`
	} `json:"games"`
}
type PCField struct {
	Localized string `json:"LOCALIZED"`
	EN        string `json:"EN"`
	Status    string `json:"mapping_status"`
}
type PCRecord struct {
	ID     string    `json:"id"`
	Fields []PCField `json:"fields"`
}
type Options struct {
	ProjectRoot, Game, NativeRecords, Output, MapRoot string
}
type Report struct {
	SchemaVersion     int               `json:"schema_version"`
	Game              string            `json:"game"`
	Status            string            `json:"status"`
	SourceRecordsSHA  string            `json:"source_records_sha256"`
	MappedFields      int               `json:"mapped_fields"`
	EditedFields      int               `json:"edited_fields"`
	UnchangedFields   int               `json:"unchanged_mapped_fields"`
	EditedArchives    []int             `json:"edited_archives"`
	TranslationHashes map[string]string `json:"translation_inputs_sha256"`
	AlignmentSHA      string            `json:"alignment_sha256"`
	GeneratedSHA      string            `json:"generated_edits_sha256"`
	Warning           string            `json:"warning"`
}

func hashBytes(p []byte) string { s := sha256.Sum256(p); return hex.EncodeToString(s[:]) }
func hashFile(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}
func streamLines(reader io.Reader, cb func([]byte) error) error {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 128*1024), 32*1024*1024)
	for s.Scan() {
		line := s.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		if e := cb(line); e != nil {
			return e
		}
	}
	return s.Err()
}
func pair(file, id string) string { return file + "\x00" + id }
func identity(game string, m Mapping) string {
	return pair(m.PCFile, m.PCRecordID) + fmt.Sprintf("\x00%d", m.FieldOrdinal)
}

// Stage atomically emits changed native records and reports without touching
// the sources. It rejects unknown encodings rather than silently approximating.
func Stage(o Options) (Report, error) {
	var report Report
	names, ok := canonical[o.Game]
	if !ok {
		return report, fmt.Errorf("unknown game %q", o.Game)
	}
	if o.MapRoot == "" {
		o.MapRoot = filepath.Join(o.ProjectRoot, "toolchain", "switch_port", "maps")
	}
	if o.NativeRecords == "" {
		o.NativeRecords = filepath.Join(o.ProjectRoot, ".local", "switch_text_port", o.Game, "western-native-records.jsonl")
	}
	if o.Output == "" {
		return report, fmt.Errorf("output path required")
	}
	// A stale stage must never masquerade as a new PC translation build.
	if _, e := os.Lstat(o.Output); e == nil {
		return report, fmt.Errorf("output exists; refusing overwrite: %s", o.Output)
	} else if !os.IsNotExist(e) {
		return report, e
	}
	manData, e := os.ReadFile(filepath.Join(o.MapRoot, "manifest.json"))
	if e != nil {
		return report, e
	}
	var man Manifest
	if e = json.Unmarshal(manData, &man); e != nil {
		return report, e
	}
	if man.SchemaVersion != 1 || man.ReleaseReady {
		return report, fmt.Errorf("expected research-only stable mapping manifest")
	}
	entry, ok := man.Games[o.Game]
	if !ok {
		return report, fmt.Errorf("missing manifest game %s", o.Game)
	}
	if filepath.Base(entry.MapFile) != entry.MapFile {
		return report, fmt.Errorf("unsafe mapping name")
	}
	mapFile := filepath.Join(o.MapRoot, entry.MapFile)
	mapSHA, e := hashFile(mapFile)
	if e != nil {
		return report, e
	}
	if mapSHA != entry.MapSHA {
		return report, fmt.Errorf("stable map checksum mismatch")
	}
	f, e := os.Open(mapFile)
	if e != nil {
		return report, e
	}
	gz, e := gzip.NewReader(f)
	if e != nil {
		f.Close()
		return report, e
	}
	mappings := make(map[string]Mapping, entry.MappedFields)
	needed := map[string]bool{}
	uniqueIDs := map[string]bool{}
	e = streamLines(gz, func(line []byte) error {
		var m Mapping
		if e := json.Unmarshal(line, &m); e != nil {
			return e
		}
		validFile := false
		for _, name := range names {
			if name == m.PCFile {
				validFile = true
				break
			}
		}
		if !validFile || !strings.HasPrefix(m.TargetKey, datasets[o.Game]+"/") || m.FieldOrdinal < 0 || len(m.OldSHA) != 64 || m.PCRecordID == "" {
			return fmt.Errorf("invalid mapping %s", m.TargetKey)
		}
		if _, ok := mappings[m.TargetKey]; ok {
			return fmt.Errorf("duplicate Switch target %s", m.TargetKey)
		}
		if uniqueIDs[identity(o.Game, m)] {
			return fmt.Errorf("duplicate PC field %s", m.TargetKey)
		}
		uniqueIDs[identity(o.Game, m)] = true
		mappings[m.TargetKey] = m
		needed[pair(m.PCFile, m.PCRecordID)] = true
		return nil
	})
	gz.Close()
	f.Close()
	if e != nil {
		return report, e
	}
	if len(mappings) != entry.MappedFields {
		return report, fmt.Errorf("map count mismatch %d != %d", len(mappings), entry.MappedFields)
	}
	nativeSHA, e := hashFile(o.NativeRecords)
	if e != nil {
		return report, e
	}
	if nativeSHA != entry.NativeSHA {
		return report, fmt.Errorf("pristine Western native records SHA mismatch")
	}
	pc := make(map[string]PCRecord, len(needed))
	inputSHA := map[string]string{}
	for _, name := range names {
		path := filepath.Join(o.ProjectRoot, "translations", name)
		sha, e := hashFile(path)
		if e != nil {
			return report, e
		}
		inputSHA[name] = sha
		file, e := os.Open(path)
		if e != nil {
			return report, e
		}
		seen := map[string]bool{}
		e = streamLines(file, func(line []byte) error {
			var rec PCRecord
			if e := json.Unmarshal(line, &rec); e != nil {
				return e
			}
			id := pair(name, rec.ID)
			if seen[id] {
				return fmt.Errorf("duplicate canonical PC record %s", id)
			}
			seen[id] = true
			if needed[id] {
				pc[id] = rec
			}
			return nil
		})
		file.Close()
		if e != nil {
			return report, fmt.Errorf("%s: %w", name, e)
		}
	}
	if len(pc) != len(needed) {
		return report, fmt.Errorf("%d missing canonical PC records", len(needed)-len(pc))
	}
	targets := make(map[string]string, len(mappings))
	for key, m := range mappings {
		rec := pc[pair(m.PCFile, m.PCRecordID)]
		if m.FieldOrdinal >= len(rec.Fields) {
			return report, fmt.Errorf("%s: field index out of range", key)
		}
		field := rec.Fields[m.FieldOrdinal]
		if !strings.HasPrefix(field.Status, "EXACT_") {
			return report, fmt.Errorf("%s: PC field no longer exact", key)
		}
		if hashBytes([]byte(field.Localized)) != m.OldSHA {
			return report, fmt.Errorf("%s: PC original localized text drift", key)
		}
		if strings.ContainsRune(field.EN, 0) || !utf8.ValidString(field.EN) {
			return report, fmt.Errorf("%s: invalid EN", key)
		}
		targets[key] = field.EN
	}
	if e = os.MkdirAll(filepath.Dir(o.Output), 0700); e != nil {
		return report, e
	}
	temp, e := os.CreateTemp(filepath.Dir(o.Output), ".switch-live-edits-*")
	if e != nil {
		return report, e
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	writer := bufio.NewWriterSize(temp, 1024*1024)
	h := sha256.New()
	outWriter := io.MultiWriter(writer, h)
	seenTargets := map[string]bool{}
	edited := 0
	unchanged := 0
	archives := map[int]bool{}
	file, e := os.Open(o.NativeRecords)
	if e != nil {
		return report, e
	}
	e = streamLines(file, func(line []byte) error {
		// Decode only relevant native records but retain ALL original metadata.
		var basic struct {
			RecordKey string `json:"record_key"`
		}
		if e := json.Unmarshal(line, &basic); e != nil {
			return e
		}
		m, found := mappings[basic.RecordKey]
		if !found {
			return nil
		}
		if seenTargets[basic.RecordKey] {
			return fmt.Errorf("duplicate native key %s", basic.RecordKey)
		}
		seenTargets[basic.RecordKey] = true
		var row struct {
			RecordKey    string `json:"record_key"`
			Dataset      string `json:"source_dataset"`
			Text         string `json:"text"`
			RawHex       string `json:"raw_hex"`
			Encoding     string `json:"encoding"`
			DecodeErrors int    `json:"decode_errors"`
			Archive      int    `json:"archive_number"`
			Metadata     struct {
				Blob string `json:"blob_type"`
			} `json:"repack_metadata"`
		}
		if e := json.Unmarshal(line, &row); e != nil {
			return e
		}
		if row.Dataset != datasets[o.Game] || row.DecodeErrors != 0 || hashBytes([]byte(row.Text)) != m.OldSHA {
			return fmt.Errorf("%s: native identity drift", basic.RecordKey)
		}
		raw, e := hex.DecodeString(row.RawHex)
		if e != nil {
			return fmt.Errorf("%s: malformed source raw bytes", basic.RecordKey)
		}
		if row.Metadata.Blob != "gnl" && row.Metadata.Blob != "enc_row" && row.Metadata.Blob != "bin_string" {
			return fmt.Errorf("%s: unsupported edit chain", basic.RecordKey)
		}
		// These Western records use UTF-8. CP932 is safe only for pure ASCII;
		// other code pages must have a proven native encoder before Go release.
		encode := func(s string) ([]byte, error) {
			switch strings.ToLower(row.Encoding) {
			case "utf-8", "utf8":
				return []byte(s), nil
			case "cp932":
				for _, r := range s {
					if r > 127 {
						return nil, fmt.Errorf("CP932 non-ASCII needs proven encoder")
					}
				}
				return []byte(s), nil
			default:
				return nil, fmt.Errorf("unsupported native encoding %q", row.Encoding)
			}
		}
		sourceBytes, e := encode(row.Text)
		if e != nil {
			return fmt.Errorf("%s: %w", basic.RecordKey, e)
		}
		if string(sourceBytes) != string(raw) {
			return fmt.Errorf("%s: original byte parity failure", basic.RecordKey)
		}
		newBytes, e := encode(targets[basic.RecordKey])
		if e != nil {
			return fmt.Errorf("%s: %w", basic.RecordKey, e)
		}
		if targets[basic.RecordKey] == row.Text {
			unchanged++
			return nil
		}
		if len(newBytes) == 0 || strings.IndexByte(string(newBytes), 0) >= 0 {
			return fmt.Errorf("%s: empty/NUL target", basic.RecordKey)
		}
		var obj map[string]json.RawMessage
		if e := json.Unmarshal(line, &obj); e != nil {
			return e
		}
		replacement, e := json.Marshal(targets[basic.RecordKey])
		if e != nil {
			return e
		}
		obj["text"] = replacement
		encoded, e := json.Marshal(obj)
		if e != nil {
			return e
		}
		if _, e = outWriter.Write(append(encoded, '\n')); e != nil {
			return e
		}
		edited++
		archives[row.Archive] = true
		return nil
	})
	file.Close()
	if e != nil {
		return report, e
	}
	if len(seenTargets) != len(mappings) {
		return report, fmt.Errorf("%d mapped Western targets missing", len(mappings)-len(seenTargets))
	}
	if e = writer.Flush(); e != nil {
		return report, e
	}
	if e = temp.Sync(); e != nil {
		return report, e
	}
	if e = temp.Close(); e != nil {
		return report, e
	}
	archiveList := make([]int, 0, len(archives))
	for a := range archives {
		archiveList = append(archiveList, a)
	}
	sort.Ints(archiveList)
	report = Report{SchemaVersion: 1, Game: o.Game, Status: "NATIVE_CANDIDATE_STAGED_NOT_RELEASE_READY", SourceRecordsSHA: entry.NativeSHA, MappedFields: len(mappings), EditedFields: edited, UnchangedFields: unchanged, EditedArchives: archiveList, TranslationHashes: inputSHA, AlignmentSHA: entry.MapSHA, GeneratedSHA: hex.EncodeToString(h.Sum(nil)), Warning: "Unmatched Switch records, format fit and native rebuild/runtime are not yet approved."}
	// File creation exclusive: no accidental stale file reuse. No copies/links.
	if _, e = os.Lstat(o.Output); e == nil {
		return Report{}, fmt.Errorf("output created concurrently: %s", o.Output)
	} else if !os.IsNotExist(e) {
		return Report{}, e
	}
	if e = os.Rename(temp.Name(), o.Output); e != nil {
		return Report{}, e
	}
	payload, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return Report{}, e
	}
	reportPath := strings.TrimSuffix(o.Output, filepath.Ext(o.Output)) + ".report.json"
	if e = os.WriteFile(reportPath, append(payload, '\n'), 0600); e != nil {
		os.Remove(o.Output)
		return Report{}, e
	}
	return report, nil
}
