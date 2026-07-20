package convert

// init wires concrete converters into the Default registry. To add support for
// a new engine or format pair, register it here with a single line.
func init() {
	Default.Register(LibvipsConverter{})
	Default.Register(LibreOfficeConverter{})
	Default.Register(ChromiumConverter{})
	Default.Register(AudioConverter{})
	// Registered AFTER AudioConverter (D-08, Phase 35): Registry.Register is
	// a bare map assignment with silent last-write-wins on pair collision --
	// a later registration silently wins any shared (from, to) pair with no
	// error, panic, or log. This ordering is safe only because AVConverter's
	// target formats ({mp4,webm,mp3,wav,m4a,jpg,png,webp}) are disjoint from
	// AudioConverter's ({txt,srt,vtt,json}); the sole guard is
	// TestAVAudioPairDisjointness (pairs_disjoint_test.go) -- there is no
	// runtime check. Registering AVConverter without also wiring SniffVideo
	// into the upload detection chain (internal/api/handlers.go) in the same
	// change would ship an engine for formats (mkv/webm) the service cannot
	// recognize -- see that file for the paired change.
	Default.Register(AVConverter{})
}
