package updater

type LegacyFile struct{ RelativePath, PristineSHA string }
type BytePatch struct {
	RVA                   uint32
	Expected, Replacement []byte
	Label                 string
}
type Profile struct {
	Key, DisplayName, ExeName, BundleDir string
	OriginalSHA, DestubSHA, FinalSHA     string
	Patches                              []BytePatch
	LatePatches                          []BytePatch
	LegacyFiles                          []LegacyFile
	RequiredMarkers                      []string
	CleanupAuditRoots                    []string
}

var Profiles = map[string]Profile{
	"ms2": {
		Key: "ms2", DisplayName: "Mary Skelter 2", ExeName: "MarySkelter2.exe", BundleDir: "MS2_EN_FIDELITY_PATCH",
		OriginalSHA: "07b97dd3b4aa9e50249a2fcfb619b31822eb7219ebeae150b9b9c421a5b5834c",
		DestubSHA:   "c24df22f43524e236925bf38ab8cf9d6171ba3518e6f1b5828bdf740d95cffbc",
		FinalSHA:    "81b6641fadff2767bb23492b1c6b0a9cac645a7496c65ed9d8eef2144ca94fa8",
		Patches: []BytePatch{
			{RVA: 0x25B2BB, Expected: []byte{0x8b, 0x41, 0x40}, Replacement: []byte{0x6a, 0x1b, 0x58}, Label: "skip startup logos"},
			{RVA: 0x2AA27A, Expected: []byte{0x48, 0x8d, 0x0d, 0xaf, 0x1b, 0x71, 0x00}, Replacement: []byte{0x48, 0x8d, 0x0d, 0xef, 0x1a, 0x71, 0x00}, Label: "Japanese voice boot path"},
		},
		LatePatches: []BytePatch{
			{RVA: 0x000BFE0E, Expected: []byte{0x48, 0x8d, 0x15, 0x5b, 0x69, 0x64, 0x00, 0x48, 0x8d, 0x0d, 0x0c, 0x5b, 0x2a, 0x01, 0xe8, 0xbf, 0x7c, 0x28, 0x00}, Replacement: []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90}, Label: "skip Voice_EN.bra mount"},
		},
		LegacyFiles:       []LegacyFile{{"masterWindows/GxArchivedFile053.dat", "b1d367fe54224b061e32ff6fcc2dbe974741dae1ac2ac3a630f37a2e26a2b581"}},
		RequiredMarkers:   []string{"Minigame.dat"},
		CleanupAuditRoots: []string{"masterWindows/Voice_EN.bra", "masterWindows/MOVIE_EN_SUB"},
	},
	"finale": {
		Key: "finale", DisplayName: "Mary Skelter Finale", ExeName: "MarySkelterFinale.exe", BundleDir: "MSF_EN_FIDELITY_PATCH",
		OriginalSHA: "5c16abfdc3f355404c2c7faa6b277e5023812d2292cb8c5d4ad5177ba6c05a54",
		DestubSHA:   "900a2cf0485fe882285e2409b2105eda52d44063c378d4ea9e9e1db052f2fa25",
		FinalSHA:    "cc0faf4df3c94e64a0084092e6afdb8c34dee9abe9f00a9f9c429cc5957c570c",
		Patches: []BytePatch{
			{RVA: 0x29B248, Expected: []byte{0x01}, Replacement: []byte{0x1a}, Label: "skip startup logos"},
			{RVA: 0x11FC81, Expected: []byte{0x0c}, Replacement: []byte{0xff}, Label: "Japanese voice resource variant"},
		},
		LatePatches: []BytePatch{
			{RVA: 0x00091F10, Expected: []byte{0x48, 0x8d, 0x15, 0xd9, 0xeb, 0x8a, 0x00, 0x48, 0x8d, 0x0d, 0xc2, 0xd0, 0x0f, 0x01, 0xe8, 0x4d, 0x16, 0x2f, 0x00}, Replacement: []byte{0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90, 0x90}, Label: "skip Sound_EN.bra mount"},
		},
		LegacyFiles:       []LegacyFile{{"masterWindows/GxArchivedFile068.dat", "18c0d755de9270489ac524b43d74f2da37b4140216f1a3eea3f149efb90525fe"}},
		CleanupAuditRoots: []string{"masterWindows/Sound_EN.bra"},
	},
}
