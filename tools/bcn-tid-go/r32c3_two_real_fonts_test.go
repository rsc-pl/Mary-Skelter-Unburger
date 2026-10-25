package main

// legacy: physical source + owner-private fonts test. The five actual font binaries
// are never shipped; test automatically runs only in the owner's private tree.
import (
	"crypto/sha256"
	"encoding/hex"
	"maryskelter-unburger/internal/privateassets"
	"os"
	"path/filepath"
	"testing"
)

func TestTwoRealR32C3AtlasSourceWithOwnerFonts(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"third_party/fonts/avionic/AvionicCond-Bold.otf", "third_party/fonts/chiaro/ChiaroStd-B.otf"} {
		if _, err := os.Stat(filepath.Join(root, f)); os.IsNotExist(err) {
			t.Skip("owner-private font not in public recovery archive")
		}
	}
	cases := []struct{ id, source, rgbaSHA string }{
		{"MS2_PARTY_FORMATION_NAMES", "source-party-formation-rendered-reference", "2992304c2754e8cb852c196e9c865e6c21d2bb79b480f12beb70fc59d400cf03"},
		{"MS2_EQUIPMENT_HEADER_NAMES", "source-equipment-header-rendered-reference", "13ae1a9b2f6b0f4f4ebce4736b8768e67ea6c4c31b3dfc11fce98c92754f6650"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			sourcePath, err := privateassets.Resolve(root, c.source)
			if err != nil {
				t.Fatal(err)
			}
			original, err := os.ReadFile(sourcePath)
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := renderR32C3Triad(root, c.id, original)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256(rendered)
			if hex.EncodeToString(hash[:]) != c.rgbaSHA {
				t.Fatalf("font-backed atlas changed: %x", hash)
			}
			if c.id == "MS2_PARTY_FORMATION_NAMES" {
				output, rep, err := EncodeChangedTID(original, rendered, 6, 64, 64)
				if err != nil {
					t.Fatal(err)
				}
				encodedHash := sha256.Sum256(output)
				if hex.EncodeToString(encodedHash[:]) != "4f4a1697234c0612d9b323a1e7a31c940f64332a7c9e2842b923b4ae758dda0c" {
					t.Fatalf("strict BC7 output changed: %x", encodedHash)
				}
				if rep.MaxAlphaError > 64 || rep.MaxVisibleError > 64 {
					t.Fatalf("quality guard broken: %+v", rep)
				}
			}
		})
	}
}
