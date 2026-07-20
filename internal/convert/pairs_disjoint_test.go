package convert

import "testing"

// TestAVAudioPairDisjointness proves AVConverter.Pairs() and
// AudioConverter.Pairs() share no (from, to) pair -- the regression guard
// Pitfall 7 (35-RESEARCH.md) and D-06 (35-CONTEXT.md) require.
//
// The hazard this test guards against: Registry.Register (convert.go:74-80)
// is a bare map assignment with NO collision check -- a later
// Default.Register(AVConverter{}) after AudioConverter would silently win
// any shared (from, to) pair, with no error, panic, or log. The only
// observable symptom would be a job silently routed to the wrong
// engine-class queue. THIS TEST IS THE ONLY GUARD; there is no runtime one.
//
// Why disjointness currently holds by construction: AVConverter's target
// formats are {mp4,webm,mp3,wav,m4a,jpg,png,webp} (av.go); AudioConverter's
// target formats are always {txt,srt,vtt,json} (whisper.go), UNCHANGED by
// D-04's expansion of audioSourceFormats. These two target sets are
// disjoint, so no (from, to) pair can collide even though both converters
// now share five SOURCE formats (mp4/mov/avi/mkv/webm, after D-04). A
// future edit that adds an overlapping TARGET format to either converter
// would silently break this invariant without this test.
func TestAVAudioPairDisjointness(t *testing.T) {
	avPairs := (AVConverter{}).Pairs()
	audioPairs := (AudioConverter{}).Pairs()

	for _, p := range avPairs {
		for _, q := range audioPairs {
			if p == q {
				t.Fatalf("pair %+v registered by both AVConverter and AudioConverter, want disjoint", p)
			}
		}
	}

	// Second assertion: the union's length must equal the sum of the two
	// converters' individual pair counts -- catches an overlap by COUNT as
	// well as by identity, so a future change that introduces a duplicate
	// pair is caught even if the identity loop above were ever weakened.
	union := make(map[Pair]bool, len(avPairs)+len(audioPairs))
	for _, p := range avPairs {
		union[p] = true
	}
	for _, p := range audioPairs {
		union[p] = true
	}
	if want := len(avPairs) + len(audioPairs); len(union) != want {
		t.Fatalf("len(union of AVConverter.Pairs() and AudioConverter.Pairs()) = %d, want %d (len(avPairs)=%d + len(audioPairs)=%d) -- a collision was masked by set semantics",
			len(union), want, len(avPairs), len(audioPairs))
	}
}
