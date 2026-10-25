package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestPCMasterAuditCLIRequiresBothRootsAndEmitsSafeStatus(t *testing.T) {
	var output bytes.Buffer
	if err := runPCMasterAuditNative(".", nil, &output); err == nil {
		t.Fatal("missing source roots accepted")
	}
	if output.Len() != 0 {
		t.Fatal("unexpected output on invalid command")
	}
	if err := runPCMasterAuditNative(".", []string{"--ms2-master", "/tmp", "--finale-master", "/tmp", "extra"}, &output); err == nil {
		t.Fatal("extra arg accepted")
	}
	// The synthetic source paths will be missing, but results must be emitted
	// with a nonzero error and must NOT include the private path supplied.
	if err := runPCMasterAuditNative("../..", []string{"--ms2-master", "/a-private-directory", "--finale-master", "/another-private-directory"}, &output); err == nil {
		t.Fatal("absent masters accepted")
	}
	if !strings.Contains(output.String(), `"expected_archives": 10`) || strings.Contains(output.String(), "a-private-directory") || strings.Contains(output.String(), "another-private-directory") {
		t.Fatal("unsafe/invalid audit report")
	}
}
