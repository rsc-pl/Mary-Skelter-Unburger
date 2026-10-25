package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// RunReceipt records one completed install/repair session independently of the
// optional persistent original-file backup. It is deliberately small: the
// authoritative state remains the installed files plus the authenticated v3
// container, while the receipt explains what this run actually changed.
type RunReceipt struct {
	SchemaVersion    int               `json:"schema_version"`
	Mode             string            `json:"mode"`
	UpdaterVersion   string            `json:"updater_version"`
	CompletedAt      string            `json:"completed_at"`
	AssetContainerID string            `json:"asset_container_id"`
	AssetObjectCount int               `json:"asset_object_count"`
	Games            map[string]string `json:"games"`
	Patch            Receipt           `json:"patch"`
	CleanupRequested bool              `json:"cleanup_requested"`
	Cleanup          *CleanupReceipt   `json:"cleanup,omitempty"`
	CleanupError     string            `json:"cleanup_error,omitempty"`
	PersistentBackup string            `json:"persistent_backup,omitempty"`
}

func NewRunReceipt(mode, updaterVersion string, assets *ReleaseAssets, roots map[string]string, patch Receipt, cleanupRequested bool, cleanup CleanupReceipt, cleanupErr error, backup string) RunReceipt {
	games := make(map[string]string, len(roots))
	for k, v := range roots {
		games[k] = v
	}
	r := RunReceipt{
		SchemaVersion:    1,
		Mode:             mode,
		UpdaterVersion:   updaterVersion,
		CompletedAt:      time.Now().UTC().Format(time.RFC3339),
		Games:            games,
		Patch:            patch,
		CleanupRequested: cleanupRequested,
		PersistentBackup: backup,
	}
	if assets != nil && assets.Reader != nil {
		r.AssetContainerID = assets.Reader.Manifest.ContainerID
		r.AssetObjectCount = len(assets.Reader.Manifest.Objects)
	}
	if cleanupRequested {
		c := cleanup
		r.Cleanup = &c
	}
	if cleanupErr != nil {
		r.CleanupError = cleanupErr.Error()
	}
	return r
}

func receiptBaseDir() (string, error) {
	if runtime.GOOS == "windows" {
		if p := os.Getenv("LOCALAPPDATA"); p != "" {
			return filepath.Join(p, "MarySkelterUnburger", "receipts"), nil
		}
		p, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(p, "MarySkelterUnburger", "receipts"), nil
	}
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return filepath.Join(p, "mary-skelter-unburger", "receipts"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "mary-skelter-unburger", "receipts"), nil
}

func DefaultRunReceiptPath(mode string) (string, error) {
	base, err := receiptBaseDir()
	if err != nil {
		return "", err
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "repair" {
		mode = "install"
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	return filepath.Join(base, stamp+"-"+mode+".json"), nil
}

func WriteRunReceipt(path string, receipt RunReceipt) error {
	if path == "" {
		return fmt.Errorf("receipt path is empty")
	}
	raw, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func ReceiptOperationCounts(r Receipt) (critical int, remuxed int) {
	for _, op := range r.Operations {
		if op.Action == "REMUX" {
			remuxed++
		} else {
			critical++
		}
	}
	return critical, remuxed
}

func FormatRunSummary(r Receipt, cleanupRequested bool, cleanup CleanupReceipt, cleanupErr error) string {
	critical, remuxed := ReceiptOperationCounts(r)
	movieFailures := 0
	for _, f := range r.SoftFailures {
		if f.Action == "REMUX" {
			movieFailures++
		}
	}
	var b strings.Builder
	if critical == 0 && remuxed == 0 && movieFailures == 0 {
		b.WriteString("Managed patch state: already current\n")
	} else {
		fmt.Fprintf(&b, "Managed patch: %d critical operation(s) applied\n", critical)
		fmt.Fprintf(&b, "Finale movies: %d remuxed, %d unresolved\n", remuxed, movieFailures)
	}
	if cleanupRequested {
		switch {
		case cleanupErr != nil:
			fmt.Fprintf(&b, "English-dub cleanup: WARNING: %v\n", cleanupErr)
		case len(cleanup.Files) == 0:
			b.WriteString("English-dub cleanup: already clean\n")
		default:
			fmt.Fprintf(&b, "English-dub cleanup: %d file(s), %s reclaimed\n", len(cleanup.Files), FormatBytes(cleanup.TotalBytes))
		}
	} else {
		b.WriteString("English-dub cleanup: kept by user choice\n")
	}
	return b.String()
}

// SortedRunGames is used only for stable human-facing diagnostics/tests.
func SortedRunGames(r RunReceipt) []string {
	keys := make([]string, 0, len(r.Games))
	for k := range r.Games {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
