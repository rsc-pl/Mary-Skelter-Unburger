package maintainer

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// VerifyNativeGo is an explicit, non-legacy verification route. It never
// directly invokes Python scripts, even if a native gate fails. Individual
// historical Go test cases may still invoke independent Python QA oracles. Quick mode is source-independent
// and deliberately does NOT claim game-source/archive parity. Full mode requires
// the owner's canonical pristine source cache and checks the translation bridge.
// Historical Python regression suites are not claimed to have Go equivalents.
func VerifyNativeGo(root string, quick bool, stdout, stderr io.Writer) error {
	log := func(format string, args ...any) {
		if stdout != nil {
			fmt.Fprintf(stdout, format+"\n", args...)
		}
	}
	runGo := func(args ...string) error {
		cmd := exec.Command("go", args...)
		cmd.Dir = root
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("native verifier: go %s: %w", strings.Join(args, " "), err)
		}
		return nil
	}
	log("[native-go 1/7] Go tests and vet (historical Go tests may use Python QA oracles)")
	if err := runGo("test", "-p", "1", "./...", "-count=1"); err != nil {
		return err
	}
	if err := runGo("vet", "./..."); err != nil {
		return err
	}
	log("[native-go 2/7] Native text dataset surface")
	if err := verifyNativeTextDatasetSurface(); err != nil {
		return err
	}
	log("[native-go 3/7] Canonical title, translations and graphics")
	if err := PreflightTitleFooter(root, stdout); err != nil {
		return err
	}
	if _, err := ValidateTranslations(root, stdout); err != nil {
		return err
	}
	if err := VerifyGraphicsConfigNative(root, false); err != nil {
		return err
	}
	log("[native-go 4/7] Pristine source cache / translation bridge")
	if !quick {
		cache, err := EnsureSourceCache(root, stdout, stderr)
		if err != nil {
			return fmt.Errorf("native verifier: pristine PC sources required: %w", err)
		}
		// This is the established source-bound record bridge. Never interpret it
		// as independent whole-original-master parity or as a game runtime PASS.
		if err := prepareSourceRecords(root, cache); err != nil {
			return err
		}
		if _, err := BuildRecords(root, stdout); err != nil {
			return err
		}
		if err := validateChangeManifestCanonical(root); err != nil {
			return err
		}
	} else {
		log("SOURCE PARITY NOT CHECKED: --quick skips private source cache and translation-to-record bridge")
	}
	log("[native-go 5/7] Public tree hygiene (strict, never suppressed)")
	if err := CheckRepoHygieneNative(root); err != nil {
		return err
	}
	log("[native-go 6/7] Non-Python tool dependencies")
	if missing := MissingTools([]string{"go", "ffmpeg", "ffprobe", "zstd"}); len(missing) != 0 {
		return fmt.Errorf("native verifier missing required tools: %s", strings.Join(missing, ", "))
	}
	log("[native-go 7/7] Scoped result")
	if quick {
		log("NATIVE GO QUICK VERIFY: PASS (source and historical Python regressions NOT tested)")
	} else {
		log("NATIVE GO VERIFY: PASS (source bridge, not full master DAT/game/runtime parity)")
	}
	return nil
}

func verifyNativeTextDatasetSurface() error {
	for _, name := range []string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"} {
		if _, err := sourceDatasetInfoFor(name); err != nil {
			return fmt.Errorf("canonical dataset %s: %w", name, err)
		}
	}
	if _, err := sourceDatasetInfoFor("MSF_PC_KO"); err == nil {
		return fmt.Errorf("forbidden Korean dataset is exposed")
	}
	return nil
}
