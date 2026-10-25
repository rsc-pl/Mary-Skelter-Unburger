package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"maryskelter-unburger/internal/maintainer"
)

// Explicit opt-in experimental Go-only graphics container patch. The new TID
// is provided via stdin, and source SHA pins the complete pristine DAT.
func runGraphicsPatchTIDGo(args []string) {
	fs := flag.NewFlagSet("graphics-patch-tid-go", flag.ExitOnError)
	source := fs.String("dat", "", "pristine GDAT")
	dest := fs.String("out", "", "new result GDAT (must not exist)")
	sha := fs.String("source-sha256", "", "SHA-256 of pristine GDAT (mandatory)")
	idx := fs.Int("gdat-index", -1, "target GDAT index")
	inner := fs.String("inner", "", "case-insensitive CL3 member name")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *source == "" || *dest == "" || *sha == "" || *idx < 0 || *inner == "" {
		fatal(fmt.Errorf("--dat, --out, --source-sha256, --gdat-index, --inner are required"))
	}
	tid, err := io.ReadAll(io.LimitReader(os.Stdin, 64<<20+1))
	must(err)
	if len(tid) == 0 || len(tid) > 64<<20 {
		fatal(fmt.Errorf("replacement TID must be 1..64 MiB"))
	}
	rep, err := maintainer.PatchGraphicsTIDGoFile(*source, *dest, *sha, *idx, *inner, tid)
	must(err)
	enc := json.NewEncoder(os.Stdout)
	must(enc.Encode(rep))
}
