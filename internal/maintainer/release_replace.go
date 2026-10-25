package maintainer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"maryskelter-unburger/internal/updater"
)

// ExistingReleasePackages is a read-only preflight for the named release ZIPs
// and adjacent checksums. This never touches source caches or game files.
func ExistingReleasePackages(root, version string, pc, atmosphere, eden bool) ([]string, error) {
	if version == "" {
		raw, err := os.ReadFile(filepath.Join(root, "version.txt"))
		if err != nil {
			return nil, err
		}
		version = strings.TrimSpace(string(raw))
	}
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if err := updater.ValidateReleaseVersion(version); err != nil {
		return nil, err
	}
	names := []string{}
	if pc {
		names = append(names, "Mary-Skelter-2-Finale-UNBURGER-PC-v"+version+".zip")
	}
	if atmosphere {
		names = append(names, "Mary-Skelter-2-Finale-UNBURGER-Switch-Atmosphere-v"+version+".zip")
	}
	if eden {
		names = append(names, "Mary-Skelter-2-Finale-UNBURGER-Switch-Eden-v"+version+".zip")
	}
	dir := filepath.Join(root, ".local", "release", "packages")
	out := make([]string, 0, 2*len(names))
	for _, name := range names {
		for _, suffix := range []string{"", ".sha256"} {
			path := filepath.Join(dir, name+suffix)
			st, err := os.Lstat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("refuse nonregular release artifact: %s", path)
			}
			out = append(out, path)
		}
	}
	return out, nil
}

// WithReleaseReplacement explicitly overwrites only release archives the user
// selected. Existing ZIPs are first moved, without copying, into a private
// recovery directory. Build failures restore exactly those files; successful
// builds retain the previous release in .local/release/replaced-packages.
// Caller must obtain an explicit overwrite decision before setting approved.
func WithReleaseReplacement(root, version string, pc, atmosphere, eden, approved bool, build func() error) (err error) {
	existing, err := ExistingReleasePackages(root, version, pc, atmosphere, eden)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		return build()
	}
	if !approved {
		return fmt.Errorf("release ZIP already exists; select Overwrite existing release in the builder before rebuilding: %s", existing[0])
	}
	dir := filepath.Join(root, ".local", "release", "packages")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	lock := filepath.Join(dir, ".release-replacement.lock")
	lf, e := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return fmt.Errorf("another release replacement may be running: %w", e)
	}
	_ = lf.Close()
	defer os.Remove(lock)
	// Reject a changed set, e.g. an artifact inserted during confirmation.
	again, e := ExistingReleasePackages(root, version, pc, atmosphere, eden)
	if e != nil {
		return e
	}
	if len(existing) != len(again) {
		return errors.New("release artifact set changed during confirmation")
	}
	for i := range existing {
		if again[i] != existing[i] {
			return errors.New("release artifact set changed during confirmation")
		}
	}
	base := filepath.Join(root, ".local", "release", "replaced-packages")
	if err = os.MkdirAll(base, 0700); err != nil {
		return err
	}
	backup, e := os.MkdirTemp(base, "previous-"+time.Now().Format("20060102-150405")+"-*")
	if e != nil {
		return e
	}
	moved := []string{}
	initial := map[string]bool{}
	for _, p := range existing {
		initial[p] = true
	}
	restore := func(cause error) error {
		problems := []error{cause}
		for _, path := range moved {
			// Only remove a regular replacement that this invocation could have published.
			if st, e := os.Lstat(path); e == nil {
				if !st.Mode().IsRegular() {
					problems = append(problems, fmt.Errorf("unexpected nonregular new release %s", path))
					continue
				}
				if e = os.Remove(path); e != nil {
					problems = append(problems, e)
					continue
				}
			} else if !os.IsNotExist(e) {
				problems = append(problems, e)
				continue
			}
			if e := os.Rename(filepath.Join(backup, filepath.Base(path)), path); e != nil {
				problems = append(problems, e)
			}
		}
		// Also remove newly published files (ZIP / SHA) where no previous file existed.
		// They are produced only at these strictly enumerated release names.
		cleanNames := []string{}
		if pc {
			cleanNames = append(cleanNames, "Mary-Skelter-2-Finale-UNBURGER-PC-v")
		}
		if atmosphere {
			cleanNames = append(cleanNames, "Mary-Skelter-2-Finale-UNBURGER-Switch-Atmosphere-v")
		}
		if eden {
			cleanNames = append(cleanNames, "Mary-Skelter-2-Finale-UNBURGER-Switch-Eden-v")
		}
		v := version
		if v == "" {
			b, _ := os.ReadFile(filepath.Join(root, "version.txt"))
			v = strings.TrimSpace(string(b))
		}
		v = strings.TrimPrefix(v, "v")
		preserved := map[string]bool{}
		for _, p := range moved {
			preserved[p] = true
		}
		for _, prefix := range cleanNames {
			for _, suffix := range []string{".zip", ".zip.sha256"} {
				p := filepath.Join(dir, prefix+v+suffix)
				if preserved[p] || initial[p] {
					continue
				}
				if st, e := os.Lstat(p); e == nil && st.Mode().IsRegular() {
					if e = os.Remove(p); e != nil {
						problems = append(problems, e)
					}
				}
			}
		}
		return errors.Join(problems...)
	}
	for _, p := range existing {
		// Revalidate immediately before renaming; no links, no directories.
		st, e := os.Lstat(p)
		if e != nil || !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
			return restore(fmt.Errorf("release artifact changed during replacement: %s", p))
		}
		if e = os.Rename(p, filepath.Join(backup, filepath.Base(p))); e != nil {
			return restore(e)
		}
		moved = append(moved, p)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = restore(fmt.Errorf("panic during release build: %v", p))
			panic(p)
		}
	}()
	if err = build(); err != nil {
		return restore(err)
	}
	// The build must have published exactly one ZIP and one checksum for
	// every requested variant. Incomplete success is rolled back as a failure.
	v := version
	if v == "" {
		b, readErr := os.ReadFile(filepath.Join(root, "version.txt"))
		if readErr != nil {
			return restore(readErr)
		}
		v = strings.TrimSpace(string(b))
	}
	v = strings.TrimPrefix(v, "v")
	variants := []string{}
	if pc {
		variants = append(variants, "Mary-Skelter-2-Finale-UNBURGER-PC-v"+v)
	}
	if atmosphere {
		variants = append(variants, "Mary-Skelter-2-Finale-UNBURGER-Switch-Atmosphere-v"+v)
	}
	if eden {
		variants = append(variants, "Mary-Skelter-2-Finale-UNBURGER-Switch-Eden-v"+v)
	}
	for _, name := range variants {
		for _, suffix := range []string{".zip", ".zip.sha256"} {
			p := filepath.Join(dir, name+suffix)
			st, checkErr := os.Lstat(p)
			if checkErr != nil {
				return restore(fmt.Errorf("incomplete release output %s: %w", p, checkErr))
			}
			if !st.Mode().IsRegular() || st.Mode()&os.ModeSymlink != 0 {
				return restore(fmt.Errorf("invalid release output %s", p))
			}
		}
	}
	// The previous release remains available without duplicating its bytes.
	fmt.Fprintf(os.Stdout, "Replaced prior release archives; preserved originals in %s\n", backup)
	return nil
}
