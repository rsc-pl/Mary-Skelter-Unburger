package maintainer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"maryskelter-unburger/internal/mskit"
)

func Fixture(t *testing.T) ([]byte, PCTextPlan) {
	t.Helper()
	raw, err := hex.DecodeString("4753544c01000000100000000400000001000000400000000200000004000000010000004800000000000000500000000800000000000000000000000000000000000000000000000500000000000000536e6f772057686974652e2e2e00")
	if err != nil {
		t.Fatal(err)
	}
	original, err := mskit.BuildGDAT([][]byte{raw, []byte("UNTCHANGED-BINARY-ENTRY")})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	edit := PCTextEdit{RecordKey: "MS2_PC_LOCALIZED/a052/g00000/c0/r00000/f0", SourceFile: "GxArchivedFile052.dat",
		SourceSHA256: fmt.Sprintf("%x", sum), Archive: 52, GDAT: 0, Type: "gnl", CL3: -1, Blob: -1,
		Message: 0, Field: 0, Row: -1, Offset: -1, StringIndex: -1, Old: []byte("Snow White..."), New: []byte("Shirayuki...")}
	return original, PCTextPlan{Dataset: "MS2_PC_LOCALIZED", TotalRecords: 2, ChangedRecords: 1, Archives: []int{52}, Edits: []PCTextEdit{edit}}
}

func TestPCTextNativeGDATGBNLRebuildAndUnchanged(t *testing.T) {
	src, plan := Fixture(t)
	got, rep, err := RebuildPCTextArchiveNative(src, plan.Dataset, 52, plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.TotalEntries != 2 || rep.ChangedFields != 1 || len(rep.ChangedEntries) != 1 || rep.ChangedEntries[0] != 0 {
		t.Fatalf("wrong report %+v", rep)
	}
	orig, _ := mskit.ParseGDAT(src)
	actual, err := mskit.ParseGDAT(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(orig.Entries[1], actual.Entries[1]) {
		t.Fatal("untouched entry changed")
	}
	g, err := mskit.ParseGBNL(actual.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	f0, _ := g.Field(0, 0)
	f1, _ := g.Field(1, 0)
	if string(f0.StrRaw) != "Shirayuki..." || string(f1.StrRaw) != "Snow White..." {
		t.Fatalf("string aliasing %q / %q", f0.StrRaw, f1.StrRaw)
	}
	if !bytes.Equal(src, orig.Raw) {
		t.Fatal("source unexpectedly mutated")
	}
	if rep.OutputSHA256 == rep.SourceSHA256 {
		t.Fatal("reported unchanged archive")
	}
	same, sameReport, err := RebuildPCTextArchiveNative(src, plan.Dataset, 52, PCTextPlan{Dataset: plan.Dataset}, nil)
	if err != nil || !bytes.Equal(src, same) || sameReport.OutputSHA256 != sameReport.SourceSHA256 {
		t.Fatal("no-op not byte exact", err)
	}
}
func TestPCTextNativeGuardsAndNoWrites(t *testing.T) {
	src, _ := Fixture(t)
	cases := []struct {
		name   string
		change func(*PCTextPlan)
	}{
		{"hash", func(p *PCTextPlan) { p.Edits[0].SourceSHA256 = strings.Repeat("0", 64) }},
		{"filename", func(p *PCTextPlan) { p.Edits[0].SourceFile = "GxArchivedFile053.dat" }},
		{"old text", func(p *PCTextPlan) { p.Edits[0].Old = []byte("wrong") }},
		{"unsupported", func(p *PCTextPlan) { p.Edits[0].Type = "stcm_string" }},
		{"route", func(p *PCTextPlan) { p.Edits[0].Blob = 42 }},
		{"dataset", func(p *PCTextPlan) { p.Dataset = "MSF_PC_SYSTEM" }},
		{"wrong index", func(p *PCTextPlan) { p.Edits[0].GDAT = 1000 }},
		{"non UTF8", func(p *PCTextPlan) { p.Edits[0].New = []byte{0xff} }},
		{"nul", func(p *PCTextPlan) { p.Edits[0].New = []byte("x\x00y") }},
		{"duplicate", func(p *PCTextPlan) { p.Edits = append(p.Edits, p.Edits[0]) }},
		{"mixed", func(p *PCTextPlan) {
			x := p.Edits[0]
			x.Type = "bin_string"
			x.StringIndex = 1
			p.Edits = append(p.Edits, x)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, p := Fixture(t)
			tc.change(&p)
			out, _, err := RebuildPCTextArchiveNative(src, "MS2_PC_LOCALIZED", 52, p, nil)
			if err == nil || out != nil {
				t.Fatalf("unsafe input accepted: %v", err)
			}
		})
	}
	_, p := Fixture(t)
	if _, _, err := RebuildPCTextArchiveNative([]byte("not GDAT"), p.Dataset, 52, p, nil); err == nil {
		t.Fatal("bad source accepted")
	}
}
