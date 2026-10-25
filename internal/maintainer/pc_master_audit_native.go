package maintainer

// Native, read-only audit of owner-supplied pristine PC DATs. Unlike
// EnsureSourceCache this does not copy masters or generate derived records.
// It checks the canonical translation+graphics archive union and whole-file
// SHA-256 in a bounded-memory stream. It is provenance only, not text parity.
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type PCMasterAuditItem struct {
	Game         string `json:"game"`
	File         string `json:"file"`
	Bytes        int64  `json:"expected_bytes"`
	SHA256       string `json:"expected_sha256"`
	Status       string `json:"status"`
	ActualBytes  int64  `json:"actual_bytes,omitempty"`
	ActualSHA256 string `json:"actual_sha256,omitempty"`
}

type PCMasterAuditReport struct {
	Schema           int                 `json:"schema_version"`
	Status           string              `json:"status"`
	Scope            string              `json:"scope"`
	Selection        string              `json:"selection"`
	ExpectedArchives int                 `json:"expected_archives"`
	VerifiedArchives int                 `json:"verified_archives"`
	ExpectedBytes    int64               `json:"expected_bytes"`
	VerifiedBytes    int64               `json:"verified_bytes"`
	Items            []PCMasterAuditItem `json:"items"`
}

// auditPCMasterSpecs is separately exercised on tiny synthetic archives. It
// never logs owner paths or reads an archive into memory, and it does not
// follow symlinks at the archive file boundary.
func auditPCMasterSpecs(files []sourceSpecFile, ms2, finale string) PCMasterAuditReport {
	r := PCMasterAuditReport{Schema: 1, Status: "INCOMPLETE", Scope: "pristine-original-PC-DAT-SHA-only; NO semantic-rebuild-or-runtime-parity"}
	roots := map[string]string{"MS2": ms2, "MSF": finale}
	for _, f := range files {
		if f.Kind != "archive" {
			continue
		}
		item := PCMasterAuditItem{Game: f.Game, File: f.Name, Bytes: f.Bytes, SHA256: f.SHA256, Status: "INVALID_SPEC"}
		r.ExpectedArchives++
		r.ExpectedBytes += f.Bytes
		if (f.Game != "MS2" && f.Game != "MSF") || filepath.Base(f.Name) != f.Name || f.Name == "." || f.Bytes <= 0 || len(f.SHA256) != 64 {
			r.Items = append(r.Items, item)
			continue
		}
		if roots[f.Game] == "" {
			item.Status = "NO_SOURCE_DIRECTORY"
			r.Items = append(r.Items, item)
			continue
		}
		path := filepath.Join(roots[f.Game], f.Name)
		st, err := os.Lstat(path)
		if os.IsNotExist(err) {
			item.Status = "MISSING"
		} else if err != nil {
			item.Status = "UNREADABLE"
		} else if !st.Mode().IsRegular() {
			item.Status = "NOT_REGULAR_OR_SYMLINK"
		} else {
			item.ActualBytes = st.Size()
			if st.Size() != f.Bytes {
				item.Status = "SIZE_MISMATCH"
			} else {
				h := sha256.New()
				fd, err := os.Open(path)
				if err == nil {
					observed, se := fd.Stat()
					if se != nil || !os.SameFile(st, observed) || !observed.Mode().IsRegular() {
						err = fmt.Errorf("source identity changed during open")
					}
					var n int64
					if err == nil {
						n, err = io.Copy(h, fd)
					}
					closeErr := fd.Close()
					if err == nil {
						err = closeErr
					}
					if err == nil && n != st.Size() {
						err = fmt.Errorf("file changed while reading")
					}
				}
				if err != nil {
					item.Status = "READ_FAILED_OR_CHANGED"
				} else {
					item.ActualSHA256 = hex.EncodeToString(h.Sum(nil))
					if item.ActualSHA256 != f.SHA256 {
						item.Status = "SHA256_MISMATCH"
					} else {
						item.Status = "PASS"
						r.VerifiedArchives++
						r.VerifiedBytes += f.Bytes
					}
				}
			}
		}
		r.Items = append(r.Items, item)
	}
	if r.ExpectedArchives > 0 && r.VerifiedArchives == r.ExpectedArchives {
		r.Status = "PASS_SOURCE_IDENTITY_ONLY"
	}
	return r
}

// AuditPCMasterSourcesNative audits all PC DAT archives actually required by
// canonical translations and enabled graphics, not arbitrary guessed paths.
// The PC executables are out of scope, as are Switch original sources.
func AuditPCMasterSourcesNative(root, ms2Master, finaleMaster string, allManifest bool) (PCMasterAuditReport, error) {
	spec, err := sourceCacheSpecForRoot(root)
	if err != nil {
		return PCMasterAuditReport{}, fmt.Errorf("canonical PC archive selection: %w", err)
	}
	files := spec.Files
	selection := "required-translation-and-graphics-union"
	if allManifest {
		selection = "all-pinned-PC-DAT-manifest"
		rows, err := readSourceHashManifest(filepath.Join(root, "toolchain", "manifests", "expected_pc_archive_sha256.tsv"))
		if err != nil {
			return PCMasterAuditReport{}, err
		}
		files = nil
		seen := make(map[string]bool)
		for _, row := range rows {
			game := "MSF"
			if strings.HasPrefix(row.Dataset, "MS2_") {
				game = "MS2"
			} else if !strings.HasPrefix(row.Dataset, "MSF_") {
				return PCMasterAuditReport{}, fmt.Errorf("unsupported pin dataset %q", row.Dataset)
			}
			key := game + "/" + row.File
			if seen[key] {
				return PCMasterAuditReport{}, fmt.Errorf("duplicate pinned DAT %s", key)
			}
			seen[key] = true
			files = append(files, sourceSpecFile{Game: game, Kind: "archive", Name: row.File, Bytes: row.Bytes, SHA256: row.SHA256})
		}
	}
	r := auditPCMasterSpecs(files, ms2Master, finaleMaster)
	r.Selection = selection
	return r, nil
}
