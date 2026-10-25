package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"maryskelter-unburger/internal/maintainer"
)

type graphicsPatchCLIReport struct {
	GDATIndex    int    `json:"gdat_index"`
	CL3Index     int    `json:"cl3_index"`
	InnerName    string `json:"inner_name"`
	OldTIDSHA256 string `json:"old_tid_sha256"`
	NewTIDSHA256 string `json:"new_tid_sha256"`
}

func sha256Hex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func runGraphicsExtractTID(root string, args []string) {
	fs := flag.NewFlagSet("graphics-extract-tid", flag.ExitOnError)
	dat := fs.String("dat", "", "GDAT path")
	idx := fs.Int("gdat-index", -1, "GDAT index")
	inner := fs.String("inner", "", "CL3 member name")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *dat == "" || *idx < 0 || *inner == "" {
		fatal(fmt.Errorf("--dat, --gdat-index and --inner are required"))
	}
	tid, _, err := maintainer.ExtractGraphicsTIDFile(*dat, *idx, *inner)
	must(err)
	_, err = os.Stdout.Write(tid)
	must(err)
}

func runGraphicsPatchTID(root string, args []string) {
	fs := flag.NewFlagSet("graphics-patch-tid", flag.ExitOnError)
	dat := fs.String("dat", "", "input GDAT path")
	out := fs.String("out", "", "output GDAT path")
	idx := fs.Int("gdat-index", -1, "GDAT index")
	inner := fs.String("inner", "", "CL3 member name")
	_ = fs.Parse(args)
	if fs.NArg() != 0 || *dat == "" || *out == "" || *idx < 0 || *inner == "" {
		fatal(fmt.Errorf("--dat, --out, --gdat-index and --inner are required"))
	}
	newTID, err := io.ReadAll(os.Stdin)
	must(err)
	oldTID, _, err := maintainer.ExtractGraphicsTIDFile(*dat, *idx, *inner)
	must(err)
	loc, err := maintainer.PatchGraphicsTIDFile(root, *dat, *out, *idx, *inner, newTID)
	must(err)
	rep := graphicsPatchCLIReport{GDATIndex: *idx, CL3Index: loc.CL3Index, InnerName: *inner, OldTIDSHA256: sha256Hex(oldTID), NewTIDSHA256: sha256Hex(newTID)}
	raw, err := json.Marshal(rep)
	must(err)
	fmt.Println(string(raw))
}
