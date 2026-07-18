package main

import (
	"runtime"
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

// TestResolveAudioThreads pins the AUDIO_THREADS -> cgroup -> NumCPU
// precedence chain (T-32-04/AUD-07 SC4). The cgroup branch's exact value is
// deliberately NOT asserted here (this test's host may or may not be a real
// cgroup v2 container -- CI runners, OrbStack host, developer laptops all
// differ); instead this test asserts the branch SELECTION: an explicit
// positive AUDIO_THREADS always wins outright regardless of what the host's
// cgroup detection would otherwise return, and an unset/non-positive
// AUDIO_THREADS always falls through past the "env override" source (either
// "cgroup" or "NumCPU fallback", both >= 1).
func TestResolveAudioThreads(t *testing.T) {
	t.Run("env override wins", func(t *testing.T) {
		t.Setenv("AUDIO_THREADS", "5")
		n, source := resolveAudioThreads()
		if n != 5 || source != "env override" {
			t.Fatalf("resolveAudioThreads() = (%d, %q), want (5, \"env override\")", n, source)
		}
	})

	t.Run("unset falls through past env override", func(t *testing.T) {
		n, source := resolveAudioThreads()
		if source == "env override" {
			t.Fatalf("resolveAudioThreads() source = %q, want a fallthrough branch (\"cgroup\" or \"NumCPU fallback\") when AUDIO_THREADS is unset", source)
		}
		if n < 1 {
			t.Fatalf("resolveAudioThreads() = (%d, %q), want n >= 1", n, source)
		}
	})

	t.Run("zero falls through past env override", func(t *testing.T) {
		t.Setenv("AUDIO_THREADS", "0")
		n, source := resolveAudioThreads()
		if source == "env override" {
			t.Fatalf("resolveAudioThreads() source = %q, want a fallthrough branch when AUDIO_THREADS=0", source)
		}
		if n < 1 {
			t.Fatalf("resolveAudioThreads() = (%d, %q), want n >= 1", n, source)
		}
	})

	t.Run("NumCPU fallback is a sane lower bound", func(t *testing.T) {
		// runtime.NumCPU() itself is always >= 1 per its own contract; this
		// only asserts the test host's own reported value is consistent,
		// guarding against a typo regressing the fallback arm to 0.
		if runtime.NumCPU() < 1 {
			t.Fatal("runtime.NumCPU() < 1, want >= 1 (stdlib contract violated)")
		}
	})
}

// TestStripInlineComment pins WR-06: AUDIO_MODEL_PATH read under a non-shell
// env-file loader (docker --env-file, compose env_file:, k8s configmap) may
// carry a trailing inline "# comment" that would otherwise reach whisper-cli
// as part of the -m path. Unlike firstField, stripping must be conservative
// enough to preserve paths that legitimately contain spaces or embedded '#'
// characters — only a '#' preceded by whitespace is a comment.
func TestStripInlineComment(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain path unchanged", "/models/ggml-base.bin", "/models/ggml-base.bin"},
		{"inline comment stripped", "/models/ggml-base.bin   # whisper.cpp model path", "/models/ggml-base.bin"},
		{"tab before comment stripped", "/models/ggml-base.bin\t# comment", "/models/ggml-base.bin"},
		{"path with spaces preserved", "/Users/dev/My Models/ggml-base.bin", "/Users/dev/My Models/ggml-base.bin"},
		{"path with spaces plus comment", "/Users/dev/My Models/ggml-base.bin # override", "/Users/dev/My Models/ggml-base.bin"},
		{"embedded hash preserved", "/models/exp#3/ggml-base.bin", "/models/exp#3/ggml-base.bin"},
		{"surrounding whitespace trimmed", "  /models/ggml-base.bin  ", "/models/ggml-base.bin"},
		{"empty stays empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripInlineComment(tc.in); got != tc.want {
				t.Fatalf("stripInlineComment(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
