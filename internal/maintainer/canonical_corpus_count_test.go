package maintainer

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// Compare the immutable source-ledger values with the actual translated fields.
// The total number of edited fields is not interchangeable with the historical
// number of approved translation operations.
func TestCanonicalTranslationCountsMatchPublicCorpora(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "toolchain", "manifests", "canonical_counts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ledger struct {
		ExpectedChanged map[string]int `json:"expected_changed_fields_by_dataset"`
		ExpectedMapped  map[string]int `json:"expected_mapped_fields_by_dataset"`
		Corpora         map[string]struct {
			Records int `json:"records"`
			Fields  int `json:"fields"`
			Changed int `json:"changed_EN_fields"`
		} `json:"translation_corpora"`
	}
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(root, "translations", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	if len(paths) != len(ledger.Corpora) {
		t.Fatalf("source corpora count=%d ledger=%d", len(paths), len(ledger.Corpora))
	}
	changed, mapped := map[string]int{}, map[string]int{}
	for _, path := range paths {
		name := filepath.Base(path)
		wanted, ok := ledger.Corpora[name]
		if !ok {
			t.Fatalf("corpus missing from ledger: %s", name)
		}
		in, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(in)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		records, fields, edits := 0, 0, 0
		for sc.Scan() {
			if len(sc.Bytes()) == 0 {
				continue
			}
			var obj struct {
				Fields []struct {
					EN        string `json:"EN"`
					Localized string `json:"LOCALIZED"`
					Dataset   string `json:"target_dataset"`
				} `json:"fields"`
			}
			if err := json.Unmarshal(sc.Bytes(), &obj); err != nil {
				in.Close()
				t.Fatal(err)
			}
			records++
			for _, f := range obj.Fields {
				if _, ok := ledger.ExpectedMapped[f.Dataset]; !ok {
					in.Close()
					t.Fatalf("unknown target dataset %s", f.Dataset)
				}
				fields++
				mapped[f.Dataset]++
				if f.EN != f.Localized {
					edits++
					changed[f.Dataset]++
				}
			}
		}
		err = sc.Err()
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
		if records != wanted.Records || fields != wanted.Fields || edits != wanted.Changed {
			t.Fatalf("corpus %s: records %d/%d, fields %d/%d, edits %d/%d", name, records, wanted.Records, fields, wanted.Fields, edits, wanted.Changed)
		}
	}
	for ds, want := range ledger.ExpectedMapped {
		if mapped[ds] != want {
			t.Errorf("mapped %s: %d != %d", ds, mapped[ds], want)
		}
	}
	for ds, want := range ledger.ExpectedChanged {
		if changed[ds] != want {
			t.Errorf("changed %s: %d != %d", ds, changed[ds], want)
		}
	}
}
