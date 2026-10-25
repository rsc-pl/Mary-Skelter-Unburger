package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maryskelter-unburger/internal/mskit"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func makeNativeTextCLIInput(t *testing.T) (string, string, string, []byte) {
	t.Helper()
	root := privateFixturePath(t, "text-codec-golden-vectors")
	g, err := os.ReadFile(filepath.Join(root, "gbnl_one_sharer.input.bin"))
	if err != nil {
		t.Fatal(err)
	}
	gd, err := mskit.BuildGDAT([][]byte{g, []byte("UNMODIFIED")})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "GxArchivedFile052.dat")
	rec := filepath.Join(dir, "records.jsonl")
	out := filepath.Join(dir, "rebuilt.dat")
	if err := os.WriteFile(src, gd, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(gd)
	record := map[string]any{
		"record_key":     "MS2_PC_LOCALIZED/a052/g00000/direct/r00000/f0",
		"source_dataset": "MS2_PC_LOCALIZED", "source_file": "GxArchivedFile052.dat",
		"source_file_sha256": hex.EncodeToString(sum[:]), "archive_number": 52,
		"gdat_index": 0, "record_index": 0, "field_index": 0, "encoding": "utf-8",
		"text": "Shirayuki...", "raw_hex": hex.EncodeToString([]byte("Snow White...")),
		"format_chain":    []string{"GBNL"},
		"repack_metadata": map[string]any{"blob_type": "gnl"},
	}
	blob, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rec, append(blob, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return src, rec, out, gd
}
func TestGoTextCLIIndependentGBNLAndNoOverwrite(t *testing.T) {
	src, rec, out, baseline := makeNativeTextCLIInput(t)
	args := []string{"--dataset", "MS2_PC_LOCALIZED", "--archive", "52", "--source", src, "--records", rec, "--out", out}
	var stdout bytes.Buffer
	if err := executeNativePCTextRebuild(args, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"status":"written-unapproved-for-game"`) {
		t.Fatalf("misleading status %s", stdout.String())
	}
	result, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	orig, err := os.ReadFile(src)
	if err != nil || !bytes.Equal(orig, baseline) {
		t.Fatal("source changed", err)
	}
	gd, err := mskit.ParseGDAT(result)
	if err != nil {
		t.Fatal(err)
	}
	org, _ := mskit.ParseGDAT(orig)
	if !bytes.Equal(gd.Entries[1], org.Entries[1]) {
		t.Fatal("unmodified GDAT entry changed")
	}
	expected, err := os.ReadFile(filepath.Join(privateFixturePath(t, "text-codec-golden-vectors"), "gbnl_one_sharer.output.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gd.Entries[0], expected) {
		t.Fatal("rebuilt GBNL differs from frozen legacy golden")
	}
	if err := executeNativePCTextRebuild(args, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "existing output") {
		t.Fatalf("overwrite allowed %v", err)
	}
}
func TestGoTextCLIFailClosedNoFileCreated(t *testing.T) {
	src, rec, out, _ := makeNativeTextCLIInput(t)
	tests := []struct {
		name string
		args []string
	}{
		{"inplace", []string{"--dataset", "MS2_PC_LOCALIZED", "--archive", "52", "--source", src, "--records", rec, "--out", src}},
		{"wrong archive", []string{"--dataset", "MS2_PC_LOCALIZED", "--archive", "53", "--source", src, "--records", rec, "--out", out}},
		{"missing source", []string{"--dataset", "MS2_PC_LOCALIZED", "--archive", "52", "--source", src + "bad", "--records", rec, "--out", out}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := executeNativePCTextRebuild(tc.args, &bytes.Buffer{}); err == nil {
				t.Fatal("unsafe write accepted")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatalf("output unexpectedly exists: %v", err)
			}
		})
	}
	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m["source_file_sha256"] = strings.Repeat("0", 64)
	wrong, _ := json.Marshal(m)
	if err := os.WriteFile(rec, append(wrong, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := executeNativePCTextRebuild([]string{"--dataset", "MS2_PC_LOCALIZED", "--archive", "52", "--source", src, "--records", rec, "--out", out}, &bytes.Buffer{}); err == nil {
		t.Fatal("stale source SHA accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("unsafe output created: %v", err)
	}
}
