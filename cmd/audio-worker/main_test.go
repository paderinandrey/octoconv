package main

import (
	"testing"
	"time"
)

// TestEnvDurationSeconds pins WR-05: AUDIO_MAX_DURATION_SECONDS's
// _SECONDS-suffixed name invites a bare integer value, which
// time.ParseDuration alone rejects — envDurationSeconds must accept BOTH the
// bare-seconds form the name advertises and the codebase's usual Go duration
// syntax, tolerate a trailing inline comment (firstField, same convention as
// envDuration/envInt), and fall back to the default (with a logged warning,
// not silently by shape-confusion) only for genuinely unparseable values.
func TestEnvDurationSeconds(t *testing.T) {
	const key = "TEST_AUDIO_MAX_DURATION_SECONDS"
	def := 4 * time.Hour

	cases := []struct {
		name  string
		value string
		set   bool
		want  time.Duration
	}{
		{"unset uses default", "", false, def},
		{"bare integer seconds", "7200", true, 2 * time.Hour},
		{"go duration hours", "2h", true, 2 * time.Hour},
		{"go duration seconds", "14400s", true, 4 * time.Hour},
		{"bare seconds with inline comment", "7200   # two hours", true, 2 * time.Hour},
		{"duration with inline comment", "90m   # ninety minutes", true, 90 * time.Minute},
		{"zero is a valid explicit ceiling", "0", true, 0},
		{"negative bare seconds falls back", "-5", true, def},
		{"garbage falls back", "four-hours", true, def},
		{"empty value uses default", "", true, def},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			}
			if got := envDurationSeconds(key, def); got != tc.want {
				t.Fatalf("envDurationSeconds(%q=%q) = %v, want %v", key, tc.value, got, tc.want)
			}
		})
	}
}
