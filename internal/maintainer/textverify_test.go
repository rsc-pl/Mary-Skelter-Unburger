package maintainer

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func verifyTestRecord(role string, blob, row, off int, text, key string) textRecord {
	meta := map[string]any{"blob_type": "gnl"}
	if role == "gnl_str" {
		meta["blob_offset"] = blob
	}
	if role == "enc_string" {
		meta = map[string]any{"blob_type": "enc_row", "row": row, "str_offset": off}
	}
	return textRecord{
		"record_key": key, "archive_number": json.Number("52"), "gdat_index": json.Number("1"),
		"cl3_inner_index": nil, "field_role": role, "record_index": json.Number("0"),
		"field_index": json.Number("0"), "container_key": "top", "repack_metadata": meta,
		"text": text, "encoding": "utf-8", "raw_hex": hex.EncodeToString([]byte(text)),
	}
}

func TestLogicalIndexSurvivesPhysicalRelayout(t *testing.T) {
	src := []textRecord{
		verifyTestRecord("gnl_str", 0x100, 0, 0, "a", "src1"),
		verifyTestRecord("gnl_str", 0x200, 0, 0, "b", "src2"),
	}
	rebuilt := []textRecord{
		verifyTestRecord("gnl_str", 0x100, 0, 0, "a", "new1"),
		verifyTestRecord("gnl_str", 0x248, 0, 0, "b", "new2"),
	}
	a, err := logicalIndex(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := logicalIndex(rebuilt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Collisions) != 0 || len(b.Collisions) != 0 || len(a.Rows) != len(b.Rows) {
		t.Fatalf("unexpected logical index sizes/collisions: %#v %#v", a, b)
	}
	for k := range a.Rows {
		if _, ok := b.Rows[k]; !ok {
			t.Fatalf("rebuilt missing logical key %s", k)
		}
	}
}

func TestLogicalIndexENCUsesOrderNotLegacyRecordKey(t *testing.T) {
	a1 := verifyTestRecord("enc_string", 0, 0, 175, "one", "same/f000")
	a2 := verifyTestRecord("enc_string", 0, 0, 320, "two", "same/f000")
	b1 := verifyTestRecord("enc_string", 0, 0, 175, "one", "new/f000")
	b2 := verifyTestRecord("enc_string", 0, 0, 325, "two", "new/f001")
	a, err := logicalIndex([]textRecord{a1, a2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := logicalIndex([]textRecord{b1, b2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Rows) != 2 || len(b.Rows) != 2 || len(a.Collisions) != 0 || len(b.Collisions) != 0 {
		t.Fatal("ENC logical identity collapsed")
	}
	for k := range a.Rows {
		if _, ok := b.Rows[k]; !ok {
			t.Fatalf("rebuilt missing ENC logical key %s", k)
		}
	}
}

func buildVerifyENCEntry(t *testing.T, row []byte) []byte {
	t.Helper()
	const rowOff = 64
	payload := make([]byte, rowOff+len(row))
	binary.LittleEndian.PutUint32(payload[0:4], 1)
	binary.LittleEndian.PutUint32(payload[4:8], uint32(len(row)))
	binary.LittleEndian.PutUint32(payload[8:12], 0)
	binary.LittleEndian.PutUint32(payload[12:16], rowOff)
	copy(payload[rowOff:], row)
	var c bytes.Buffer
	zw, err := zlib.NewWriterLevel(&c, 7)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	entry := make([]byte, 0x90+c.Len())
	entry[0], entry[1], entry[2], entry[3] = 0, 'c', 'n', 'e'
	copy(entry[0x80:0x84], []byte("BILZ"))
	binary.BigEndian.PutUint32(entry[0x84:0x88], uint32(len(payload)))
	binary.BigEndian.PutUint32(entry[0x88:0x8c], uint32(c.Len()))
	binary.BigEndian.PutUint32(entry[0x8c:0x90], 7)
	copy(entry[0x90:], c.Bytes())
	return entry
}

func TestVerifyTextDatasetGoENCUsesDecodedRowNotScannerCardinality(t *testing.T) {
	root := t.TempDir()
	source, rebuilt := filepath.Join(root, "source"), filepath.Join(root, "rebuilt")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rebuilt, 0o755); err != nil {
		t.Fatal(err)
	}
	const off, capacity = 8, 16
	old := []byte("愛")
	newText := "Love Style"
	newb := []byte(newText)
	row := make([]byte, 72)
	copy(row[:8], []byte{1, 2, 3, 4, 0, 0, 0, 0})
	copy(row[off:], old)
	srcEntry := buildVerifyENCEntry(t, row)
	newRow := append([]byte(nil), row...)
	copy(newRow[off:], newb)
	for i := off + len(newb); i <= off+capacity; i++ {
		newRow[i] = 0
	}
	rebEntry := buildVerifyENCEntry(t, newRow)
	srcGDAT, err := mskit.BuildGDAT([][]byte{srcEntry})
	if err != nil {
		t.Fatal(err)
	}
	rebGDAT, err := mskit.BuildGDAT([][]byte{rebEntry})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "GxArchivedFile052.dat"), srcGDAT, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rebuilt, "GxArchivedFile052.dat"), rebGDAT, 0o644); err != nil {
		t.Fatal(err)
	}

	rec := map[string]any{
		"record_key":     "MS2_PC_LOCALIZED/a052/g00000/top/row0000/off00000008/f0000",
		"archive_number": 52, "gdat_index": 0, "cl3_inner_index": nil,
		"field_role": "enc_string", "record_index": 0, "field_index": 0,
		"container_key": "top", "encoding": "utf-8", "text": newText,
		"raw_hex":         hex.EncodeToString(old),
		"repack_metadata": map[string]any{"blob_type": "enc_row", "row": 0, "str_offset": off, "str_len": len(old), "slot_capacity": capacity},
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(root, "expected.jsonl")
	if err := os.WriteFile(expected, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "report.json")
	rep, err := VerifyTextDatasetGo("MS2_PC_LOCALIZED", rebuilt, expected, source, []int{52}, report)
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := rep["ok"].(bool)
	if !ok {
		t.Fatalf("verify candidate failed: %#v", rep)
	}
	audit := rep["enc_audit"].(map[string]any)
	if audit["edits_checked"] != 1 {
		t.Fatalf("edits_checked=%v", audit["edits_checked"])
	}
	srcStats := rep["source_extract_stats"].(map[string]any)
	gotStats := rep["extract_stats"].(map[string]any)
	if srcStats["by_role"].(map[string]int)["enc_string"] != 0 {
		t.Fatalf("source scanner unexpectedly saw short JP: %#v", srcStats)
	}
	if gotStats["by_role"].(map[string]int)["enc_string"] != 1 {
		t.Fatalf("rebuilt scanner did not see English replacement: %#v", gotStats)
	}

	bad := map[string]any{}
	for k, v := range rec {
		bad[k] = v
	}
	bad["text"] = "Wrong Style"
	badLine, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	badExpected := filepath.Join(root, "bad-expected.jsonl")
	if err := os.WriteFile(badExpected, append(badLine, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	badReport := filepath.Join(root, "bad-report.json")
	badRep, err := VerifyTextDatasetGoStrict("MS2_PC_LOCALIZED", rebuilt, badExpected, source, []int{52}, badReport)
	if err == nil {
		t.Fatal("strict verification accepted a mismatching report")
	}
	if ok, _ := badRep["ok"].(bool); ok {
		t.Fatalf("strict mismatch report unexpectedly ok: %#v", badRep)
	}
	if _, err := os.Stat(badReport); err != nil {
		t.Fatalf("strict verification did not preserve mismatch report: %v", err)
	}
	// Both successful and failing verification must leave no temporary records
	// inside the dataset output or the old read-only master view.
	for _, parent := range []string{rebuilt, root} {
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "_go_verify_") || strings.HasPrefix(entry.Name(), ".go-verify-") || strings.HasPrefix(entry.Name(), ".text-verify-") {
				t.Fatalf("verifier leaked scratch %s into %s", entry.Name(), parent)
			}
		}
	}
}
