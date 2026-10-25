package switchtext

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (Options, string) {
	t.Helper()
	p := t.TempDir()
	mroot := filepath.Join(p, "toolchain", "switch_port", "maps")
	for _, name := range canonical["ms2"] {
		write(t, filepath.Join(p, "translations", name), "")
	}
	old := "Hello"
	same := "Jack"
	record := fmt.Sprintf(`{"id":"R01","fields":[{"LOCALIZED":%q,"EN":"Hello, you!","mapping_status":"EXACT_TEST"},{"LOCALIZED":%q,"EN":"Jack","mapping_status":"EXACT_TEST"}]}`+"\n", old, same)
	write(t, filepath.Join(p, "translations", "MS2_STORY.jsonl"), record)
	key1 := "MS2_SWITCH_WESTERN/a071/g00000/r00000/f001"
	key2 := "MS2_SWITCH_WESTERN/a071/g00000/r00000/f000"
	mapRows := []Mapping{{TargetKey: key1, PCFile: "MS2_STORY.jsonl", PCRecordID: "R01", FieldOrdinal: 0, OldSHA: hashBytes([]byte(old))}, {TargetKey: key2, PCFile: "MS2_STORY.jsonl", PCRecordID: "R01", FieldOrdinal: 1, OldSHA: hashBytes([]byte(same))}}
	var gzData strings.Builder
	// Write compressed data via real file because hashing must pin compressed bytes.
	gzPath := filepath.Join(mroot, "ms2-stable-alignment.jsonl.gz")
	if e := os.MkdirAll(mroot, 0700); e != nil {
		t.Fatal(e)
	}
	f, e := os.Create(gzPath)
	if e != nil {
		t.Fatal(e)
	}
	gz := gzip.NewWriter(f)
	for _, r := range mapRows {
		b, _ := json.Marshal(r)
		if _, e = gz.Write(append(b, '\n')); e != nil {
			t.Fatal(e)
		}
	}
	if e = gz.Close(); e != nil {
		t.Fatal(e)
	}
	f.Close()
	_ = gzData
	mapSHA, e := hashFile(gzPath)
	if e != nil {
		t.Fatal(e)
	}
	native := filepath.Join(p, ".local", "switch_text_port", "ms2", "western-native-records.jsonl")
	mk := func(key, txt string) string {
		return fmt.Sprintf(`{"record_key":%q,"source_dataset":"MS2_SWITCH_WESTERN","text":%q,"raw_hex":%q,"encoding":"utf-8","decode_errors":0,"archive_number":71,"repack_metadata":{"blob_type":"enc_row"}}`+"\n", key, txt, hex.EncodeToString([]byte(txt)))
	}
	write(t, native, mk(key1, old)+mk(key2, same))
	sha, e := hashFile(native)
	if e != nil {
		t.Fatal(e)
	}
	man := fmt.Sprintf(`{"schema_version":1,"release_ready":false,"games":{"ms2":{"map_file":"ms2-stable-alignment.jsonl.gz","map_sha256":%q,"native_records_sha256":%q,"mapped_fields":2}}}`, mapSHA, sha)
	write(t, filepath.Join(mroot, "manifest.json"), man)
	return Options{ProjectRoot: p, Game: "ms2", NativeRecords: native, Output: filepath.Join(p, ".local", "research", "ms2-edits.jsonl")}, native
}
func TestStageUsesCurrentPCENAndProtectsSources(t *testing.T) {
	o, native := fixture(t)
	sourceSHA, e := hashFile(native)
	if e != nil {
		t.Fatal(e)
	}
	got, e := Stage(o)
	if e != nil {
		t.Fatal(e)
	}
	if got.EditedFields != 1 || got.UnchangedFields != 1 || got.MappedFields != 2 || len(got.EditedArchives) != 1 || got.EditedArchives[0] != 71 {
		t.Fatalf("wrong report: %+v", got)
	}
	bytes, e := os.ReadFile(o.Output)
	if e != nil {
		t.Fatal(e)
	}
	var row struct {
		Text string `json:"text"`
		Raw  string `json:"raw_hex"`
	}
	if e = json.Unmarshal(bytes, &row); e != nil {
		t.Fatal(e)
	}
	if row.Text != "Hello, you!" || row.Raw != hex.EncodeToString([]byte("Hello")) {
		t.Fatalf("bad data %+v", row)
	}
	if newSHA, _ := hashFile(native); newSHA != sourceSHA {
		t.Fatal("source altered")
	}
	if _, e = Stage(o); e == nil || !strings.Contains(e.Error(), "output exists") {
		t.Fatal("stale result not blocked", e)
	}
	// Change only canonical EN. Stable mapping stays byte-identical.
	rec := `{"id":"R01","fields":[{"LOCALIZED":"Hello","EN":"Hello, Toojima-sensei!","mapping_status":"EXACT_TEST"},{"LOCALIZED":"Jack","EN":"Jack","mapping_status":"EXACT_TEST"}]}` + "\n"
	write(t, filepath.Join(o.ProjectRoot, "translations", "MS2_STORY.jsonl"), rec)
	o.Output = filepath.Join(o.ProjectRoot, ".local", "research", "ms2-second.jsonl")
	got, e = Stage(o)
	if e != nil {
		t.Fatal(e)
	}
	next, e := os.ReadFile(o.Output)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(string(next), "Hello, Toojima-sensei!") || got.GeneratedSHA == hashBytes(bytes) {
		t.Fatal("new EN not regenerated")
	}
}
func TestStageRejectsSourceTampering(t *testing.T) {
	o, native := fixture(t)
	write(t, native, "{}\n")
	if _, e := Stage(o); e == nil || !strings.Contains(e.Error(), "SHA mismatch") {
		t.Fatalf("must block altered Western source, got %v", e)
	}
	if _, e := os.Stat(o.Output); !os.IsNotExist(e) {
		t.Fatal("stale output written")
	}
}
func TestStageRejectsCanonicalOriginalDrift(t *testing.T) {
	o, _ := fixture(t)
	rec := `{"id":"R01","fields":[{"LOCALIZED":"Changed original","EN":"EN","mapping_status":"EXACT_TEST"},{"LOCALIZED":"Jack","EN":"Jack","mapping_status":"EXACT_TEST"}]}` + "\n"
	write(t, filepath.Join(o.ProjectRoot, "translations", "MS2_STORY.jsonl"), rec)
	if _, e := Stage(o); e == nil || !strings.Contains(e.Error(), "original localized text drift") {
		t.Fatalf("must block map drift, got %v", e)
	}
}
func TestStageRejectsUnprovenEncoding(t *testing.T) {
	o, native := fixture(t)
	raw, _ := os.ReadFile(native)
	raw = []byte(strings.ReplaceAll(string(raw), `"encoding":"utf-8"`, `"encoding":"unsupported"`))
	write(t, native, string(raw))
	// Re-pin source digest in this test to specifically reach the encoding gate.
	manPath := filepath.Join(o.ProjectRoot, "toolchain", "switch_port", "maps", "manifest.json")
	mb, _ := os.ReadFile(manPath)
	var m map[string]any
	_ = json.Unmarshal(mb, &m)
	games := m["games"].(map[string]any)
	entry := games["ms2"].(map[string]any)
	sum := sha256.Sum256(raw)
	entry["native_records_sha256"] = hex.EncodeToString(sum[:])
	b, _ := json.Marshal(m)
	write(t, manPath, string(b))
	if _, e := Stage(o); e == nil || !strings.Contains(e.Error(), "unsupported native encoding") {
		t.Fatalf("must block unsupported encoder: %v", e)
	}
}

// Regression on the immutable research corpus: proves Go can follow every
// stable Western mapping back to the CURRENT canonical PC LOCALIZED source.
// No proprietary Switch archive or cached extracted native record is required.
func TestCurrentPCMatchesFrozenWesternAlignment(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, game := range []string{"ms2", "finale"} {
		mroot := filepath.Join(root, "toolchain", "switch_port", "maps")
		manBytes, e := os.ReadFile(filepath.Join(mroot, "manifest.json"))
		if e != nil {
			t.Fatal(e)
		}
		var man Manifest
		if e = json.Unmarshal(manBytes, &man); e != nil {
			t.Fatal(e)
		}
		ent := man.Games[game]
		path := filepath.Join(mroot, ent.MapFile)
		sha, e := hashFile(path)
		if e != nil || sha != ent.MapSHA {
			t.Fatalf("%s mapping SHA drift: %v", game, e)
		}
		f, e := os.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		gz, e := gzip.NewReader(f)
		if e != nil {
			f.Close()
			t.Fatal(e)
		}
		refs := map[string][]Mapping{}
		targetIDs := map[string]bool{}
		count := 0
		e = streamLines(gz, func(b []byte) error {
			var m Mapping
			if e := json.Unmarshal(b, &m); e != nil {
				return e
			}
			if targetIDs[m.TargetKey] {
				return fmt.Errorf("duplicate target")
			}
			targetIDs[m.TargetKey] = true
			refs[pair(m.PCFile, m.PCRecordID)] = append(refs[pair(m.PCFile, m.PCRecordID)], m)
			count++
			return nil
		})
		gz.Close()
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		if count != ent.MappedFields {
			t.Fatalf("%s: map count %d != %d", game, count, ent.MappedFields)
		}
		matched := 0
		for _, name := range canonical[game] {
			fp, e := os.Open(filepath.Join(root, "translations", name))
			if e != nil {
				t.Fatal(e)
			}
			e = streamLines(fp, func(b []byte) error {
				var r PCRecord
				if e := json.Unmarshal(b, &r); e != nil {
					return e
				}
				key := pair(name, r.ID)
				for _, m := range refs[key] {
					if m.FieldOrdinal >= len(r.Fields) {
						return fmt.Errorf("field missing: %s", m.TargetKey)
					}
					if hashBytes([]byte(r.Fields[m.FieldOrdinal].Localized)) != m.OldSHA {
						return fmt.Errorf("source mismatch: %s", m.TargetKey)
					}
					if !strings.HasPrefix(r.Fields[m.FieldOrdinal].Status, "EXACT_") {
						return fmt.Errorf("unapproved mapping: %s", m.TargetKey)
					}
					matched++
				}
				delete(refs, key)
				return nil
			})
			fp.Close()
			if e != nil {
				t.Fatalf("%s/%s: %v", game, name, e)
			}
		}
		if matched != ent.MappedFields || len(refs) != 0 {
			t.Fatalf("%s: matched %d/%d, missing %d PC IDs", game, matched, ent.MappedFields, len(refs))
		}
		t.Logf("%s: %d frozen Western mappings match current PC translations", game, matched)
	}
}
