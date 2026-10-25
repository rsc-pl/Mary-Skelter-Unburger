package maintainer

// Opt-in native PC text dataset publisher. The legacy full rebuild remains the
// default until decoded-semantic and runtime checks on the owner's actual
// pristine master DATs establish equivalence. No Python subprocess is used.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/mskit"
)

type PCNativeDatasetArchive struct {
	ArchiveNumber  int    `json:"archive_number"`
	File           string `json:"file"`
	EntriesChanged int    `json:"entries_changed"`
	SourceSize     int64  `json:"source_size"`
	RebuiltSize    int64  `json:"rebuilt_size"`
	ByteIdentical  bool   `json:"byte_identical"`
	SourceSHA256   string `json:"source_sha256"`
	OutputSHA256   string `json:"output_sha256"`
}

type PCNativeDatasetReport struct {
	Dataset            string                   `json:"dataset"`
	Encoder            string                   `json:"encoder"`
	Status             string                   `json:"status"`
	TotalEditedRecords int                      `json:"total_edited_records"`
	TotalEditedEntries int                      `json:"total_edited_entries"`
	Archives           []PCNativeDatasetArchive `json:"archives"`
	GoDecodedVerified  bool                     `json:"go_decoded_verified"`
}

// RebuildPCTextDatasetNative validates every record's source pin against a
// trusted whole-master SHA manifest, builds every affected DAT through the
// existing Go format writers, then publishes one new dataset directory. A
// missing/unsupported input is fatal; there is deliberately no Python fallback
// and no publication of partially constructed archives.
func RebuildPCTextDatasetNative(dataset, sourceMaster, recordsPath, outDir, reportPath, manifestPath string, allowSemanticZlib bool) (PCNativeDatasetReport, error) {
	return rebuildPCTextDatasetNative(dataset, sourceMaster, recordsPath, outDir, reportPath, manifestPath, allowSemanticZlib, false)
}

// RebuildPCTextDatasetNativeVerified is the main maintainer's opt-in entry point.
// It independently re-extracts the entire rebuilt and pristine source datasets,
// compares logical records, byte encodings and unrelated GDAT members *before*
// either the archive directory or its bundle report can be published.
// It does not certify real-game behavior or canonical compressed byte streams.
func RebuildPCTextDatasetNativeVerified(dataset, sourceMaster, recordsPath, outDir, reportPath, manifestPath string, allowSemanticZlib bool) (PCNativeDatasetReport, error) {
	return rebuildPCTextDatasetNative(dataset, sourceMaster, recordsPath, outDir, reportPath, manifestPath, allowSemanticZlib, true)
}

func rebuildPCTextDatasetNative(dataset, sourceMaster, recordsPath, outDir, reportPath, manifestPath string, allowSemanticZlib, strictVerify bool) (PCNativeDatasetReport, error) {
	rep := PCNativeDatasetReport{Dataset: dataset, Status: "not-published"}
	if dataset != "MS2_PC_LOCALIZED" && dataset != "MSF_PC_LOCALIZED" && dataset != "MSF_PC_SYSTEM" {
		return rep, fmt.Errorf("unsupported native PC dataset %q", dataset)
	}
	if !allowSemanticZlib {
		rep.Encoder = "strict-no-new-zlib"
	} else {
		rep.Encoder = "go-zlib-semantic-experimental"
	}
	for _, p := range []string{outDir, reportPath} {
		if p == "" {
			return rep, fmt.Errorf("empty publication path")
		}
		if st, err := os.Lstat(p); err == nil {
			return rep, fmt.Errorf("refuse pre-existing publication %s (%s)", p, st.Mode())
		} else if !os.IsNotExist(err) {
			return rep, err
		}
	}
	f, err := os.Open(recordsPath)
	if err != nil {
		return rep, err
	}
	plan, err := PlanPCTextRecords(f, dataset)
	closeErr := f.Close()
	if err != nil {
		return rep, fmt.Errorf("native text plan: %w", err)
	}
	if closeErr != nil {
		return rep, closeErr
	}
	if plan.ChangedRecords == 0 || len(plan.Archives) == 0 {
		return rep, fmt.Errorf("no changed records for %s", dataset)
	}
	rows, err := readSourceHashManifest(manifestPath)
	if err != nil {
		return rep, err
	}
	pinDataset := dataset
	if dataset == "MSF_PC_SYSTEM" {
		pinDataset = "MSF_PC_LOCALIZED"
	}
	pins := map[string]sourceHashRow{}
	for _, row := range rows {
		if row.Dataset != pinDataset {
			continue
		}
		if _, ok := pins[row.File]; ok {
			return rep, fmt.Errorf("duplicate canonical pin for %s/%s", pinDataset, row.File)
		}
		pins[row.File] = row
	}
	if err := os.MkdirAll(filepath.Dir(outDir), 0o755); err != nil {
		return rep, err
	}
	stage, err := os.MkdirTemp(filepath.Dir(outDir), ".pc-text-native-*")
	if err != nil {
		return rep, err
	}
	defer os.RemoveAll(stage)
	stageMaster := filepath.Join(stage, "masterWindows")
	if err := os.MkdirAll(stageMaster, 0o755); err != nil {
		return rep, err
	}
	var encoder mskit.CanonicalBILZEncoder
	if allowSemanticZlib {
		encoder = mskit.WrapBILZGo
	}
	for _, archive := range plan.Archives {
		filename := fmt.Sprintf("GxArchivedFile%03d.dat", archive)
		pin, ok := pins[filename]
		if !ok || pin.SHA256 == "" || pin.Bytes <= 0 {
			return rep, fmt.Errorf("no whole-original DAT pin for %s/%s", dataset, filename)
		}
		src := filepath.Join(sourceMaster, filename)
		st, e := os.Lstat(src)
		if e != nil {
			return rep, fmt.Errorf("missing pristine %s: %w", filename, e)
		}
		if !st.Mode().IsRegular() || st.Size() != pin.Bytes {
			return rep, fmt.Errorf("pristine %s type/size mismatch", filename)
		}
		original, e := os.ReadFile(src)
		if e != nil {
			return rep, e
		}
		sourceSHA := mergeSHA(original)
		if !strings.EqualFold(pin.SHA256, sourceSHA) {
			return rep, fmt.Errorf("pristine %s whole-file SHA mismatch", filename)
		}
		output, wr, e := RebuildPCTextArchiveNative(original, dataset, archive, plan, encoder)
		if e != nil {
			return rep, fmt.Errorf("native rebuild %s: %w", filename, e)
		}
		if len(wr.ChangedEntries) == 0 || wr.ChangedFields == 0 || bytes.Equal(original, output) {
			return rep, fmt.Errorf("unexpected empty native rebuild %s", filename)
		}
		if e = os.WriteFile(filepath.Join(stageMaster, filename), output, 0o600); e != nil {
			return rep, e
		}
		rep.Archives = append(rep.Archives, PCNativeDatasetArchive{ArchiveNumber: archive, File: filename,
			EntriesChanged: len(wr.ChangedEntries), SourceSize: int64(len(original)), RebuiltSize: int64(len(output)),
			ByteIdentical: false, SourceSHA256: sourceSHA, OutputSHA256: wr.OutputSHA256})
		rep.TotalEditedEntries += len(wr.ChangedEntries)
		rep.TotalEditedRecords += wr.ChangedFields
	}
	if rep.TotalEditedRecords != plan.ChangedRecords {
		return rep, fmt.Errorf("planned/committed field count mismatch: %d vs %d", plan.ChangedRecords, rep.TotalEditedRecords)
	}
	if strictVerify {
		verificationPath := filepath.Join(stage, "_native_go_prepublication_verify.json")
		if _, err := VerifyTextDatasetGoStrict(dataset, stageMaster, recordsPath, sourceMaster, plan.Archives, verificationPath); err != nil {
			return rep, fmt.Errorf("native text prepublication decoded verification: %w", err)
		}
		// Verification's overlays, record dumps and reports are private to the
		// staging transaction. Only the intended masterWindows subtree may be
		// promoted. The ordinary pipeline writes its external Verify report.
		items, err := os.ReadDir(stage)
		if err != nil {
			return rep, err
		}
		for _, item := range items {
			if item.Name() == "masterWindows" {
				continue
			}
			if err := os.RemoveAll(filepath.Join(stage, item.Name())); err != nil {
				return rep, err
			}
		}
		rep.GoDecodedVerified = true
	}
	rep.Status = "native-go-staged-unapproved-for-game"
	reportRaw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return rep, err
	}
	reportRaw = append(reportRaw, '\n')
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return rep, err
	}
	// Reserve the report as a new inode; the new DAT directory is published
	// only when every archive has been built. Never replace user content.
	rf, err := os.OpenFile(reportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return rep, err
	}
	published := false
	defer func() {
		if !published {
			os.Remove(reportPath)
		}
	}()
	if _, err = io.Copy(rf, bytes.NewReader(reportRaw)); err != nil {
		rf.Close()
		return rep, err
	}
	if err = rf.Sync(); err != nil {
		rf.Close()
		return rep, err
	}
	if err = rf.Close(); err != nil {
		return rep, err
	}
	if _, err := os.Lstat(outDir); !os.IsNotExist(err) {
		return rep, fmt.Errorf("output appeared before publication: %s", outDir)
	}
	if err := os.Rename(stage, outDir); err != nil {
		return rep, err
	}
	published = true
	return rep, nil
}
