package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Public synthetic tests intentionally have no private font/TID dependencies.
func TestPinkRoomRejectsUntrustedTID(t *testing.T) {
	if _, e := renderRoomPink([]byte("NOT-AN-ORIGINAL-TID"), nil, familyAsset{}, roomPinkPreset{}); e == nil || !strings.Contains(e.Error(), "SHA mismatch") {
		t.Fatalf("untrusted source accepted: %v", e)
	}
}
func TestFamilyBatchAuthorization(t *testing.T) {
	raw, _ := json.Marshal(familyBatchSpec{Version: 1, ProjectRoot: "/example", Jobs: []familyBatchJob{{Source: "a", Output: "b", SourceSHA256: strings.Repeat("a", 64), Families: []string{roomPinkID}, Quality: 6, MaxAlpha: 64, MaxVisible: 64}}})
	if _, e := parseFamilyBatch(raw); e != nil {
		t.Fatal("eighth proven source family rejected", e)
	}
}
