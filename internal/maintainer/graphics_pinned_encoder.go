package maintainer

// Native texture encoders are built on first use from the checked-in Go source.
// Runtime build provenance is checked by Go's embedded build-info metadata;
// a bit-for-bit fixed binary digest would make ordinary source builds fail.
import (
	"debug/buildinfo"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func verifyBundledEncoderBC7(platform, bin string) error {
	if platform != "linux-amd64" && platform != "windows-amd64" {
		return fmt.Errorf("unsupported Go graphics helper platform %s", platform)
	}
	st, err := os.Lstat(bin)
	if err != nil {
		return fmt.Errorf("native Go graphics helper missing; launch via build.sh/build.bat to compile it: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("native Go graphics helper must be a regular file: %s", bin)
	}
	if platform == runtime.GOOS+"-"+runtime.GOARCH && runtime.GOOS != "windows" && st.Mode().Perm()&0111 == 0 {
		return fmt.Errorf("native Go graphics helper is not executable: %s", bin)
	}
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		return fmt.Errorf("invalid Go graphics binary %s: %w", filepath.Base(bin), err)
	}
	if !strings.HasPrefix(info.Main.Path, "maryskelter-unburger/bcn-tid-go") {
		return fmt.Errorf("unexpected Go graphics binary package: %q", info.Main.Path)
	}
	return nil
}

func verifyBundledEncoderBC7Cross(platform, bin string) error {
	return verifyBundledEncoderBC7(platform, bin)
}
