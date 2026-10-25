package maintainer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeGraphicsHelperWhenPresent(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ platform, name string }{
		{"linux-amd64", "bcn-tid-go"}, {"windows-amd64", "bcn-tid-go.exe"},
	} {
		path := filepath.Join(root, "toolchain", "bin", item.platform, item.name)
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		if err := verifyBundledEncoderBC7Cross(item.platform, path); err != nil {
			t.Fatal(err)
		}
	}
}
