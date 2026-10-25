package maintainer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"maryskelter-unburger/internal/graphicsruntime"
	"maryskelter-unburger/internal/updater"
)

type CachedSources struct {
	Root         string
	MS2Master    string
	FinaleMaster string
	MS2Exe       string
	FinaleExe    string
	Records      string
}

// pythonTool uses only the authenticated, project-owned CPython runtime.
// The host's python3, pip, virtual environments and PYTHONPATH are never used.
func pythonTool(root string, args ...string) (*exec.Cmd, error) {
	py, err := graphicsruntime.Resolve(root)
	if err != nil {
		return nil, fmt.Errorf("portable Python unavailable: %w", err)
	}
	return py.ToolCommand(root, args...)
}

func runEnv(root string, stdout, stderr io.Writer, stdin io.Reader, env []string, name string, args ...string) error {
	var cmd *exec.Cmd
	var err error
	if name == "python3" {
		cmd, err = pythonTool(root, args...)
	} else {
		cmd = exec.Command(name, args...)
	}
	if err != nil {
		return err
	}
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, stdin
	if name != "python3" {
		cmd.Env = append(os.Environ(), env...)
	} else {
		cmd.Env = append(cmd.Env, env...)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func commandOutput(root string, env []string, name string, args ...string) ([]byte, error) {
	var cmd *exec.Cmd
	var err error
	if name == "python3" {
		cmd, err = pythonTool(root, args...)
	} else {
		cmd = exec.Command(name, args...)
	}
	if err != nil {
		return nil, err
	}
	cmd.Dir = root
	if name != "python3" {
		cmd.Env = append(os.Environ(), env...)
	} else {
		cmd.Env = append(cmd.Env, env...)
	}
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

func cachedSources(root string) CachedSources {
	return cachedSourcesAt(filepath.Join(root, ".local", "source_cache"))
}

func (c CachedSources) env() []string {
	e := []string{
		"MS2_PC_MASTER=" + c.MS2Master,
		"MSF_PC_MASTER=" + c.FinaleMaster,
		"MS2_PC_EXE=" + c.MS2Exe,
		"MARY_SKELTER_SOURCE_RECORD_CACHE=" + c.Records,
		"MARY_SKELTER_SOURCE_CACHE=" + c.Root,
		"MARY_SKELTER_SOURCE_CACHE_READY=1",
	}
	if file(c.FinaleExe) {
		e = append(e, "MSF_PC_EXE="+c.FinaleExe, "FINALE_PC_EXE="+c.FinaleExe)
	}
	return e
}

func EnsureSourceCache(root string, stdout, stderr io.Writer) (CachedSources, error) {
	cfg, err := ReadSourcePaths(root)
	if err != nil {
		return CachedSources{}, err
	}
	return ensureSourceCacheNative(root, cfg, stdout, stderr)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, st.Mode().Perm())
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	cerr := out.Close()
	if err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// prepareSourceRecords stages all three canonical record inputs before changing
// .local/build/source_records. A missing/corrupt source or an unexpected owner
// file in the destination must never wipe the previously usable record set.
// The only removable files are the three known generated JSONL records.
func prepareSourceRecords(root string, c CachedSources) error {
	return prepareSourceRecordsWithHook(root, c, nil)
}

// beforePublish is test-only fault injection. Production always passes nil.
func prepareSourceRecordsWithHook(root string, c CachedSources, beforePublish func() error) error {
	build := filepath.Join(root, ".local", "build")
	for _, d := range []string{filepath.Join(root, ".local"), build} {
		st, err := os.Lstat(d)
		if err == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("refuse linked/non-directory build path %s", d)
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(build, 0755); err != nil {
		return err
	}
	const datasetCount = 3
	datasets := [...]string{"MS2_PC_LOCALIZED", "MSF_PC_LOCALIZED", "MSF_PC_SYSTEM"}
	// Validate ALL sources before touching any previously published output.
	sources := make([]string, 0, datasetCount)
	for _, ds := range datasets {
		src := filepath.Join(c.Records, ds+".jsonl")
		st, err := os.Lstat(src)
		if err != nil {
			return fmt.Errorf("missing cached source records %s: %w", src, err)
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("nonregular/symlink source records %s", src)
		}
		sources = append(sources, src)
	}
	out := filepath.Join(build, "source_records")
	old, err := os.Lstat(out)
	hadOld := err == nil
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if hadOld {
		if !old.IsDir() || old.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse linked/non-directory source_records %s", out)
		}
		entries, err := os.ReadDir(out)
		if err != nil {
			return err
		}
		known := map[string]bool{}
		for _, ds := range datasets {
			known[ds+".jsonl"] = true
		}
		for _, e := range entries {
			if !known[e.Name()] || !e.Type().IsRegular() {
				return fmt.Errorf("refuse to replace source_records containing noncanonical or linked entry %s", e.Name())
			}
		}
	}
	stage, err := os.MkdirTemp(build, ".source-records-new-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for i, ds := range datasets {
		dst := filepath.Join(stage, ds+".jsonl")
		if err := os.Link(sources[i], dst); err != nil {
			if err := copyFile(sources[i], dst); err != nil {
				return fmt.Errorf("stage %s: %w", ds, err)
			}
		}
	}
	// Unique private backup is created first. When publication fails the old
	// directory can be atomically restored; never delete it on a failed swap.
	backup, err := os.MkdirTemp(build, ".source-records-previous-*")
	if err != nil {
		return err
	}
	retainBackup := false
	defer func() {
		if !retainBackup {
			_ = os.RemoveAll(backup)
		}
	}()
	prior := filepath.Join(backup, "prior")
	if hadOld {
		if err := os.Rename(out, prior); err != nil {
			return fmt.Errorf("preserve previous source records: %w", err)
		}
	}
	var publishErr error
	if beforePublish != nil {
		publishErr = beforePublish()
	}
	if publishErr == nil {
		publishErr = os.Rename(stage, out)
	}
	if err := publishErr; err != nil {
		if hadOld {
			if restoreErr := os.Rename(prior, out); restoreErr != nil {
				// Keep both paths for manual rescue: never remove backup.
				retainBackup = true
				return fmt.Errorf("publish source_records: %v; RESTORE FAILED: %v; prior=%s", err, restoreErr, prior)
			}
		}
		return fmt.Errorf("publish source_records: %w", err)
	}
	// Any old directory contains only known generated files after validation.
	return nil
}

type changeInfo struct {
	ChangedFields int `json:"changed_fields"`
}

func changedDatasets(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]changeInfo
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := []string{}
	for ds, inf := range m {
		if inf.ChangedFields > 0 {
			out = append(out, ds)
		}
	}
	sort.Strings(out)
	return out, nil
}

func reportArchives(path string) ([]int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r struct {
		Archives []struct {
			ArchiveNumber int `json:"archive_number"`
		} `json:"archives"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	out := make([]int, 0, len(r.Archives))
	seen := map[int]bool{}
	for _, a := range r.Archives {
		if !seen[a.ArchiveNumber] {
			seen[a.ArchiveNumber] = true
			out = append(out, a.ArchiveNumber)
		}
	}
	sort.Ints(out)
	return out, nil
}

func buildQoLExecutable(src, dst string, p updater.Profile) (string, error) {
	raw, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	patched, _, err := updater.PatchExecutable(raw, p)
	if err != nil {
		return "", err
	}
	// Re-applying must be a no-op and recognize the output as final.
	check, state, err := updater.PatchExecutable(patched, p)
	if err != nil || state != "final" || string(check) != string(patched) {
		if err == nil {
			err = fmt.Errorf("final-state verification returned %q", state)
		}
		return "", fmt.Errorf("%s QoL verification: %w", p.DisplayName, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, patched, 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return updater.SHA256File(dst)
}

func buildQoLBundles(root, distStage string, c CachedSources, stdout io.Writer) error {
	ms2 := updater.Profiles["ms2"]
	ms2Dir := filepath.Join(distStage, ms2.BundleDir)
	if !dir(ms2Dir) {
		return fmt.Errorf("MS2 patch bundle missing after canonical bundling: %s", ms2Dir)
	}
	ms2Out := filepath.Join(ms2Dir, ms2.ExeName)
	sha, err := buildQoLExecutable(c.MS2Exe, ms2Out, ms2)
	if err != nil {
		return err
	}
	ms2Install := "Back up the original game files first.\n\n1. Copy MarySkelter2.exe to the game root, replacing the original executable.\n   This verified executable skips the startup logo movies and bypasses the\n   voice-language prompt while using Japanese voices.\n2. Copy files from masterWindows/ over the same names in the game's\n   masterWindows directory. Only DATs changed by the verified rebuild are included.\n\nSave conversion is provided by the native MarySkelterUnburger client and is no longer duplicated as a Python tool in this bundle.\n"
	if err := os.WriteFile(filepath.Join(ms2Dir, "INSTALL.txt"), []byte(ms2Install), 0o644); err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "MS2 QoL EXE: %s\nSHA-256: %s\n", ms2Out, sha)
	}

	finale := updater.Profiles["finale"]
	if !file(c.FinaleExe) {
		if stdout != nil {
			fmt.Fprintf(stdout, "SKIP: %s executable not present in source cache\n", finale.ExeName)
		}
		return nil
	}
	fDir := filepath.Join(distStage, finale.BundleDir)
	if !dir(fDir) {
		return fmt.Errorf("Finale patch bundle missing after canonical bundling: %s", fDir)
	}
	fOut := filepath.Join(fDir, finale.ExeName)
	fsha, err := buildQoLExecutable(c.FinaleExe, fOut, finale)
	if err != nil {
		return err
	}
	fInstall := "Back up the original game files first.\n\n1. Copy MarySkelterFinale.exe to the game root, replacing the original executable.\n   This verified executable skips the startup creator logos, keeps the Japanese\n   VOICE resource variant, and does not mount the unused English sound archive.\n2. Copy files from masterWindows/ over the same names in the game's\n   masterWindows directory. Only DATs changed by the verified rebuild are included.\n   Canonical graphical-text fixes listed in PATCH_MANIFEST.json are already integrated\n   into these DATs.\n"
	if err := os.WriteFile(filepath.Join(fDir, "INSTALL.txt"), []byte(fInstall), 0o644); err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "Finale QoL EXE: %s\nSHA-256: %s\n", fOut, fsha)
	}
	return nil
}

func activateDist(root, stage string) error {
	return activateDistTransactional(root, stage, nil)
}

// The rollback slot must be newly allocated, not a fixed path that is
// unconditionally removed. The prior user-visible dist is restored on a
// failed publish. Unexpected content in either tree is never deleted.
func activateDistTransactional(root, stage string, fault func() error) error {
	build, err := safeBuildParent(root)
	if err != nil {
		return err
	}
	dist := filepath.Join(root, ".local", "dist")
	if err := checkGeneratedTree(stage, "dist.staging"); err != nil {
		return err
	}
	if err := checkGeneratedTree(dist, "dist.staging"); err != nil {
		return err
	}
	stageInfo, err := os.Lstat(stage)
	if err != nil {
		return err
	}
	if !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse invalid dist publication stage %s", stage)
	}
	// An unfinished old legacy dist.previous may be owner recovery material.
	// Never unlink, rewrite, or reuse it.
	old := filepath.Join(build, "dist.previous")
	if _, e := os.Lstat(old); e == nil {
		return fmt.Errorf("refuse existing dist.previous recovery path %s", old)
	} else if !os.IsNotExist(e) {
		return e
	}
	slot, err := os.MkdirTemp(build, ".dist-publish-")
	if err != nil {
		return err
	}
	previous := filepath.Join(slot, "prior")
	had := false
	if _, e := os.Lstat(dist); e == nil {
		if err = os.Rename(dist, previous); err != nil {
			_ = os.Remove(slot)
			return err
		}
		had = true
	} else if !os.IsNotExist(e) {
		_ = os.Remove(slot)
		return e
	}
	restore := func(cause error) error {
		if had {
			if e := os.Rename(previous, dist); e != nil {
				return fmt.Errorf("dist publish: %v; RESTORE FAILED: %v; prior preserved %s", cause, e, previous)
			}
		}
		if e := os.Remove(slot); e != nil {
			return fmt.Errorf("dist publish failed: %v; retained slot: %s: %w", cause, slot, e)
		}
		return fmt.Errorf("dist publish: %w", cause)
	}
	if fault != nil {
		if err = fault(); err != nil {
			return restore(err)
		}
	}
	if err = os.Rename(stage, dist); err != nil {
		return restore(err)
	}
	// Publication succeeded. The former dist only contained audited generated
	// patch bundles; do not replace it via os.RemoveAll of an unverified path.
	if had {
		if err = checkGeneratedTree(previous, "dist.staging"); err != nil {
			return fmt.Errorf("new dist published, prior retained at %s: %w", previous, err)
		}
	}
	if err = os.RemoveAll(slot); err != nil {
		return fmt.Errorf("new dist published, old verified bundle retained %s: %w", slot, err)
	}
	return nil
}

// preflightRebuildDependencies must run BEFORE EnsureSourceCache and the
// destructive .local/build scratch cleanup. It is a read-only dependency gate,
// not proof of a complete genuine master/game build. The detailed graphics
// builder revalidates the same dependencies when it reaches rasterization.
func preflightRebuildDependencies(root string) error {
	if !filepath.IsAbs(root) {
		return fmt.Errorf("rebuild requires an absolute project root")
	}
	if mode := os.Getenv("MS_BUNDLED_GRAPHICS_PYTHON"); mode == "0" {
		return fmt.Errorf("system Python graphics are not supported; public runtime required")
	} else if mode != "" && mode != "1" {
		return fmt.Errorf("MS_BUNDLED_GRAPHICS_PYTHON must be 0 or 1")
	}
	if mode := os.Getenv("MS_PC_GRAPHICS_NATIVE_BC7"); mode == "0" {
		return fmt.Errorf("native Go BCn is required for hermetic graphics build")
	} else if mode != "" && mode != "1" {
		return fmt.Errorf("MS_PC_GRAPHICS_NATIVE_BC7 must be 0 or 1")
	}
	py, err := graphicsruntime.Resolve(root)
	if err != nil {
		return fmt.Errorf("rebuild public graphics runtime preflight: %w", err)
	}
	if err := py.Smoke(); err != nil {
		return fmt.Errorf("rebuild graphics Python smoke preflight: %w", err)
	}
	encoder := os.Getenv("MS_PC_GRAPHICS_BCN_BINARY")
	if encoder == "" {
		name := "bcn-tid-go"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		encoder = filepath.Join(root, "toolchain", "bin", runtime.GOOS+"-"+runtime.GOARCH, name)
		if err := verifyBundledEncoderBC7(runtime.GOOS+"-"+runtime.GOARCH, encoder); err != nil {
			return fmt.Errorf("rebuild Go BCn preflight: %w", err)
		}
	} else if !filepath.IsAbs(encoder) {
		return fmt.Errorf("MS_PC_GRAPHICS_BCN_BINARY must be absolute")
	}
	st, err := os.Stat(encoder)
	if err != nil || !st.Mode().IsRegular() || (runtime.GOOS != "windows" && st.Mode().Perm()&0111 == 0) {
		return fmt.Errorf("rebuild Go BCn encoder unavailable or not executable: %s", encoder)
	}
	return nil
}

func Rebuild(root string, stdout, stderr io.Writer) error {
	// Preflight does not modify source records, output trees or owner masters.
	if err := preflightRebuildDependencies(root); err != nil {
		return err
	}
	// legacy: hold one rebuild lock through every writable phase, including
	// legacy/legacy records and legacy generated outputs / dist publication.
	return withExclusiveRebuild(root, func() error {
		return rebuildLocked(root, stdout, stderr)
	})
}

func rebuildLocked(root string, stdout, stderr io.Writer) error {
	c, err := EnsureSourceCache(root, stdout, stderr)
	if err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "[native-go] Preflight canonical title/footer and translations...")
	}
	if err := PreflightTitleFooter(root, stdout); err != nil {
		return err
	}
	if _, err := ValidateTranslations(root, stdout); err != nil {
		return err
	}

	// Pre-publish the validated record bridge before removing unrelated
	// generated scratch. A missing third input cannot destroy old reports.
	if err := prepareSourceRecords(root, c); err != nil {
		return err
	}
	// Keep previously usable build outputs until the entire edit-bridge has
	// been validated and atomically published. A malformed translation or
	// mismatched pristine source must not erase earlier reports or bundles.
	build := filepath.Join(root, ".local", "build")
	if _, err := BuildRecords(root, stdout); err != nil {
		return err
	}
	manifest := filepath.Join(build, "change_manifest.json")
	if err := validateChangeManifestCanonical(root); err != nil {
		return err
	}
	changed, err := changedDatasets(manifest)
	if err != nil {
		return err
	}
	if len(changed) > 0 {
		if err := CheckChangedSources(root, c, manifest, stdout); err != nil {
			return err
		}
	}
	// legacy: former generated output directories are moved to a bounded rollback
	// stage; neither owner scratch nor previous successful outputs are deleted
	// until a fully verified new bundle has been published.
	return withGeneratedOutputTransaction(root, func() error {
		if err := os.MkdirAll(filepath.Join(build, "rebuilt"), 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(build, "reports"), 0o755); err != nil {
			return err
		}
		env := c.env()
		tool := filepath.Join("toolchain", "python-src", "toolchain", "tools", "ms_text_tool.py")
		for _, ds := range changed {
			master := c.MS2Master
			if ds == "MSF_PC_LOCALIZED" || ds == "MSF_PC_SYSTEM" {
				master = c.FinaleMaster
			} else if ds != "MS2_PC_LOCALIZED" {
				return fmt.Errorf("unsupported target dataset %s", ds)
			}
			if stdout != nil {
				fmt.Fprintf(stdout, "=== REBUILD %s ===\n", ds)
			}
			records := filepath.Join(build, "records", ds+".jsonl")
			out := filepath.Join(build, "rebuilt", ds)
			rep := filepath.Join(build, "reports", ds+".rebuild.json")
			if os.Getenv("MS_PC_TEXT_NATIVE_GO") == "1" {
				if stdout != nil {
					fmt.Fprintf(stdout, "[native-go] EXPERIMENTAL native text rebuild: %s; full original-master/game QA still required\n", ds)
				}
				if _, err := RebuildPCTextDatasetNativeVerified(ds, master, records, out, rep,
					filepath.Join(root, "toolchain", "manifests", "expected_pc_archive_sha256.tsv"), true); err != nil {
					return err // Fail closed. NEVER silently fall back to legacy Python.
				}
			} else {
				if err := runEnv(root, stdout, stderr, os.Stdin, env, "python3", tool, "rebuild", "--dataset", ds, "--records", records, "--master", master, "--out", out, "--report", rep); err != nil {
					return err
				}
			}
			arcs, err := reportArchives(rep)
			if err != nil {
				return err
			}
			if len(arcs) == 0 {
				return fmt.Errorf("rebuild of %s reported no output archives", ds)
			}
			rebuilt := filepath.Join(out, "masterWindows")
			verifyReport := filepath.Join(build, "reports", ds+".verify.json")
			if _, err := VerifyTextDatasetGoStrict(ds, rebuilt, records, master, arcs, verifyReport); err != nil {
				return err
			}
			diffReport := filepath.Join(build, "reports", ds+".diff.json")
			if _, err := DiffTextDatasetGo(ds, rebuilt, master, arcs, diffReport); err != nil {
				return err
			}
			if stdout != nil {
				fmt.Fprintf(stdout, "VERIFY %s: PASS\nDIFF %s: %s\n", ds, ds, diffReport)
			}
		}
		if err := BuildGraphics(root, c, stdout, stderr); err != nil {
			return err
		}
		stage := filepath.Join(build, "dist.staging")
		if err := os.Mkdir(stage, 0o755); err != nil {
			return err
		}
		if err := BuildPatchBundles(root, stage, stdout); err != nil {
			return err
		}
		if err := buildQoLBundles(root, stage, c, stdout); err != nil {
			return err
		}
		if err := activateDist(root, stage); err != nil {
			return err
		}
		if stdout != nil {
			fmt.Fprintf(stdout, "DONE. Verified patch bundles (native-Go orchestration):\n  %s\n  %s\n", filepath.Join(root, ".local/dist/MS2_EN_FIDELITY_PATCH"), filepath.Join(root, ".local/dist/MSF_EN_FIDELITY_PATCH"))
		}
		return nil
	})
}

func validateChangeManifestCanonical(root string) error {
	return validateChangeManifestCanonicalPath(root, filepath.Join(root, ".local", "build", "change_manifest.json"))
}

// A stage-specific version permits canonical semantic-record count validation BEFORE
// publishing generated outputs, instead of detecting drift after overwriting
// a previously usable generation.
func validateChangeManifestCanonicalPath(root, manifestPath string) error {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var got map[string]struct {
		ChangedFields int `json:"changed_fields"`
		Mapped        int `json:"mapped_working_fields"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		return err
	}
	craw, err := os.ReadFile(filepath.Join(root, "toolchain", "manifests", "canonical_counts.json"))
	if err != nil {
		return err
	}
	var canon struct {
		Changed map[string]int `json:"expected_changed_fields_by_dataset"`
		Mapped  map[string]int `json:"expected_mapped_fields_by_dataset"`
	}
	if err := json.Unmarshal(craw, &canon); err != nil {
		return err
	}
	for ds, want := range canon.Changed {
		if got[ds].ChangedFields != want {
			return fmt.Errorf("%s: changed_fields %d != canonical %d", ds, got[ds].ChangedFields, want)
		}
		if mw, ok := canon.Mapped[ds]; ok && got[ds].Mapped != mw {
			return fmt.Errorf("%s: mapped_working_fields %d != canonical %d", ds, got[ds].Mapped, mw)
		}
	}
	return nil
}

func Verify(root string, quick bool, stdout, stderr io.Writer) error {
	step := func(label string) {
		if stdout != nil {
			fmt.Fprintln(stdout, label)
		}
	}
	step("[1/9] Python/domain-tool compile")
	if err := runEnv(root, stdout, stderr, os.Stdin, nil, "python3", "-m", "compileall", "-q", "toolchain/python-src/toolchain/tools/mskit", "toolchain/python-src/toolchain/exe_qol", "toolchain/python-src/toolchain/ms2_qol", "toolchain/python-src/toolchain/finale_qol", "toolchain/python-src/toolchain/ms2_save", "toolchain/python-src/scripts"); err != nil {
		return err
	}
	step("[2/9] Go regression + vet")
	if err := runEnv(root, stdout, stderr, os.Stdin, nil, "go", "test", "./...", "-count=1"); err != nil {
		return err
	}
	if err := runEnv(root, stdout, stderr, os.Stdin, nil, "go", "vet", "./..."); err != nil {
		return err
	}
	step("[3/9] Text tool surface")
	out, err := commandOutput(root, nil, "python3", "toolchain/python-src/toolchain/tools/ms_text_tool.py", "list-datasets")
	if err != nil {
		return err
	}
	if strings.Contains(string(out), "MSF_PC_KO") {
		return fmt.Errorf("unwanted Korean dataset is exposed")
	}
	step("[4/9] Structural regressions")
	for _, t := range []string{"test_edit_shared_strings.py", "test_stcm_relayout.py", "test_verify_logical_identity.py", "test_materialize_decoded_enc_targets.py", "test_source_cache_contract.py", "test_enc_strict_literals.py", "test_title_footer_discovery.py", "test_title_footer_rendering.py"} {
		if err := runEnv(root, stdout, stderr, os.Stdin, nil, "python3", filepath.Join("toolchain", "python-src", "toolchain", "tests", t)); err != nil {
			return err
		}
	}
	step("[5/9] Persistent source cache")
	c, err := EnsureSourceCache(root, stdout, stderr)
	if err != nil {
		return err
	}
	step("[6/9] Translation + graphics configuration")
	if err := PreflightTitleFooter(root, stdout); err != nil {
		return err
	}
	if _, err := ValidateTranslations(root, stdout); err != nil {
		return err
	}
	if err := VerifyGraphicsConfigNative(root, false); err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "graphics config (native Go): PASS")
	}
	if !quick {
		step("[7/9] Translation → technical-record bridge")
		if err := prepareSourceRecords(root, c); err != nil {
			return err
		}
		if _, err := BuildRecords(root, stdout); err != nil {
			return err
		}
		if err := validateChangeManifestCanonical(root); err != nil {
			return err
		}
	} else {
		step("[7/9] Translation → technical-record bridge (quick: skipped)")
	}
	step("[8/9] Repository hygiene")
	if err := CheckRepoHygieneNative(root); err != nil {
		return err
	}
	step("[9/9] Builder dependency gate")
	missing := MissingTools([]string{"go", "ffmpeg", "ffprobe", "zstd"})
	if len(missing) > 0 {
		return fmt.Errorf("missing maintainer tools: %s", strings.Join(missing, ", "))
	}
	if stdout != nil {
		fmt.Fprintln(stdout, "VERIFY_PROJECT: NATIVE-GO ORCHESTRATION PASSED")
	}
	return nil
}
