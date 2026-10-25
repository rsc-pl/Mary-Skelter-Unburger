package main

// Owner-reviewed Result and provisional Battle can be included in the same
// native straight-RGBA/GDAT transaction as other Go atlas families. This is
// intentionally opt-in. Camp must NOT enter this path until its complete
// strict BC3 source render passes. No alternative styling is introduced here.
import (
	"encoding/json"
	"errors"
	"fmt"
	"maryskelter-unburger/internal/mskit"
	"os"
	"path/filepath"
)

func isDeferredNameBank(id string) bool {
	return id == "MS2_RESULT_NAMES" || id == "MS2_BATTLE_NAMES"
}

type deferredNameBankSource struct {
	identity nameFamilyIdentity
	source   []byte
	rows     int
}

// Verify source/member/format/row layout before touching any private font.
func sourceDeferredNameBank(root, id string, originalGDAT []byte) (deferredNameBankSource, error) {
	var out deferredNameBankSource
	if !isDeferredNameBank(id) {
		return out, fmt.Errorf("unsupported deferred namebank %q", id)
	}
	identity := nameFamilyIdentities[id]
	b, err := os.ReadFile(filepath.Join(root, "graphics", "manifest.json"))
	if err != nil {
		return out, err
	}
	var m familyAssets
	if err = json.Unmarshal(b, &m); err != nil {
		return out, err
	}
	found := 0
	var target familyAsset
	for _, a := range m.Assets {
		if a.ID == id {
			target = a
			found++
		}
	}
	expectedRows, renderer := 13, "ms2_result_font_v2"
	if id == "MS2_BATTLE_NAMES" {
		expectedRows, renderer = 15, "ms2_battle_nameplates_font_v3"
	}
	if found != 1 || target.Renderer != renderer || target.Archive != 55 || target.Index != identity.index || target.Inner != identity.inner || len(target.Rows) != expectedRows || len(target.Sources) != 1 || target.Sources[0].SHA != identity.sha || target.Sources[0].Width != identity.width || target.Sources[0].Height != identity.height || target.Sources[0].Format != identity.format {
		return out, errors.New("deferred namebank canonical source/layout drift")
	}
	raw, _, err := mskit.ExtractGraphicsTID(originalGDAT, identity.index, identity.inner)
	if err != nil {
		return out, fmt.Errorf("extract original namebank TID: %w", err)
	}
	if !checkedHash(raw, identity.sha) {
		return out, errors.New("deferred namebank original TID SHA drift")
	}
	meta, err := mskit.ParseTIDMeta(raw)
	if err != nil {
		return out, err
	}
	if meta.Width != identity.width || meta.Height != identity.height || meta.Format != identity.format {
		return out, errors.New("deferred namebank TID geometry drift")
	}
	return deferredNameBankSource{identity: identity, source: raw, rows: expectedRows}, nil
}

func renderDeferredNameBank(root, id string, originalGDAT []byte) (deferredNameBankSource, string, []byte, error) {
	source, err := sourceDeferredNameBank(root, id, originalGDAT)
	if err != nil {
		return source, "", nil, err
	}
	var fontSHA string
	if id == "MS2_RESULT_NAMES" {
		// loadResult checks the pinned original preset/font SHA; it does
		// not change the owner-frozen legacy recipe in renderPinnedFamily.
		_, _, font, err := loadResult(root)
		if err != nil {
			return source, "", nil, err
		}
		fontSHA = digest(font)
	} else {
		// The Battle renderer validates this exact Neofara font itself.
		fontSHA = "0fa94bd516f44547d8cd1495f0503b37f4d9722b5ba751549bf46cb625707141"
	}
	rgba, err := renderPinnedFamily(root, id, source.source)
	if err != nil {
		return source, "", nil, err
	}
	if len(rgba) != source.identity.width*source.identity.height*4 {
		return source, "", nil, errors.New("wrong deferred namebank RGBA target size")
	}
	return source, fontSHA, rgba, nil
}
