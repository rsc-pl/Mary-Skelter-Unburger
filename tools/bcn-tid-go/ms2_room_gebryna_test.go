package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRoomFailClosed(t *testing.T) {
	for _, id := range []string{roomListID, myRoomID} {
		a := roomAsset{}
		a.ID = id
		if _, err := renderRoom([]byte("forged original"), nil, a, roomPreset{}); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
			t.Fatalf("%s accepted untrusted TID: %v", id, err)
		}
		b, _ := json.Marshal(familyBatchSpec{Version: 1, ProjectRoot: "/x", Jobs: []familyBatchJob{{Source: "a", Output: "b", SourceSHA256: strings.Repeat("a", 64), Quality: 6, MaxAlpha: 64, MaxVisible: 64, Families: []string{id}}}})
		if _, err := parseFamilyBatch(b); err != nil {
			t.Fatalf("%s excluded: %v", id, err)
		}
	}
	b, _ := json.Marshal(familyBatchSpec{Version: 1, ProjectRoot: "/x", Jobs: []familyBatchJob{{Source: "a", Output: "b", SourceSHA256: strings.Repeat("a", 64), Quality: 6, MaxAlpha: 64, MaxVisible: 64, Families: []string{roomListID, myRoomID}}}})
	if _, err := parseFamilyBatch(b); err != nil {
		t.Fatal(err)
	}
}
