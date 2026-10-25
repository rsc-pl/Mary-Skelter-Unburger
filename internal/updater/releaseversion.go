package updater

import (
	"fmt"
	"regexp"
)

var releaseVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

func ValidateReleaseVersion(version string) error {
	if !releaseVersionPattern.MatchString(version) || version == "." || version == ".." {
		return fmt.Errorf("invalid release version %q", version)
	}
	return nil
}
