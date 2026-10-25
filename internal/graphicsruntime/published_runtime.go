package graphicsruntime

// Public Python runtimes are installed ONCE into toolchain/python-runtime and
// are delivered independently of the source distribution. Their manifests
// must match the exact, owner-assembled CPython 3.13.15 / Pillow 12.3.0 /
// NumPy 2.3.5 vendor outputs before the Go production graphics builder runs.
import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

var publishedRuntimeManifests = map[string]string{
	"linux-amd64":   "b9b4ff834b5e6463f938a7843fcb7407db729f7671b07e7bc0ff46ada06ffeb7",
	"windows-amd64": "8161a9e3f7c1fec324586a95b8d19a678e8d1cdc316578a1417a0263a1e087cb",
}

// VerifyPublishedRuntimeManifest is a fast immutable version gate before
// ResolvePlatform's complete SHA inventory and Smoke's native extensions QA.
// No files are modified, installed, copied, or downloaded.
func VerifyPublishedRuntimeManifest(root, goos, goarch string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("runtime verification requires absolute project root")
	}
	platform := goos + "-" + goarch
	want, ok := publishedRuntimeManifests[platform]
	if !ok {
		return fmt.Errorf("unsupported pinned Python runtime platform %s", platform)
	}
	p := filepath.Join(root, "toolchain", "python-runtime", platform, "manifest.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("required public Python runtime %s unavailable: %w", platform, err)
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if got != want {
		return fmt.Errorf("public Python %s manifest SHA mismatch: got %s expected %s", platform, got, want)
	}
	return nil
}
