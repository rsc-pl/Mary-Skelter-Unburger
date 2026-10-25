package switchrelease

import (
	"fmt"
	"os"
	"path/filepath"
)

// preserveCanonicalGraphics retains the SHA-verified canonical graphics input
// as an immutable, content-addressed local artifact for an EXPLICIT future
// reuse decision. Cache reuse is never automatic: changes to fonts, sources,
// graphics recipes or renderer invalidate the user's previous approval.
func preserveCanonicalGraphics(root, canonical string) (string, string, error) {
	sum, err := sumFile(canonical)
	if err != nil {
		return "", "", err
	}
	cache := filepath.Join(root, ".local", "switch_release", "graphics_cache")
	for _, parent := range []string{root, filepath.Join(root, ".local"), filepath.Join(root, ".local", "switch_release"), cache} {
		st, e := os.Lstat(parent)
		if os.IsNotExist(e) {
			if e = os.Mkdir(parent, 0755); e != nil {
				return "", "", e
			}
			st, e = os.Lstat(parent)
		}
		if e != nil {
			return "", "", e
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("refuse linked/non-directory graphics cache parent %s", parent)
		}
	}
	dst := filepath.Join(cache, "canonical-"+sum+".zip")
	if err := os.Link(canonical, dst); err != nil {
		if !os.IsExist(err) {
			return "", "", err
		}
		st, e := os.Lstat(dst)
		if e != nil || !st.Mode().IsRegular() {
			return "", "", fmt.Errorf("refuse linked/nonregular graphics cache collision %s", dst)
		}
		got, e := sumFile(dst)
		if e != nil || got != sum {
			return "", "", fmt.Errorf("refuse graphics cache SHA mismatch %s", dst)
		}
	}
	return dst, sum, nil
}
