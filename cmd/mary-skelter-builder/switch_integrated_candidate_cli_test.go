package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSwitchIntegratedCandidateRequiresExplicitBothGames(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	for _, args := range [][]string{
		nil,
		{"--ms2-master", "somewhere", "--finale-master", "somewhere"},
		{"--candidate", "--ms2-master", "somewhere"},
		{"--candidate", "--finale-master", "somewhere"},
		{"--candidate", "--ms2-master", "somewhere", "--finale-master", "somewhere", "unknown"},
	} {
		out.Reset()
		err := executeSwitchIntegratedCandidate(root, args, &out, &out)
		if err == nil {
			t.Fatalf("missing fail-closed validation for %v", args)
		}
		if out.Len() > 0 {
			t.Fatalf("published output on %v: %s", args, out.String())
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".local")); !os.IsNotExist(err) {
		t.Fatalf("preflight unexpectedly mutated candidate root: %v", err)
	}
}

func TestSwitchIntegratedCandidateDoesNotFallbackToGraphicsOnly(t *testing.T) {
	root := t.TempDir()
	var out bytes.Buffer
	err := executeSwitchIntegratedCandidate(root, []string{
		"--candidate", "--ms2-master", "missing-ms2", "--finale-master", "missing-finale",
	}, &out, &out)
	if err == nil || !strings.Contains(err.Error(), "BLOCKED") {
		t.Fatalf("missing integrated-stage failure: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("unverified candidate result: %s", out.String())
	}
}
