package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

type batchFixture struct {
	Manifest         string
	Sources, Outputs []string
	Before           [][]byte
}

func makeBatchFixture(t *testing.T) batchFixture {
	t.Helper()
	d := t.TempDir()
	golden := privateFixturePath(t, "text-codec-golden-vectors")
	original, err := os.ReadFile(filepath.Join(golden, "gbnl_one_sharer.input.bin"))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := mskit.BuildGDAT([][]byte{original, []byte("UNCHANGED")})
	if err != nil {
		t.Fatal(err)
	}
	jobs := []pcBatchJob{}
	f := batchFixture{Sources: []string{}, Outputs: []string{}, Before: [][]byte{}}
	for _, a := range []int{52, 55} {
		source := filepath.Join(d, fmt.Sprintf("GxArchivedFile%03d.dat", a))
		if err := os.WriteFile(source, baseline, 0600); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(baseline)
		r := map[string]any{"record_key": fmt.Sprintf("MS2_PC_LOCALIZED/a%03d/g00000/direct/r00000/f0", a),
			"source_dataset": "MS2_PC_LOCALIZED", "source_file": filepath.Base(source), "source_file_sha256": hex.EncodeToString(hash[:]),
			"archive_number": a, "gdat_index": 0, "record_index": 0, "field_index": 0, "encoding": "utf-8",
			"text": "Shirayuki...", "raw_hex": hex.EncodeToString([]byte("Snow White...")),
			"format_chain": []string{"GBNL"}, "repack_metadata": map[string]any{"blob_type": "gnl"}}
		b, _ := json.Marshal(r)
		records := filepath.Join(d, fmt.Sprintf("rec-%03d.jsonl", a))
		if err := os.WriteFile(records, append(b, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(d, fmt.Sprintf("new-%03d.dat", a))
		jobs = append(jobs, pcBatchJob{Dataset: "MS2_PC_LOCALIZED", Archive: a, Records: filepath.Base(records), Source: filepath.Base(source), Output: filepath.Base(out)})
		f.Sources = append(f.Sources, source)
		f.Outputs = append(f.Outputs, out)
		f.Before = append(f.Before, append([]byte(nil), baseline...))
	}
	manifest := filepath.Join(d, "batch.json")
	payload, _ := json.Marshal(pcBatchManifest{Version: 1, Jobs: jobs})
	if err := os.WriteFile(manifest, payload, 0600); err != nil {
		t.Fatal(err)
	}
	f.Manifest = manifest
	return f
}
func testBatchInputsUnchanged(t *testing.T, f batchFixture) {
	t.Helper()
	for i, p := range f.Sources {
		raw, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(raw, f.Before[i]) {
			t.Fatalf("input %d mutated: %v", i, err)
		}
	}
}
func testBatchOutputsAbsent(t *testing.T, f batchFixture) {
	t.Helper()
	for i, p := range f.Outputs {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("output %d improperly published: %v", i, err)
		}
	}
}
func TestNativePCTextBatchTwoArchivesIndependentGolden(t *testing.T) {
	f := makeBatchFixture(t)
	var output bytes.Buffer
	if err := executeNativePCTextBatch([]string{"--manifest", f.Manifest}, &output); err != nil {
		t.Fatal(err)
	}
	var report pcBatchReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Files) != 2 || report.Status != "written-unapproved-for-game" || report.Encoder != "strict-no-new-zlib" {
		t.Fatal("unexpected report", output.String())
	}
	golden := filepath.Join(privateFixturePath(t, "text-codec-golden-vectors"), "gbnl_one_sharer.output.bin")
	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range f.Outputs {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		gd, err := mskit.ParseGDAT(b)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gd.Entries[0], expected) || !bytes.Equal(gd.Entries[1], []byte("UNCHANGED")) {
			t.Fatalf("batch output %d differs from legacy golden", i)
		}
		if stat, _ := os.Stat(path); stat.Mode().Perm() != 0600 {
			t.Fatal("output not private")
		}
	}
	testBatchInputsUnchanged(t, f)
	if err := executeNativePCTextBatch([]string{"--manifest", f.Manifest}, &bytes.Buffer{}); err == nil {
		t.Fatal("overwrote existing outputs")
	}
}
func TestNativePCTextBatchRejectsWholeBatchIfSecondSourceStale(t *testing.T) {
	f := makeBatchFixture(t)
	b, err := os.ReadFile(f.Sources[1])
	if err != nil {
		t.Fatal(err)
	}
	b = bytes.Replace(b, []byte("UNCHANGED"), []byte("UnCHANGED"), 1)
	if err := os.WriteFile(f.Sources[1], b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeNativePCTextBatch([]string{"--manifest", f.Manifest}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "source hash") {
		t.Fatalf("stale source accepted: %v", err)
	}
	testBatchOutputsAbsent(t, f)
}
func TestNativePCTextBatchFaultRollsBackFirstPublishedOutput(t *testing.T) {
	f := makeBatchFixture(t)
	injected := fmt.Errorf("test injected before publishing second job")
	err := executeNativePCTextBatchWithHook([]string{"--manifest", f.Manifest}, &bytes.Buffer{}, func(i int) error {
		if i == 1 {
			return injected
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("injected failure not detected: %v", err)
	}
	testBatchOutputsAbsent(t, f)
	testBatchInputsUnchanged(t, f)
}
func TestNativePCTextBatchRejectsExistingOutputAndDuplicateDestination(t *testing.T) {
	f := makeBatchFixture(t)
	if err := os.WriteFile(f.Outputs[1], []byte("PRIVATE-OWNER-FILE"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeNativePCTextBatch([]string{"--manifest", f.Manifest}, &bytes.Buffer{}); err == nil {
		t.Fatal("allowed existing user output")
	}
	if b, err := os.ReadFile(f.Outputs[1]); err != nil || string(b) != "PRIVATE-OWNER-FILE" {
		t.Fatal("changed existing owner file", err)
	}
	if _, err := os.Lstat(f.Outputs[0]); !os.IsNotExist(err) {
		t.Fatal("partial first output created")
	}
	if err := os.Remove(f.Outputs[1]); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest pcBatchManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Jobs[1].Output = manifest.Jobs[0].Output
	edited, _ := json.Marshal(manifest)
	if err := os.WriteFile(f.Manifest, edited, 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeNativePCTextBatch([]string{"--manifest", f.Manifest}, &bytes.Buffer{}); err == nil {
		t.Fatal("duplicate output accepted")
	}
	testBatchOutputsAbsent(t, f)
}
func TestNativePCTextBatchRefusesNoopAndWrongVersion(t *testing.T) {
	f := makeBatchFixture(t)
	b, err := os.ReadFile(f.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var m pcBatchManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m.Version = 99
	wrong, _ := json.Marshal(m)
	if err := os.WriteFile(f.Manifest, wrong, 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeNativePCTextBatch([]string{"--manifest", f.Manifest}, &bytes.Buffer{}); err == nil {
		t.Fatal("wrong manifest version allowed")
	}
	testBatchOutputsAbsent(t, f)
}
