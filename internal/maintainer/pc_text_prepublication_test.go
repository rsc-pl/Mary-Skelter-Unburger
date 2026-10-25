package maintainer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeDatasetPrepublicationVerify(t *testing.T) {
	root, master, records, pins, out, _ := makeNativeDataset(t)
	generated := filepath.Join(root, "original.jsonl")
	if _, e := ExtractSourceDatasetGo("MS2_PC_LOCALIZED", master, []int{52, 53}, generated, nil); e != nil {
		t.Fatal(e)
	}
	contents, e := os.ReadFile(generated)
	if e != nil {
		t.Fatal(e)
	}
	var outrows bytes.Buffer
	for _, line := range bytes.Split(bytes.TrimSpace(contents), []byte("\n")) {
		var m map[string]interface{}
		if e := json.Unmarshal(line, &m); e != nil {
			t.Fatal(e)
		}
		if m["record_index"].(float64) == 0 {
			m["text"] = fmt.Sprintf("Shirayuki-%03.0f", m["archive_number"].(float64))
		}
		b, _ := json.Marshal(m)
		outrows.Write(b)
		outrows.WriteByte('\n')
	}
	if e := os.WriteFile(records, outrows.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	rep := filepath.Join(root, "report.json")
	result, e := RebuildPCTextDatasetNativeVerified("MS2_PC_LOCALIZED", master, records, out, rep, pins, false)
	if e != nil {
		t.Fatal(e)
	}
	if !result.GoDecodedVerified {
		t.Fatal("native prepublication verification bit not asserted")
	}
	entries, e := os.ReadDir(out)
	if e != nil || len(entries) != 1 || entries[0].Name() != "masterWindows" {
		t.Fatalf("staging temp files leaked: %v %v", entries, e)
	}
	r, e := VerifyTextDatasetGoStrict("MS2_PC_LOCALIZED", filepath.Join(out, "masterWindows"), records, master, []int{52, 53}, filepath.Join(root, "verification.json"))
	t.Logf("result=%v err=%v", r["ok"], e)
	if e != nil {
		t.Fatal(e)
	}
}

// Old legacy synthetic record identifiers are writer-valid but cannot match
// original source-extraction identities. Main Rebuild must therefore reject
// them before publishing anything, even though the isolated writer succeeds.
func TestRejectUnverifiableBeforePublication(t *testing.T) {
	root, master, records, pins, out, originals := makeNativeDataset(t)
	report := filepath.Join(root, "report.json")
	_, err := RebuildPCTextDatasetNativeVerified("MS2_PC_LOCALIZED", master, records, out, report, pins, false)
	if err == nil || !strings.Contains(err.Error(), "prepublication decoded verification") {
		t.Fatalf("missing verification rejection: %v", err)
	}
	if _, err := os.Lstat(out); !os.IsNotExist(err) {
		t.Fatalf("failed output published: %v", err)
	}
	if _, err := os.Lstat(report); !os.IsNotExist(err) {
		t.Fatalf("failed report published: %v", err)
	}
	for i, num := range []int{52, 53} {
		b, e := os.ReadFile(filepath.Join(master, fmt.Sprintf("GxArchivedFile%03d.dat", num)))
		if e != nil || !bytes.Equal(b, originals[i]) {
			t.Fatal("owner DAT modified after strict failure")
		}
	}
}
