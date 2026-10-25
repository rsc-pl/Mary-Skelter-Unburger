package maintainer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func makeNativeDataset(t *testing.T) (string, string, string, string, string, [][]byte) {
	t.Helper()
	src, plan := Fixture(t)
	_ = plan
	root := t.TempDir()
	master := filepath.Join(root, "source")
	_ = os.Mkdir(master, 0700)
	names := []string{"GxArchivedFile052.dat", "GxArchivedFile053.dat"}
	contents := [][]byte{src, src}
	for i, name := range names {
		if e := os.WriteFile(filepath.Join(master, name), contents[i], 0600); e != nil {
			t.Fatal(e)
		}
	}
	records := filepath.Join(root, "records.jsonl")
	var rows []string
	for i, n := range []int{52, 53} {
		row := map[string]any{
			"record_key":     fmt.Sprintf("MS2_PC_LOCALIZED/a%03d/g00000/c0/r00000/f0", n),
			"source_dataset": "MS2_PC_LOCALIZED", "source_file": names[i], "source_file_sha256": mergeSHA(contents[i]),
			"archive_number": n, "gdat_index": 0, "cl3_inner_index": nil, "record_index": 0, "field_index": 0,
			"encoding": "utf-8", "raw_hex": "536e6f772057686974652e2e2e", "text": fmt.Sprintf("Shirayuki-%d", n),
			"format_chain": []string{"GDAT", "GBNL"}, "repack_metadata": map[string]any{"blob_type": "gnl", "blob_offset": nil},
		}
		b, e := json.Marshal(row)
		if e != nil {
			t.Fatal(e)
		}
		rows = append(rows, string(b))
	}
	if e := os.WriteFile(records, []byte(strings.Join(rows, "\n")+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	pins := filepath.Join(root, "pins.tsv")
	pinData := "dataset\tfile\tbytes\tsha256\n"
	for i, name := range names {
		pinData += fmt.Sprintf("MS2_PC_LOCALIZED\t%s\t%d\t%s\n", name, len(contents[i]), mergeSHA(contents[i]))
	}
	if e := os.WriteFile(pins, []byte(pinData), 0600); e != nil {
		t.Fatal(e)
	}
	return root, master, records, pins, filepath.Join(root, "out"), contents
}

func TestNativeDatasetTwoArchiveTransactional(t *testing.T) {
	root, master, records, pins, out, srcs := makeNativeDataset(t)
	report := filepath.Join(root, "report.json")
	rep, err := RebuildPCTextDatasetNative("MS2_PC_LOCALIZED", master, records, out, report, pins, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Archives) != 2 || rep.TotalEditedRecords != 2 || rep.TotalEditedEntries != 2 {
		t.Fatalf("unexpected report %+v", rep)
	}
	for i, n := range []int{52, 53} {
		name := fmt.Sprintf("GxArchivedFile%03d.dat", n)
		got, e := os.ReadFile(filepath.Join(out, "masterWindows", name))
		if e != nil {
			t.Fatal(e)
		}
		dat, e := mskit.ParseGDAT(got)
		if e != nil {
			t.Fatal(e)
		}
		g, e := mskit.ParseGBNL(dat.Entries[0])
		if e != nil {
			t.Fatal(e)
		}
		f, e := g.Field(0, 0)
		if e != nil || string(f.StrRaw) != fmt.Sprintf("Shirayuki-%d", n) {
			t.Fatalf("bad native value %s %v", f.StrRaw, e)
		}
		old, _ := mskit.ParseGDAT(srcs[i])
		if !bytes.Equal(dat.Entries[1], old.Entries[1]) {
			t.Fatal("unrelated GDAT mutated")
		}
		onDisk, _ := os.ReadFile(filepath.Join(master, name))
		if !bytes.Equal(onDisk, srcs[i]) {
			t.Fatal("owner source mutated")
		}
	}
	if _, err = RebuildPCTextDatasetNative("MS2_PC_LOCALIZED", master, records, out, report, pins, false); err == nil {
		t.Fatal("overwriting existing output was accepted")
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var fromFile PCNativeDatasetReport
	if err = json.Unmarshal(b, &fromFile); err != nil || fromFile.TotalEditedRecords != 2 {
		t.Fatalf("report parse %v", err)
	}
	if a, e := nonIdenticalRebuildArchives(report); e != nil || len(a) != 2 {
		t.Fatalf("bundle report incompatible %v %v", a, e)
	}
}

func TestNativeDatasetFailClosedNoPartial(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(t *testing.T, root, master, records, pins, out, report string)
	}{
		{"missing second source", func(t *testing.T, _, master, _, _, _, _ string) {
			t.Helper()
			os.Remove(filepath.Join(master, "GxArchivedFile053.dat"))
		}},
		{"stale source hash", func(t *testing.T, _, master, _, _, _, _ string) {
			t.Helper()
			p := filepath.Join(master, "GxArchivedFile053.dat")
			f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
			f.Write([]byte("x"))
			f.Close()
		}},
		{"manifest mismatch", func(t *testing.T, _, _, _, pins, _, _ string) {
			t.Helper()
			b, _ := os.ReadFile(pins)
			os.WriteFile(pins, []byte(strings.Replace(string(b), "MS2_PC_LOCALIZED\tGxArchivedFile053.dat", "OTHER\tGxArchivedFile053.dat", 1)), 0600)
		}},
		{"source path symlink", func(t *testing.T, _, master, _, _, _, _ string) {
			t.Helper()
			p := filepath.Join(master, "GxArchivedFile053.dat")
			os.Rename(p, p+".real")
			os.Symlink(p+".real", p)
		}},
		{"pre-existing report", func(t *testing.T, _, _, _, _, _, rep string) { t.Helper(); os.WriteFile(rep, []byte("owner"), 0600) }},
		{"pre-existing output", func(t *testing.T, _, _, _, _, out, _ string) { t.Helper(); os.Mkdir(out, 0700) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, master, records, pins, out, _ := makeNativeDataset(t)
			report := filepath.Join(root, "report.json")
			tc.alter(t, root, master, records, pins, out, report)
			_, err := RebuildPCTextDatasetNative("MS2_PC_LOCALIZED", master, records, out, report, pins, true)
			if err == nil {
				t.Fatal("invalid source/output accepted")
			}
			if tc.name != "pre-existing output" {
				if _, e := os.Lstat(out); !os.IsNotExist(e) {
					t.Fatal("partially published output")
				}
			}
			if tc.name == "pre-existing report" {
				b, _ := os.ReadFile(report)
				if string(b) != "owner" {
					t.Fatal("overwrote user report")
				}
			}
		})
	}
}
