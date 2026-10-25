package updater

// English-dub cleanup is intentionally game/path aware rather than a wildcard.
// Regular standalone archives stay exact path/size/SHA pinned. MS2 MOVIE_EN_SUB
// is a special shipped directory whose complete contents are known to be the
// three English-dub movie variants below; the directory is purgeable only when
// its inventory is exactly this set of regular files and contains no extras,
// subdirectories, or symlinks. Contents are fingerprinted into the receipt at
// runtime so compatible Steam revisions are not rejected merely because an MP4
// was rebuilt byte-differently.
//
// Finale Sound_EN.bra cleanup is independent from movie remux success. Movie
// audio replacement is automatic/best-effort; English-dub purge is a separate
// default-ON user option and may complete even when a movie is unresolved.
var englishCleanupAllowlist = []CleanupManifestEntry{
	{GameKey: "ms2", RelativePath: "masterWindows/Voice_EN.bra", Size: 282182260, SHA256: "ca16e06a1d683c4aa217ee917f0b9a7720775b10d88b9a00958ffec593aadc36"},
	{GameKey: "finale", RelativePath: "masterWindows/Sound_EN.bra", Size: 209778703, SHA256: "f3d119cc23c382dd5c0a842ef89df6e9e69176e875d65dbd451dcceda2af20cd"},
}

var englishCleanupDirectories = []CleanupDirectorySpec{
	{
		GameKey:      "ms2",
		RelativePath: "masterWindows/MOVIE_EN_SUB",
		ExpectedFiles: []string{
			"400004.mp4",
			"600000.mp4",
			"700000.mp4",
		},
	},
}
