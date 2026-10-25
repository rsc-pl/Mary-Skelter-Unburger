package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"maryskelter-unburger/internal/maintainer"
)

// Read-only owner-side source audit. The JSON contains only public archive
// identifiers and checksums; neither private archive bytes nor user paths.
func runPCMasterAuditNative(root string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("pc-master-audit-go", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	ms2 := fs.String("ms2-master", "", "pristine MS2 masterWindows directory")
	finale := fs.String("finale-master", "", "pristine Finale masterWindows directory")
	all := fs.Bool("all-manifest", false, "audit all 46 pinned PC DATs, not only the currently required 10")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}
	if *ms2 == "" || *finale == "" {
		return errors.New("both --ms2-master and --finale-master are required for a complete read-only audit")
	}
	a, err := filepath.Abs(*ms2)
	if err != nil {
		return err
	}
	b, err := filepath.Abs(*finale)
	if err != nil {
		return err
	}
	report, err := maintainer.AuditPCMasterSourcesNative(root, a, b, *all)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(report); err != nil {
		return err
	}
	if report.Status != "PASS_SOURCE_IDENTITY_ONLY" {
		return fmt.Errorf("pristine PC source audit incomplete: %d/%d verified (no game data altered)", report.VerifiedArchives, report.ExpectedArchives)
	}
	return nil
}
