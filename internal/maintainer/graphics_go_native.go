package maintainer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type NativeGraphicsPatchReport struct {
	SourceSHA256    string `json:"source_sha256"`
	ResultSHA256    string `json:"result_sha256"`
	SourceTIDSHA256 string `json:"source_tid_sha256"`
	ResultTIDSHA256 string `json:"result_tid_sha256"`
	GDATIndex       int    `json:"gdat_index"`
	CL3Index        int    `json:"cl3_index"`
	BILZ            bool   `json:"bilz"`
	BILZAlgorithm   int    `json:"bilz_algorithm"`
	Compression     string `json:"compression"`
	Status          string `json:"status"`
}

// PatchGraphicsTIDGoFile uses no Python, never overwrites an original or an
// existing result, and requires SHA-256 of the pristine source archive.
func PatchGraphicsTIDGoFile(sourcePath, resultPath, expectedSHA string, gdatIndex int, member string, replacement []byte) (NativeGraphicsPatchReport, error) {
	var r NativeGraphicsPatchReport
	if len(expectedSHA) != 64 {
		return r, fmt.Errorf("source SHA-256 must be exactly 64 hex digits")
	}
	expected, err := hex.DecodeString(strings.ToLower(expectedSHA))
	if err != nil || len(expected) != sha256.Size {
		return r, fmt.Errorf("invalid source SHA-256")
	}
	src, err := filepath.Abs(sourcePath)
	if err != nil {
		return r, err
	}
	dst, err := filepath.Abs(resultPath)
	if err != nil {
		return r, err
	}
	if src == dst || member == "" || gdatIndex < 0 {
		return r, fmt.Errorf("invalid target, member or GDAT index")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return r, err
	}
	sum := sha256.Sum256(raw)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), expectedSHA) {
		return r, fmt.Errorf("pristine source SHA-256 mismatch; refusing to patch")
	}
	before, _, err := mskit.ExtractGraphicsTID(raw, gdatIndex, member)
	if err != nil {
		return r, err
	}
	result, loc, err := mskit.PatchGraphicsTIDGoSafe(raw, gdatIndex, member, replacement)
	if err != nil {
		return r, err
	}
	if err = publishGraphicsGDAT(src, dst, result); err != nil {
		return r, err
	}
	newHash := sha256.Sum256(result)
	oldTIDHash := sha256.Sum256(before)
	newTIDHash := sha256.Sum256(replacement)
	compression := "plain"
	if loc.BILZ {
		compression = "BILZ stored"
		if loc.BILZAlgo == mskit.BILZAlgoZlib {
			compression = "BILZ Go zlib"
		}
	}
	r = NativeGraphicsPatchReport{hex.EncodeToString(sum[:]), hex.EncodeToString(newHash[:]), hex.EncodeToString(oldTIDHash[:]), hex.EncodeToString(newTIDHash[:]), gdatIndex, loc.CL3Index, loc.BILZ, loc.BILZAlgo, compression, "PASS"}
	_ = expected // hex validity verified above
	return r, nil
}
