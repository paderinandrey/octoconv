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
// Pairs are compared in NORMALIZED form (NormalizeFormat on both From and To),
// because that is exactly the key Registry.Register indexes on (convert.go:78).
// Comparing raw pairs would miss an alias-level collision -- e.g. one converter
// claiming target "jpeg" and the other "jpg" map to the same registry slot even
// though the raw Pair values differ (35-SECURITY.md non-blocking observation 1).
// All current formats are already canonical, so this changes nothing today; it
// closes the gap against a future alias-form pair being added.
func TestAVAudioPairDisjointness(t *testing.T) {
	norm := func(p Pair) Pair {
		return Pair{From: NormalizeFormat(p.From), To: NormalizeFormat(p.To)}
	}
	avPairs := make([]Pair, 0)
	for _, p := range (AVConverter{}).Pairs() {
		avPairs = append(avPairs, norm(p))
	}
	audioPairs := make([]Pair, 0)
	for _, p := range (AudioConverter{}).Pairs() {
		audioPairs = append(audioPairs, norm(p))
	}

	for _, p := range avPairs {
		for _, q := range audioPairs {
			if p == q {
				t.Fatalf("normalized pair %+v registered by both AVConverter and AudioConverter, want disjoint", p)
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
