package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFinaleCampRejectsUntrustedSource(t *testing.T) {
	if _, e := renderFinaleCamp([]byte("not original TID"), nil, familyAsset{}, finaleCampPreset{}); e == nil || !strings.Contains(e.Error(), "SHA mismatch") {
		t.Fatalf("untrusted TID accepted: %v", e)
	}
}
func TestFamilyBatchAuthorizesOnlySourceVerified(t *testing.T) {
	b, _ := json.Marshal(familyBatchSpec{Version: 1, ProjectRoot: "/test", Jobs: []familyBatchJob{{Source: "source", Output: "out", SourceSHA256: strings.Repeat("0", 64), Families: []string{finaleCampID}, Quality: 6, MaxAlpha: 64, MaxVisible: 64}}})
	if _, e := parseFamilyBatch(b); e != nil {
		t.Fatal(e)
	}
}
