package maintainer

import (
	"fmt"
	"strings"
	"testing"
)

const pcPlanSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func pcPlanRow(dataset string, archive, gdat int, path string, meta string, oldHex, value string) string {
	return fmt.Sprintf(`{"record_key":%q,"source_dataset":%q,"source_file":%q,"source_file_sha256":%q,"archive_number":%d,"gdat_index":%d,"cl3_inner_index":null,"record_index":1,"field_index":2,"encoding":"utf-8","raw_hex":%q,"text":%q,"format_chain":["GDAT","BILZ","GBIN"],"repack_metadata":%s}`, fmt.Sprintf("%s/a%03d/g%05d/%s", dataset, archive, gdat, path), dataset, fmt.Sprintf("GxArchivedFile%03d.dat", archive), pcPlanSHA, archive, gdat, oldHex, value, meta)
}

func TestPCTextPlanNoChangeAndThreeKinds(t *testing.T) {
	ds := "MS2_PC_LOCALIZED"
	rows := []string{
		pcPlanRow(ds, 51, 0, "top/blob0/r00001/f002", `{"blob_type":"gnl","blob_offset":null}`, "6f6c64", "new"),
		pcPlanRow(ds, 52, 1, "top/blob0/r00002/f002", `{"blob_type":"enc_row","row":3,"str_offset":128,"slot_capacity":24}`, "6f6c64", "New"),
		pcPlanRow(ds, 55, 2, "top/blob0/r00003/f002", `{"blob_type":"bin_string","string_index":7}`, "6f6c64", "new"),
		pcPlanRow(ds, 55, 2, "top/blob0/r00004/f002", `{"blob_type":"bin_string","string_index":8}`, "6f6c64", "old"),
	}
	plan, err := PlanPCTextRecords(strings.NewReader(strings.Join(rows, "\n")), ds)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalRecords != 4 || plan.ChangedRecords != 3 || len(plan.Archives) != 3 {
		t.Fatalf("wrong census %+v", plan)
	}
	if plan.Edits[0].Type != "gnl" || plan.Edits[0].CL3 != -1 || plan.Edits[0].Blob != -1 || plan.Edits[1].Type != "enc_row" || plan.Edits[1].Capacity != 24 || plan.Edits[2].StringIndex != 7 {
		t.Fatalf("wrong grouping %+v", plan.Edits)
	}
}

func TestPCTextPlanFailsClosed(t *testing.T) {
	ds := "MS2_PC_LOCALIZED"
	orig := pcPlanRow(ds, 51, 0, "top/blob0/r00001/f002", `{"blob_type":"gnl","blob_offset":null}`, "6f6c64", "new")
	cases := map[string]string{
		"bad dataset":            strings.Replace(orig, `"source_dataset":"MS2_PC_LOCALIZED"`, `"source_dataset":"MSF_PC_SYSTEM"`, 1),
		"unsupported cp932":      strings.Replace(orig, `"encoding":"utf-8"`, `"encoding":"cp932"`, 1),
		"wrong source sha":       strings.Replace(orig, pcPlanSHA, "000", 1),
		"wrong archive filename": strings.Replace(orig, "GxArchivedFile051.dat", "GxArchivedFile052.dat", 1),
		"unsupported blob":       strings.Replace(orig, `"blob_type":"gnl"`, `"blob_type":"stcm_string"`, 1),
		"bad rawhex":             strings.Replace(orig, `"raw_hex":"6f6c64"`, `"raw_hex":"bogus"`, 1),
		"bad key":                strings.Replace(orig, "/a051/", "/a052/", 1),
		"duplicate":              orig + "\n" + orig,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := PlanPCTextRecords(strings.NewReader(input), ds); err == nil {
				t.Fatal("invalid source accepted")
			}
		})
	}
}

func TestPCTextPlanChangedPhysicalCollision(t *testing.T) {
	ds := "MS2_PC_LOCALIZED"
	a := pcPlanRow(ds, 51, 0, "top/blob0/r00001/f002", `{"blob_type":"gnl","blob_offset":null}`, "6f6c64", "new")
	b := pcPlanRow(ds, 51, 0, "other/blob0/r00001/f002", `{"blob_type":"gnl","blob_offset":null}`, "6f6c64", "fresh")
	if _, err := PlanPCTextRecords(strings.NewReader(a+"\n"+b), ds); err == nil {
		t.Fatal("duplicate physical text coordinate accepted")
	}
}
