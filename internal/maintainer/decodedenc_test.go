package maintainer

import (
	"bytes"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func TestDecodedENCRecordAndSlotGuard(t *testing.T) {
	decoded := make([]byte, 32)
	copy(decoded[4:], []byte("Hello"))
	spec := decodedENCSpec{
		Locator:  decodedENCLocator{Dataset: "MS2_PC_LOCALIZED", Archive: 52, GDAT: 1, Row: 0, Offset: 4},
		Capacity: 7,
		Target: decodedENCTarget{
			WorkingID: "fixture", Kind: "string", Localized: "Hello",
			RecordKey: "MS2_PC_LOCALIZED/a052/g00001/top/row0000/off00000004/f0002", Field: 2,
		},
	}
	ef := mskit.ENCFile{Count: 1, Rows: []mskit.ENCRow{{Index: 0, SizeField: 0x80000010, Aux: uint32(len(decoded))}}}
	rec, err := decodedENCRecordWithRow(spec, "GxArchivedFile052.dat", strings.Repeat("a", 64), ef, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if rec["record_key"] != spec.Target.RecordKey || rec["language"] != "en" {
		t.Fatalf("record identity drift: %#v", rec)
	}
	chain := rec["format_chain"].([]string)
	if len(chain) != 4 || chain[3] != "LZO1X(decoded row)" {
		t.Fatalf("format chain=%v", chain)
	}
	line, err := canonicalJSONLine(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(line, []byte(`"archive_number": 52, "cl3_inner_index": null`)) {
		t.Fatalf("canonical JSON does not use Python-compatible separators: %s", line)
	}

	bad := append([]byte(nil), decoded...)
	bad[10] = 1 // inside the pinned NUL padding after "Hello\0"
	if _, err := decodedENCRecordWithRow(spec, "GxArchivedFile052.dat", strings.Repeat("a", 64), ef, bad); err == nil {
		t.Fatal("slot guard accepted nonzero bytes inside pinned capacity")
	}
}
