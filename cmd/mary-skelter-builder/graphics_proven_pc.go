package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"maryskelter-unburger/internal/maintainer"
)

// graphics-proven-pc-go is an opt-in PC bridge, not the default game builder.
// A user must provide genuine pristine full PC masterWindows archives.
func runGraphicsProvenPC(root string, args []string) {
	fs := flag.NewFlagSet("graphics-proven-pc-go", flag.ExitOnError)
	ms2 := fs.String("ms2-master", "", "pristine MS2 masterWindows directory")
	finale := fs.String("finale-master", "", "pristine Finale masterWindows directory")
	out := fs.String("output-dir", "", "candidate results directory, outside pristine game sources")
	stagedMS2 := fs.String("staged-ms2-master", "", "optional already text-rebuilt MS2 masterWindows directory; requires both staged directories")
	stagedFinale := fs.String("staged-finale-master", "", "optional already text-rebuilt Finale masterWindows directory; requires both staged directories")
	binary := fs.String("renderer-bin", "", "compiled native bcn-tid-go executable (not Python)")
	validate := fs.Bool("validate-only", false, "read-only inspection of allowlisted original source TIDs")
	deferred := fs.Bool("include-deferred-name-banks", false, "opt in to frozen Result and provisional Battle candidate (not production)")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fatal(fmt.Errorf("unexpected argument %s", fs.Arg(0)))
	}
	if *ms2 == "" && *finale == "" {
		*ms2 = filepath.Join(root, ".local", "source_cache", "pristine", "ms2", "masterWindows")
		*finale = filepath.Join(root, ".local", "source_cache", "pristine", "finale", "masterWindows")
	}
	r, err := maintainer.NativeProvenPCGraphics(root, maintainer.NativeProvenPCGraphicsOptions{MS2Master: *ms2, FinaleMaster: *finale, OutputDir: *out, RendererBinary: *binary, ValidateOnly: *validate, IncludeDeferredNameBanks: *deferred, StagedMS2Master: *stagedMS2, StagedFinaleMaster: *stagedFinale})
	if err != nil {
		fmt.Fprintf(os.Stderr, "PC_NATIVE_CANDIDATE_BLOCKED: %v\n", err)
		os.Exit(1)
	}
	must(maintainer.WriteNativeProvenPCGraphicsReport(os.Stdout, r))
}
