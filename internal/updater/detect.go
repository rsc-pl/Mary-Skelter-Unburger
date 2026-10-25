package updater

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var vdfPathRE = regexp.MustCompile(`(?i)"path"\s+"([^"]+)"`)

// DetectGames searches configured and common Steam library locations for supported games.
func DetectGames() map[string][]string {
	commons := steamCommonDirs()
	out := map[string][]string{"ms2": {}, "finale": {}}
	seen := map[string]bool{}
	for _, common := range commons {
		entries, err := os.ReadDir(common)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			root := filepath.Join(common, e.Name())
			for key, p := range Profiles {
				if _, err := os.Stat(filepath.Join(root, p.ExeName)); err != nil {
					continue
				}
				if info, err := os.Stat(filepath.Join(root, "masterWindows")); err != nil || !info.IsDir() {
					continue
				}
				abs, _ := filepath.Abs(root)
				id := key + "\x00" + abs
				if !seen[id] {
					seen[id] = true
					out[key] = append(out[key], abs)
				}
			}
		}
	}
	for key := range out {
		sort.Strings(out[key])
	}
	return out
}

func steamCommonDirs() []string {
	roots := []string{}
	if v := os.Getenv("MARY_SKELTER_STEAM_DIR"); v != "" {
		// An explicit Steam root is an override, not an additional search path.
		// This also keeps scripted/noninteractive runs deterministic.
		roots = append(roots, v)
	} else {
		home, _ := os.UserHomeDir()
		if runtime.GOOS == "windows" {
			if v := os.Getenv("ProgramFiles(x86)"); v != "" {
				roots = append(roots, filepath.Join(v, "Steam"))
			}
			if v := os.Getenv("ProgramFiles"); v != "" {
				roots = append(roots, filepath.Join(v, "Steam"))
			}
		} else if home != "" {
			roots = append(roots,
				filepath.Join(home, ".steam", "steam"),
				filepath.Join(home, ".local", "share", "Steam"),
				filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", "data", "Steam"),
			)
		}
	}
	seen := map[string]bool{}
	commons := []string{}
	add := func(p string) {
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			commons = append(commons, p)
		}
	}
	for _, root := range roots {
		add(filepath.Join(root, "steamapps", "common"))
		raw, err := os.ReadFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"))
		if err != nil {
			continue
		}
		for _, m := range vdfPathRE.FindAllStringSubmatch(string(raw), -1) {
			lib := strings.ReplaceAll(m[1], `\\`, `\`)
			add(filepath.Join(lib, "steamapps", "common"))
		}
	}
	return commons
}
