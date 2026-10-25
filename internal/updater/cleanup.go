package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type CleanupManifestEntry struct {
	GameKey      string `json:"game"`
	RelativePath string `json:"relative_path"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
}

type CleanupDirectorySpec struct {
	GameKey       string   `json:"game"`
	RelativePath  string   `json:"relative_path"`
	ExpectedFiles []string `json:"expected_files"`
}

type CleanupDirectoryPlan struct {
	GameKey      string                 `json:"game"`
	RelativePath string                 `json:"relative_path"`
	Files        []CleanupManifestEntry `json:"files"`
	TotalBytes   int64                  `json:"total_bytes"`
}

type CleanupAuditFile = CleanupManifestEntry

type CleanupAudit struct {
	SchemaVersion int                `json:"schema_version"`
	Files         []CleanupAuditFile `json:"files"`
	TotalBytes    int64              `json:"total_bytes"`
}

type CleanupPlan struct {
	Files       []CleanupManifestEntry `json:"files"`
	Directories []CleanupDirectoryPlan `json:"directories,omitempty"`
	TotalBytes  int64                  `json:"total_bytes"`
}

type CleanupReceipt struct {
	SchemaVersion int                    `json:"schema_version"`
	RemovedAt     string                 `json:"removed_at"`
	Files         []CleanupManifestEntry `json:"files"`
	Directories   []string               `json:"directories,omitempty"`
	TotalBytes    int64                  `json:"total_bytes"`
}

func AuditCleanupCandidates(roots map[string]string) (CleanupAudit, error) {
	out := CleanupAudit{SchemaVersion: 1}
	keys := make([]string, 0, len(roots))
	for key := range roots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p, ok := Profiles[key]
		if !ok {
			return CleanupAudit{}, fmt.Errorf("unknown game profile %q", key)
		}
		root, err := filepath.Abs(roots[key])
		if err != nil {
			return CleanupAudit{}, err
		}
		for _, rel := range p.CleanupAuditRoots {
			path := filepath.Join(root, filepath.FromSlash(rel))
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return CleanupAudit{}, err
			}
			if info.Mode().IsRegular() {
				f, err := auditCleanupFile(root, key, path)
				if err != nil {
					return CleanupAudit{}, err
				}
				out.Files = append(out.Files, f)
				out.TotalBytes += f.Size
				continue
			}
			if !info.IsDir() {
				return CleanupAudit{}, fmt.Errorf("cleanup audit root is not a regular file or directory: %s", path)
			}
			err = filepath.WalkDir(path, func(candidate string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.Type()&os.ModeSymlink != 0 {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if d.IsDir() {
					return nil
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				if !info.Mode().IsRegular() {
					return nil
				}
				f, err := auditCleanupFile(root, key, candidate)
				if err != nil {
					return err
				}
				out.Files = append(out.Files, f)
				out.TotalBytes += f.Size
				return nil
			})
			if err != nil {
				return CleanupAudit{}, err
			}
		}
	}
	sort.Slice(out.Files, func(i, j int) bool {
		if out.Files[i].GameKey != out.Files[j].GameKey {
			return out.Files[i].GameKey < out.Files[j].GameKey
		}
		return out.Files[i].RelativePath < out.Files[j].RelativePath
	})
	return out, nil
}

func auditCleanupFile(root, gameKey, path string) (CleanupAuditFile, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return CleanupAuditFile{}, err
	}
	if !filepath.IsLocal(rel) {
		return CleanupAuditFile{}, fmt.Errorf("cleanup candidate escaped game root: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return CleanupAuditFile{}, err
	}
	sha, err := SHA256File(path)
	if err != nil {
		return CleanupAuditFile{}, err
	}
	return CleanupAuditFile{GameKey: gameKey, RelativePath: filepath.ToSlash(rel), Size: info.Size(), SHA256: sha}, nil
}

func (a CleanupAudit) JSON() ([]byte, error) { return json.MarshalIndent(a, "", "  ") }

// CleanupPotentialBytes is a fast display estimate. Exact validation and hashes
// are done in BuildCleanupPlan before the first destructive operation.
func CleanupPotentialBytes(roots map[string]string) int64 {
	var total int64
	for _, f := range englishCleanupAllowlist {
		if roots[f.GameKey] != "" {
			total += f.Size
		}
	}
	for _, d := range englishCleanupDirectories {
		root := roots[d.GameKey]
		if root == "" {
			continue
		}
		for _, name := range d.ExpectedFiles {
			if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(d.RelativePath), name)); err == nil && info.Mode().IsRegular() {
				total += info.Size()
			}
		}
	}
	return total
}

// BuildCleanupPlan validates every present cleanup target before any deletion is
// possible. Missing targets are already-clean. Standalone files are exact
// path/size/SHA pinned. Known purge directories must contain exactly their
// declared regular-file inventory; no wildcard or recursive unknown content is
// ever accepted.
func BuildCleanupPlan(roots map[string]string) (CleanupPlan, error) {
	return BuildCleanupPlanWithProgress(roots, nil)
}

// BuildCleanupPlanWithProgress validates the exact cleanup allow-list while
// reporting one deterministic unit per potential shipped dub file.
func BuildCleanupPlanWithProgress(roots map[string]string, progress ProgressFunc) (CleanupPlan, error) {
	var plan CleanupPlan
	total := 0
	for _, f := range englishCleanupAllowlist {
		if roots[f.GameKey] != "" {
			total++
		}
	}
	for _, spec := range englishCleanupDirectories {
		if roots[spec.GameKey] != "" {
			total += len(spec.ExpectedFiles)
		}
	}
	done := 0
	emitProgress(progress, ProgressEvent{Phase: "cleanup-preflight", Detail: "Starting cleanup allow-list verification", Done: done, Total: total})
	for _, f := range englishCleanupAllowlist {
		root := roots[f.GameKey]
		if root == "" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(f.RelativePath))
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			done++
			emitProgress(progress, ProgressEvent{Phase: "cleanup-preflight", Action: "CHECK", GameKey: f.GameKey, RelativePath: f.RelativePath, Detail: "Already absent", Done: done, Total: total})
			continue
		}
		if err != nil {
			return CleanupPlan{}, err
		}
		if !info.Mode().IsRegular() {
			return CleanupPlan{}, fmt.Errorf("cleanup target is not a regular file: %s", path)
		}
		if info.Size() != f.Size {
			return CleanupPlan{}, fmt.Errorf("cleanup target size mismatch: %s (got %d, expected %d)", path, info.Size(), f.Size)
		}
		sha, err := SHA256File(path)
		if err != nil {
			return CleanupPlan{}, err
		}
		if sha != f.SHA256 {
			return CleanupPlan{}, fmt.Errorf("cleanup target SHA-256 mismatch: %s (got %s, expected %s)", path, sha, f.SHA256)
		}
		plan.Files = append(plan.Files, f)
		plan.TotalBytes += f.Size
		done++
		emitProgress(progress, ProgressEvent{Phase: "cleanup-preflight", Action: "CHECK", GameKey: f.GameKey, RelativePath: f.RelativePath, Detail: "Verified", Done: done, Total: total})
	}
	for _, spec := range englishCleanupDirectories {
		root := roots[spec.GameKey]
		if root == "" {
			continue
		}
		dirPlan, exists, err := buildCleanupDirectoryPlanWithProgress(root, spec, &done, total, progress)
		if err != nil {
			return CleanupPlan{}, err
		}
		if !exists {
			continue
		}
		plan.Directories = append(plan.Directories, dirPlan)
		plan.TotalBytes += dirPlan.TotalBytes
	}
	return plan, nil
}

func buildCleanupDirectoryPlan(root string, spec CleanupDirectorySpec) (CleanupDirectoryPlan, bool, error) {
	done := 0
	return buildCleanupDirectoryPlanWithProgress(root, spec, &done, len(spec.ExpectedFiles), nil)
}

func buildCleanupDirectoryPlanWithProgress(root string, spec CleanupDirectorySpec, done *int, total int, progress ProgressFunc) (CleanupDirectoryPlan, bool, error) {
	path := filepath.Join(root, filepath.FromSlash(spec.RelativePath))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		for _, name := range spec.ExpectedFiles {
			(*done)++
			emitProgress(progress, ProgressEvent{Phase: "cleanup-preflight", Action: "CHECK", GameKey: spec.GameKey, RelativePath: filepath.ToSlash(filepath.Join(spec.RelativePath, name)), Detail: "Already absent", Done: *done, Total: total})
		}
		return CleanupDirectoryPlan{}, false, nil
	}
	if err != nil {
		return CleanupDirectoryPlan{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return CleanupDirectoryPlan{}, false, fmt.Errorf("cleanup directory target is not a real directory: %s", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return CleanupDirectoryPlan{}, false, err
	}
	expected := make(map[string]bool, len(spec.ExpectedFiles))
	for _, name := range spec.ExpectedFiles {
		if filepath.Base(name) != name || name == "." || name == ".." {
			return CleanupDirectoryPlan{}, false, fmt.Errorf("unsafe cleanup directory member %q", name)
		}
		expected[name] = true
	}
	if len(entries) != len(expected) {
		return CleanupDirectoryPlan{}, false, fmt.Errorf("cleanup directory inventory mismatch: %s has %d entries, expected exactly %d (%s)", path, len(entries), len(expected), strings.Join(spec.ExpectedFiles, ", "))
	}
	plan := CleanupDirectoryPlan{GameKey: spec.GameKey, RelativePath: spec.RelativePath}
	for _, entry := range entries {
		name := entry.Name()
		if !expected[name] {
			return CleanupDirectoryPlan{}, false, fmt.Errorf("cleanup directory contains unexpected entry: %s", filepath.Join(path, name))
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return CleanupDirectoryPlan{}, false, fmt.Errorf("cleanup directory member is not a regular file: %s", filepath.Join(path, name))
		}
		member := filepath.Join(path, name)
		memberInfo, err := entry.Info()
		if err != nil {
			return CleanupDirectoryPlan{}, false, err
		}
		if !memberInfo.Mode().IsRegular() {
			return CleanupDirectoryPlan{}, false, fmt.Errorf("cleanup directory member is not a regular file: %s", member)
		}
		sha, err := SHA256File(member)
		if err != nil {
			return CleanupDirectoryPlan{}, false, err
		}
		rel := filepath.ToSlash(filepath.Join(spec.RelativePath, name))
		f := CleanupManifestEntry{GameKey: spec.GameKey, RelativePath: rel, Size: memberInfo.Size(), SHA256: sha}
		plan.Files = append(plan.Files, f)
		plan.TotalBytes += f.Size
		(*done)++
		emitProgress(progress, ProgressEvent{Phase: "cleanup-preflight", Action: "CHECK", GameKey: spec.GameKey, RelativePath: rel, Detail: "Verified", Done: *done, Total: total})
	}
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].RelativePath < plan.Files[j].RelativePath })
	return plan, true, nil
}

func FormatCleanupPlan(plan CleanupPlan) string {
	count := len(plan.Files)
	for _, d := range plan.Directories {
		count += len(d.Files)
	}
	if count == 0 {
		return "English-dub purge: already clean\n"
	}
	return fmt.Sprintf("REMOVE  %d verified English-dub file(s), including %d whole known movie folder(s); reclaim %s\n", count, len(plan.Directories), FormatBytes(plan.TotalBytes))
}

func FormatBytes(n int64) string {
	const gib = 1024 * 1024 * 1024
	const mib = 1024 * 1024
	if n >= gib {
		return fmt.Sprintf("%.2f GiB", float64(n)/float64(gib))
	}
	if n >= mib {
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	}
	return fmt.Sprintf("%d B", n)
}

type cleanupRename struct {
	from string
	to   string
}

// RemoveEnglishAssets atomically deactivates all selected dub targets by
// renaming them out of their live game paths only after a full revalidation.
// If any rename fails, prior renames are rolled back. Once all live paths are
// gone, the renamed copies are permanently deleted. They are never placed in
// the optional original-file backup: selecting purge means the dub assets are
// intentionally not retained by this updater. Steam verification remains the
// external way to reacquire shipped language assets.
func RemoveEnglishAssets(plan CleanupPlan, roots map[string]string, receiptPath string) (CleanupReceipt, error) {
	return RemoveEnglishAssetsWithProgress(plan, roots, receiptPath, nil)
}

func RemoveEnglishAssetsWithProgress(plan CleanupPlan, roots map[string]string, receiptPath string, progress ProgressFunc) (CleanupReceipt, error) {
	receipt := CleanupReceipt{SchemaVersion: 2, RemovedAt: time.Now().UTC().Format(time.RFC3339)}
	fresh, err := BuildCleanupPlan(roots)
	if err != nil {
		return receipt, err
	}
	if !sameCleanupPlan(plan, fresh) {
		return receipt, fmt.Errorf("cleanup preflight changed since plan; nothing was removed")
	}

	tx := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	var renamed []cleanupRename
	totalUnits := len(plan.Files)
	for _, d := range plan.Directories {
		totalUnits += len(d.Files)
	}
	doneUnits := 0
	emitProgress(progress, ProgressEvent{Phase: "cleanup", Detail: "Ready", Done: doneUnits, Total: totalUnits})
	rollback := func(cause error) (CleanupReceipt, error) {
		for i := len(renamed) - 1; i >= 0; i-- {
			_ = os.Rename(renamed[i].to, renamed[i].from)
		}
		return CleanupReceipt{}, fmt.Errorf("cleanup failed before commit and rollback was attempted: %w", cause)
	}

	for _, f := range plan.Files {
		path := filepath.Join(roots[f.GameKey], filepath.FromSlash(f.RelativePath))
		tmp := path + ".unburger-purge." + tx
		if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
			if err == nil {
				return rollback(fmt.Errorf("temporary cleanup path already exists: %s", tmp))
			}
			return rollback(err)
		}
		if err := os.Rename(path, tmp); err != nil {
			return rollback(fmt.Errorf("deactivate %s: %w", path, err))
		}
		renamed = append(renamed, cleanupRename{from: path, to: tmp})
		doneUnits++
		emitProgress(progress, ProgressEvent{Phase: "cleanup", Action: "REMOVE", GameKey: f.GameKey, RelativePath: f.RelativePath, Detail: "Deactivated", Done: doneUnits, Total: totalUnits})
	}
	for _, d := range plan.Directories {
		path := filepath.Join(roots[d.GameKey], filepath.FromSlash(d.RelativePath))
		tmp := path + ".unburger-purge." + tx
		if _, err := os.Lstat(tmp); !os.IsNotExist(err) {
			if err == nil {
				return rollback(fmt.Errorf("temporary cleanup path already exists: %s", tmp))
			}
			return rollback(err)
		}
		if err := os.Rename(path, tmp); err != nil {
			return rollback(fmt.Errorf("deactivate %s: %w", path, err))
		}
		renamed = append(renamed, cleanupRename{from: path, to: tmp})
		doneUnits += len(d.Files)
		emitProgress(progress, ProgressEvent{Phase: "cleanup", Action: "REMOVE", GameKey: d.GameKey, RelativePath: d.RelativePath, Detail: "Deactivated verified folder", Done: doneUnits, Total: totalUnits})
	}

	// Commit: live game paths are now free of known dub assets. Destruction is
	// deliberate and no persistent backup is kept.
	for _, r := range renamed {
		if err := os.RemoveAll(r.to); err != nil {
			return receipt, fmt.Errorf("dub paths were deactivated but permanent deletion failed for %s: %w", r.to, err)
		}
	}
	for _, f := range plan.Files {
		receipt.Files = append(receipt.Files, f)
		receipt.TotalBytes += f.Size
	}
	for _, d := range plan.Directories {
		receipt.Directories = append(receipt.Directories, d.RelativePath)
		for _, f := range d.Files {
			receipt.Files = append(receipt.Files, f)
			receipt.TotalBytes += f.Size
		}
	}
	if receiptPath != "" {
		if err := writeCleanupReceipt(receiptPath, receipt); err != nil {
			return receipt, err
		}
	}
	emitProgress(progress, ProgressEvent{Phase: "cleanup", Detail: "Committed", Done: totalUnits, Total: totalUnits})
	return receipt, nil
}

func sameCleanupPlan(a, b CleanupPlan) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return string(ra) == string(rb)
}

func writeCleanupReceipt(path string, receipt CleanupReceipt) error {
	if path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}
