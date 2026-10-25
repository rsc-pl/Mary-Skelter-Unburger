package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var datNameRE = regexp.MustCompile(`^GxArchivedFile[0-9]{3}\.dat$`)

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

type PatchManifest struct {
	Files []string `json:"files"`
}

type Bundle struct {
	Profile  Profile
	Root     string
	DatFiles map[string]string
}

type GameStatus struct {
	Profile        Profile
	Root           string
	ExeSHA         string
	ExeState       string
	LegacyWarnings []string
}

type Operation struct {
	GameKey           string            `json:"game"`
	RelativePath      string            `json:"relative_path"`
	Action            string            `json:"action"`
	SourcePath        string            `json:"-"`
	PayloadPath       string            `json:"-"`
	PayloadObject     string            `json:"-"`
	PayloadSHA        string            `json:"payload_sha256,omitempty"`
	GDATPatch         *GDATPatchArchive `json:"-"`
	ExpectedBeforeSHA string            `json:"expected_before_sha256,omitempty"`
	Generated         []byte            `json:"-"`
	SourceSHA         string            `json:"source_sha256"`
	BeforeSHA         string            `json:"before_sha256,omitempty"`
	BackupPath        string            `json:"backup_path,omitempty"`
	Existed           bool              `json:"existed"`
	BestEffort        bool              `json:"best_effort,omitempty"`
}

type OperationFailure struct {
	GameKey      string `json:"game"`
	RelativePath string `json:"relative_path"`
	Action       string `json:"action"`
	Error        string `json:"error"`
}

// ProgressEvent reports deterministic unit progress for interactive callers.
// Core CLI callers may ignore it; no timing-derived or synthetic percentages are
// emitted.
type ProgressEvent struct {
	Phase        string
	Action       string
	GameKey      string
	RelativePath string
	Detail       string
	Done         int
	Total        int
}

type ProgressFunc func(ProgressEvent)

func emitProgress(fn ProgressFunc, event ProgressEvent) {
	if fn != nil {
		fn(event)
	}
}

type Plan struct {
	Games        []GameStatus
	Operations   []Operation
	Warnings     []string
	SoftFailures []OperationFailure
}

type Receipt struct {
	SchemaVersion int                `json:"schema_version"`
	InstalledAt   string             `json:"installed_at"`
	Operations    []Operation        `json:"operations"`
	Warnings      []string           `json:"warnings,omitempty"`
	SoftFailures  []OperationFailure `json:"soft_failures,omitempty"`
}

func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func LoadBundle(bundleRoot string, p Profile) (Bundle, error) {
	root := filepath.Join(bundleRoot, p.BundleDir)
	manifestPath := filepath.Join(root, "PATCH_MANIFEST.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Bundle{}, fmt.Errorf("%s bundle manifest: %w", p.DisplayName, err)
	}
	var manifest PatchManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return Bundle{}, fmt.Errorf("%s bundle manifest: %w", p.DisplayName, err)
	}
	if len(manifest.Files) == 0 {
		return Bundle{}, fmt.Errorf("%s bundle manifest has no DAT files", p.DisplayName)
	}
	dats := make(map[string]string, len(manifest.Files))
	for _, name := range manifest.Files {
		if !datNameRE.MatchString(name) || filepath.Base(name) != name {
			return Bundle{}, fmt.Errorf("%s bundle has unsafe DAT name %q", p.DisplayName, name)
		}
		if _, dup := dats[name]; dup {
			return Bundle{}, fmt.Errorf("%s bundle repeats %s", p.DisplayName, name)
		}
		path := filepath.Join(root, "masterWindows", name)
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			return Bundle{}, fmt.Errorf("%s bundle missing regular file %s", p.DisplayName, path)
		}
		dats[name] = path
	}
	entries, err := os.ReadDir(filepath.Join(root, "masterWindows"))
	if err != nil {
		return Bundle{}, err
	}
	for _, e := range entries {
		if e.IsDir() || !datNameRE.MatchString(e.Name()) {
			continue
		}
		if _, ok := dats[e.Name()]; !ok {
			return Bundle{}, fmt.Errorf("%s bundle contains DAT not listed by PATCH_MANIFEST.json: %s", p.DisplayName, e.Name())
		}
	}
	return Bundle{Profile: p, Root: root, DatFiles: dats}, nil
}

func InspectGame(root string, p Profile) (GameStatus, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return GameStatus{}, err
	}
	if info, err := os.Stat(filepath.Join(root, "masterWindows")); err != nil || !info.IsDir() {
		return GameStatus{}, fmt.Errorf("%s: missing masterWindows directory", root)
	}
	exe := filepath.Join(root, p.ExeName)
	exeBytes, err := os.ReadFile(exe)
	if err != nil {
		return GameStatus{}, fmt.Errorf("%s: %w", exe, err)
	}
	sha := sha256Bytes(exeBytes)
	state := classifyExecutable(exeBytes, p)
	st := GameStatus{Profile: p, Root: root, ExeSHA: sha, ExeState: state}
	for _, legacy := range p.LegacyFiles {
		path := filepath.Join(root, filepath.FromSlash(legacy.RelativePath))
		got, err := SHA256File(path)
		if err != nil {
			st.LegacyWarnings = append(st.LegacyWarnings, fmt.Sprintf("legacy-only file unavailable: %s", path))
			continue
		}
		if got != legacy.PristineSHA {
			st.LegacyWarnings = append(st.LegacyWarnings, fmt.Sprintf("legacy-only file is not byte-pristine: %s (got %s)", path, got))
		}
	}
	return st, nil
}

func BuildPlan(bundleRoot string, roots map[string]string) (Plan, error) {
	var plan Plan
	keys := make([]string, 0, len(roots))
	for key := range roots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p, ok := Profiles[key]
		if !ok {
			return Plan{}, fmt.Errorf("unknown game profile %q", key)
		}
		st, err := InspectGame(roots[key], p)
		if err != nil {
			return Plan{}, err
		}
		if st.ExeState == "unknown" {
			return Plan{}, fmt.Errorf("%s executable is unsupported: %s", p.DisplayName, st.ExeSHA)
		}
		bundle, err := LoadBundle(bundleRoot, p)
		if err != nil {
			return Plan{}, err
		}
		plan.Games = append(plan.Games, st)
		plan.Warnings = append(plan.Warnings, st.LegacyWarnings...)

		exePath := filepath.Join(st.Root, p.ExeName)
		exeBytes, err := os.ReadFile(exePath)
		if err != nil {
			return Plan{}, err
		}
		patched, _, err := PatchExecutable(exeBytes, p)
		if err != nil {
			return Plan{}, err
		}
		if st.ExeState != "final" {
			plan.Operations = append(plan.Operations, Operation{GameKey: key, RelativePath: p.ExeName, Action: "PATCH", Generated: patched, SourceSHA: sha256Bytes(patched)})
		}

		for _, marker := range p.RequiredMarkers {
			if filepath.Base(marker) != marker {
				return Plan{}, fmt.Errorf("%s has unsafe required marker %q", p.DisplayName, marker)
			}
			dst := filepath.Join(st.Root, marker)
			info, err := os.Stat(dst)
			switch {
			case err == nil && info.Mode().IsRegular():
				// Presence is the game contract; preserve any existing regular file.
			case err == nil:
				return Plan{}, fmt.Errorf("%s required marker is not a regular file: %s", p.DisplayName, dst)
			case os.IsNotExist(err):
				plan.Operations = append(plan.Operations, Operation{
					GameKey: key, RelativePath: marker, Action: "CREATE",
					Generated: []byte{}, SourceSHA: emptySHA256,
				})
			default:
				return Plan{}, err
			}
		}

		names := make([]string, 0, len(bundle.DatFiles))
		for name := range bundle.DatFiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			src := bundle.DatFiles[name]
			sha, err := SHA256File(src)
			if err != nil {
				return Plan{}, err
			}
			rel := filepath.Join("masterWindows", name)
			dst := filepath.Join(st.Root, rel)
			if current, err := SHA256File(dst); err == nil && current == sha {
				continue
			}
			plan.Operations = append(plan.Operations, Operation{GameKey: key, RelativePath: filepath.ToSlash(rel), Action: "INSTALL", SourcePath: src, SourceSHA: sha})
		}

		if key == "finale" {
			assets, err := finaleRemuxAssets(defaultAssetStageRoot(bundleRoot))
			if err != nil {
				plan.SoftFailures = append(plan.SoftFailures, OperationFailure{
					GameKey: key, RelativePath: "masterWindows/MOVIE_EN", Action: "REMUX",
					Error: "movie remux assets unavailable: " + err.Error(),
				})
				continue
			}
			for _, a := range assets {
				dst := filepath.Join(st.Root, filepath.FromSlash(a.RelativePath))
				current, err := SHA256File(dst)
				if err != nil {
					plan.SoftFailures = append(plan.SoftFailures, OperationFailure{GameKey: key, RelativePath: a.RelativePath, Action: "REMUX", Error: err.Error()})
					continue
				}
				if current == a.TargetSHA256 {
					continue
				}
				compatible := current == a.SourceSHA256
				if !compatible && a.SourceVideoSHA256 != "" {
					videoSHA, videoErr := FinaleMovieVideoSHA256(dst)
					if videoErr == nil && videoSHA == a.SourceVideoSHA256 {
						compatible = true
					} else if videoErr != nil {
						plan.SoftFailures = append(plan.SoftFailures, OperationFailure{GameKey: key, RelativePath: a.RelativePath, Action: "REMUX", Error: "cannot validate movie video identity: " + videoErr.Error()})
						continue
					}
				}
				if !compatible {
					plan.SoftFailures = append(plan.SoftFailures, OperationFailure{
						GameKey: key, RelativePath: a.RelativePath, Action: "REMUX",
						Error: fmt.Sprintf("unsupported movie state %s (video identity does not match the shipped/remux-compatible source)", current),
					})
					continue
				}
				plan.Operations = append(plan.Operations, Operation{
					GameKey: key, RelativePath: a.RelativePath, Action: "REMUX", BestEffort: true,
					PayloadPath: a.PayloadPath, PayloadSHA: a.PayloadSHA256,
					ExpectedBeforeSHA: current, SourceSHA: a.TargetSHA256,
				})
			}
		}
	}
	return plan, nil
}

func Install(plan Plan, roots map[string]string, backupRoot string, dryRun bool) (Receipt, error) {
	return installWithAssets(plan, roots, backupRoot, dryRun, nil, nil)
}

func InstallAssets(plan Plan, roots map[string]string, backupRoot string, dryRun bool, assets *ReleaseAssets) (Receipt, error) {
	return InstallAssetsWithProgress(plan, roots, backupRoot, dryRun, assets, nil)
}

func InstallAssetsWithProgress(plan Plan, roots map[string]string, backupRoot string, dryRun bool, assets *ReleaseAssets, progress ProgressFunc) (Receipt, error) {
	if assets == nil {
		return Receipt{}, fmt.Errorf("release assets are required")
	}
	return installWithAssets(plan, roots, backupRoot, dryRun, assets, progress)
}

func installWithAssets(plan Plan, roots map[string]string, backupRoot string, dryRun bool, assets *ReleaseAssets, progress ProgressFunc) (Receipt, error) {
	receipt := Receipt{
		SchemaVersion: 3,
		InstalledAt:   time.Now().UTC().Format(time.RFC3339),
		Warnings:      append([]string(nil), plan.Warnings...),
		SoftFailures:  append([]OperationFailure(nil), plan.SoftFailures...),
	}
	if dryRun {
		receipt.Operations = append(receipt.Operations, plan.Operations...)
		return receipt, nil
	}

	// Persistent backup is optional. Transaction rollback is mandatory for
	// critical operations. Best-effort movie remuxes roll back only their own
	// file and never abort otherwise-valid EXE/DAT/text installation work.
	if backupRoot != "" {
		if err := os.MkdirAll(filepath.Dir(backupRoot), 0o755); err != nil {
			return Receipt{}, err
		}
		if err := os.Mkdir(backupRoot, 0o755); err != nil {
			return Receipt{}, fmt.Errorf("backup root must be new: %w", err)
		}
	}
	tx := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	type appliedOperation struct {
		op           Operation
		rollbackPath string
	}
	applied := make([]appliedOperation, 0, len(plan.Operations))
	rollback := func(cause error) (Receipt, error) {
		for i := len(applied) - 1; i >= 0; i-- {
			a := applied[i]
			dst := filepath.Join(roots[a.op.GameKey], filepath.FromSlash(a.op.RelativePath))
			if a.op.Existed {
				_ = os.Remove(dst)
				if a.rollbackPath != "" {
					_ = os.Rename(a.rollbackPath, dst)
				}
			} else {
				_ = os.Remove(dst)
			}
		}
		if backupRoot != "" {
			_ = os.RemoveAll(backupRoot)
		}
		return Receipt{}, fmt.Errorf("install failed and transaction rollback was attempted: %w", cause)
	}

	totalOps := len(plan.Operations)
	doneOps := 0
	emitProgress(progress, ProgressEvent{Phase: "install", Detail: "Ready", Done: 0, Total: totalOps})
	for _, base := range plan.Operations {
		op := base
		emitProgress(progress, ProgressEvent{Phase: "install", Action: op.Action, GameKey: op.GameKey, RelativePath: op.RelativePath, Detail: "Applying", Done: doneOps, Total: totalOps})
		dst := filepath.Join(roots[op.GameKey], filepath.FromSlash(op.RelativePath))
		var rollbackPath string
		currentApplied := false

		softFail := func(cause error) {
			if currentApplied {
				_ = os.Remove(dst)
				if op.Existed && rollbackPath != "" {
					_ = os.Rename(rollbackPath, dst)
				}
				applied = applied[:len(applied)-1]
			}
			if op.BackupPath != "" {
				_ = os.Remove(op.BackupPath)
			}
			receipt.SoftFailures = append(receipt.SoftFailures, OperationFailure{
				GameKey: op.GameKey, RelativePath: op.RelativePath, Action: op.Action, Error: cause.Error(),
			})
			doneOps++
			emitProgress(progress, ProgressEvent{Phase: "install", Action: op.Action, GameKey: op.GameKey, RelativePath: op.RelativePath, Detail: "Skipped after non-critical failure", Done: doneOps, Total: totalOps})
		}
		fail := func(cause error) (bool, Receipt, error) {
			if op.BestEffort {
				softFail(cause)
				return true, Receipt{}, nil
			}
			r, err := rollback(cause)
			return false, r, err
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			if handled, r, fatalErr := fail(err); handled {
				continue
			} else {
				return r, fatalErr
			}
		}
		if _, err := os.Stat(dst); err == nil {
			op.Existed = true
			op.BeforeSHA, err = SHA256File(dst)
			if err != nil {
				if handled, r, fatalErr := fail(err); handled {
					continue
				} else {
					return r, fatalErr
				}
			}
			if op.ExpectedBeforeSHA != "" && op.BeforeSHA != op.ExpectedBeforeSHA {
				cause := fmt.Errorf("source drift before %s %s: got %s expected %s", op.Action, dst, op.BeforeSHA, op.ExpectedBeforeSHA)
				if handled, r, fatalErr := fail(cause); handled {
					continue
				} else {
					return r, fatalErr
				}
			}
			rollbackPath = dst + ".unburger-rollback." + tx
			if _, err := os.Lstat(rollbackPath); !os.IsNotExist(err) {
				cause := err
				if cause == nil {
					cause = fmt.Errorf("transaction rollback path already exists: %s", rollbackPath)
				}
				if handled, r, fatalErr := fail(cause); handled {
					continue
				} else {
					return r, fatalErr
				}
			}
			if err := os.Rename(dst, rollbackPath); err != nil {
				cause := fmt.Errorf("stage original %s for rollback: %w", dst, err)
				if handled, r, fatalErr := fail(cause); handled {
					continue
				} else {
					return r, fatalErr
				}
			}
			applied = append(applied, appliedOperation{op: op, rollbackPath: rollbackPath})
			currentApplied = true
			if backupRoot != "" {
				op.BackupPath = filepath.Join(backupRoot, op.GameKey, filepath.FromSlash(op.RelativePath))
				if err := copyAtomic(rollbackPath, op.BackupPath); err != nil {
					cause := fmt.Errorf("persistent backup %s: %w", dst, err)
					if handled, r, fatalErr := fail(cause); handled {
						continue
					} else {
						return r, fatalErr
					}
				}
				applied[len(applied)-1].op = op
			}
		} else if os.IsNotExist(err) {
			applied = append(applied, appliedOperation{op: op})
			currentApplied = true
		} else {
			if handled, r, fatalErr := fail(err); handled {
				continue
			} else {
				return r, fatalErr
			}
		}

		var writeErr error
		switch {
		case op.Action == "PATCH-GDAT":
			if assets == nil || op.GDATPatch == nil || rollbackPath == "" {
				writeErr = fmt.Errorf("container-backed GDAT operation is incomplete: %s", dst)
				break
			}
			writeErr = ApplyGDATPatchObjects(rollbackPath, dst, *op.GDATPatch, assets.ReadObject)
			if writeErr == nil {
				if info, err := os.Stat(rollbackPath); err == nil {
					_ = os.Chmod(dst, info.Mode().Perm())
				}
			}
		case op.Action == "REMUX":
			if rollbackPath == "" {
				writeErr = fmt.Errorf("remux requires an existing source movie: %s", dst)
				break
			}
			payloadPath := op.PayloadPath
			removePayload := false
			if op.PayloadObject != "" {
				if assets == nil {
					writeErr = fmt.Errorf("movie payload container unavailable")
					break
				}
				b, err := assets.ReadObject(op.PayloadObject)
				if err != nil {
					writeErr = fmt.Errorf("read movie payload %s: %w", op.PayloadObject, err)
					break
				}
				if bytesSHA(b) != op.PayloadSHA {
					writeErr = fmt.Errorf("movie payload SHA mismatch for %s", op.PayloadObject)
					break
				}
				payloadPath = dst + ".unburger-audio-payload." + tx + ".m4a"
				if err := os.WriteFile(payloadPath, b, 0o600); err != nil {
					writeErr = err
					break
				}
				removePayload = true
			}
			if removePayload {
				defer os.Remove(payloadPath)
			}
			if op.PayloadObject == "" {
				payloadSHA, err := SHA256File(payloadPath)
				if err != nil || payloadSHA != op.PayloadSHA {
					writeErr = fmt.Errorf("remux payload drift %s: got %s expected %s err=%v", payloadPath, payloadSHA, op.PayloadSHA, err)
					break
				}
			}
			if err := RemuxFinaleMovie(rollbackPath, payloadPath, dst); err != nil {
				writeErr = fmt.Errorf("remux %s: %w", dst, err)
			}
			if removePayload {
				_ = os.Remove(payloadPath)
			}
			if writeErr == nil {
				if info, err := os.Stat(rollbackPath); err == nil {
					_ = os.Chmod(dst, info.Mode().Perm())
				}
			}
		case op.Generated != nil:
			mode := os.FileMode(0o755)
			if rollbackPath != "" {
				if info, err := os.Stat(rollbackPath); err == nil {
					mode = info.Mode().Perm()
				}
			}
			writeErr = writeAtomic(op.Generated, dst, mode)
		case op.SourcePath != "":
			writeErr = copyAtomic(op.SourcePath, dst)
		default:
			writeErr = fmt.Errorf("operation has no payload: %s %s", op.Action, dst)
		}
		if writeErr != nil {
			if handled, r, fatalErr := fail(writeErr); handled {
				continue
			} else {
				return r, fatalErr
			}
		}
		var verifyErr error
		if op.Action == "PATCH-GDAT" {
			needs, err := InspectGDATPatchState(dst, *op.GDATPatch)
			if err != nil {
				verifyErr = err
			} else if needs {
				verifyErr = fmt.Errorf("managed GDAT entries did not reach target state")
			}
		} else {
			got, err := SHA256File(dst)
			if err != nil {
				verifyErr = err
			} else if got != op.SourceSHA {
				verifyErr = fmt.Errorf("post-write SHA-256 %s != %s", got, op.SourceSHA)
			}
		}
		if verifyErr != nil {
			cause := fmt.Errorf("verify %s: %w", dst, verifyErr)
			if handled, r, fatalErr := fail(cause); handled {
				continue
			} else {
				return r, fatalErr
			}
		}
		receipt.Operations = append(receipt.Operations, op)
		doneOps++
		emitProgress(progress, ProgressEvent{Phase: "install", Action: op.Action, GameKey: op.GameKey, RelativePath: op.RelativePath, Detail: "Verified", Done: doneOps, Total: totalOps})
	}

	if backupRoot != "" {
		raw, err := json.MarshalIndent(receipt, "", "  ")
		if err != nil {
			return rollback(err)
		}
		if err := os.WriteFile(filepath.Join(backupRoot, "receipt.json"), append(raw, '\n'), 0o644); err != nil {
			return rollback(err)
		}
	}
	for _, a := range applied {
		if a.rollbackPath == "" {
			continue
		}
		if err := os.Remove(a.rollbackPath); err != nil && !os.IsNotExist(err) {
			receipt.Warnings = append(receipt.Warnings, fmt.Sprintf("could not remove transaction rollback file %s: %v", a.rollbackPath, err))
		}
	}
	return receipt, nil
}

func writeAtomic(data []byte, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.unburger.tmp.%d", dst, os.Getpid())
	old := fmt.Sprintf("%s.unburger.old.%d", dst, os.Getpid())
	_ = os.Remove(tmp)
	_ = os.Remove(old)
	if err := os.WriteFile(tmp, data, mode.Perm()); err != nil {
		return err
	}
	_, statErr := os.Stat(dst)
	hadDst := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		_ = os.Remove(tmp)
		return statErr
	}
	if hadDst {
		if err := os.Rename(dst, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if hadDst {
			_ = os.Rename(old, dst)
		}
		_ = os.Remove(tmp)
		return err
	}
	if hadDst {
		_ = os.Remove(old)
	}
	return nil
}

func copyAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.unburger.tmp.%d", dst, os.Getpid())
	old := fmt.Sprintf("%s.unburger.old.%d", dst, os.Getpid())
	_ = os.Remove(tmp)
	_ = os.Remove(old)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Chmod(tmp, info.Mode().Perm()); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_, statErr := os.Stat(dst)
	hadDst := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		_ = os.Remove(tmp)
		return statErr
	}
	if hadDst {
		if err := os.Rename(dst, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		if hadDst {
			_ = os.Rename(old, dst)
		}
		_ = os.Remove(tmp)
		return err
	}
	if hadDst {
		_ = os.Remove(old)
	}
	return nil
}

func FormatPlan(plan Plan) string {
	var b strings.Builder
	for _, game := range plan.Games {
		fmt.Fprintf(&b, "%s: %s executable (%s)\n", game.Profile.DisplayName, game.ExeState, game.ExeSHA)
	}
	for _, w := range plan.Warnings {
		fmt.Fprintf(&b, "WARNING: %s\n", w)
	}
	if len(plan.Operations) == 0 && len(plan.SoftFailures) == 0 {
		b.WriteString("Already current: no managed files need changes\n")
	} else if len(plan.Operations) == 0 {
		b.WriteString("No critical files need changes; unresolved best-effort items are listed below\n")
	}
	for _, f := range plan.SoftFailures {
		fmt.Fprintf(&b, "WARN    %-6s %s (%s: %s)\n", f.GameKey, f.RelativePath, f.Action, f.Error)
	}
	for _, op := range plan.Operations {
		action := op.Action
		if action == "" {
			if op.Generated != nil {
				action = "PATCH"
			} else {
				action = "INSTALL"
			}
		}
		fmt.Fprintf(&b, "%-7s %-6s %s\n", action, op.GameKey, op.RelativePath)
	}
	return b.String()
}
