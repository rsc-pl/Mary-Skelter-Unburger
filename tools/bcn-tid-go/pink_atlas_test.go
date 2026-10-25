package main

import "testing"

func TestExactOwnerApprovedAllowlist(t *testing.T) {
	for _, v := range []struct {
		id   string
		want string
	}{
		{pink24ID, "b860758e8c0f8523a5b833e98fa2d115363a2d1fa9012f3409a16eeb82375559"},
		{pinkMiddleID, "57c142e188550d9261854c2e31d2e9b4961bbb8e3d1fbf13214eee49ff0434b6"},
		{pinkSkillID, "c133a411c876d7688d40b95193788fe8ecf59f1b5d4db98c4c304397397b737f"},
	} {
		got, ok := pinkApprovalFor(v.id)
		if !ok || got.target != v.want {
			t.Fatal("owner approved target changed", v.id)
		}
	}
	if _, ok := pinkApprovalFor("MS2_AFFECTION_CHARACTER_NAMES"); ok {
		t.Fatal("reviewed tolerance leaked to other family")
	}
}
