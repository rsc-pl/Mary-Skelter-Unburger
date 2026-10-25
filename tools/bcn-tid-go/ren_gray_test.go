package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenGrayFailsClosedWithoutOriginalSource(t *testing.T) {
	if _, e := renderRenGray([]byte("forged original"), nil, familyAsset{}, renGrayPreset{}); e == nil || !strings.Contains(e.Error(), "SHA mismatch") {
		t.Fatalf("nonoriginal accepted: %v", e)
	}
	b, _ := json.Marshal(familyBatchSpec{Version: 1, ProjectRoot: "/x", Jobs: []familyBatchJob{{Source: "src", Output: "dst", SourceSHA256: strings.Repeat("0", 64), Families: []string{renGrayID}, Quality: 6, MaxAlpha: 64, MaxVisible: 64}}})
	if _, e := parseFamilyBatch(b); e != nil {
		t.Fatalf("allowlist rejects SHA-pinned family: %v", e)
	}
}
