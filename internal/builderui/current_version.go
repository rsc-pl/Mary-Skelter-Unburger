package builderui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"maryskelter-unburger/internal/updater"
)

// refreshReleaseVersion reads the project release identity when the user
// actually selects an action. The interactive menu may remain open while the
// version file changes; its initial banner is not a release version pin.
func refreshReleaseVersion(o Options) (Options, error) {
	data, err := os.ReadFile(filepath.Join(o.Root, "version.txt"))
	if err != nil {
		return o, fmt.Errorf("read release version: %w", err)
	}
	v := strings.TrimPrefix(strings.TrimSpace(string(data)), "v")
	if err := updater.ValidateReleaseVersion(v); err != nil {
		return o, err
	}
	o.Version = v
	return o, nil
}

// requireCurrentReleaseVersion prevents the PC and Switch builds from
// publishing different versions if version.txt changes mid-workflow.
func requireCurrentReleaseVersion(o Options) error {
	current, err := refreshReleaseVersion(o)
	if err != nil {
		return err
	}
	if current.Version != o.Version {
		return fmt.Errorf("version.txt changed during this release action (%s -> %s); start the action again before building", o.Version, current.Version)
	}
	return nil
}
