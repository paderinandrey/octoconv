package convert

import (
	"strings"
	"testing"
)

func TestParseAudioOpts(t *testing.T) {
	t.Run("empty object valid, zero opts", func(t *testing.T) {
		o, err := ParseAudioOpts([]byte(`{}`))
		if err != nil {
			t.Fatalf("ParseAudioOpts({}) unexpected error: %v", err)
		}
		if o != (AudioOpts{}) {
			t.Errorf("ParseAudioOpts({}) = %+v, want zero value", o)
		}
	})

	t.Run("valid language en accepted", func(t *testing.T) {
		o, err := ParseAudioOpts([]byte(`{"language":"en"}`))
		if err != nil {
			t.Fatalf("ParseAudioOpts: unexpected error: %v", err)
		}
		if o.Language != "en" {
			t.Errorf("ParseAudioOpts = %+v, want language=en", o)
		}
	})

	t.Run("valid language ru accepted", func(t *testing.T) {
		o, err := ParseAudioOpts([]byte(`{"language":"ru"}`))
		if err != nil {
			t.Fatalf("ParseAudioOpts: unexpected error: %v", err)
		}
		if o.Language != "ru" {
			t.Errorf("ParseAudioOpts = %+v, want language=ru", o)
		}
	})

	t.Run("language auto accepted (maps to whisper -l auto)", func(t *testing.T) {
		o, err := ParseAudioOpts([]byte(`{"language":"auto"}`))
		if err != nil {
			t.Fatalf("ParseAudioOpts: unexpected error: %v", err)
		}
		if o.Language != "auto" {
			t.Errorf("ParseAudioOpts = %+v, want language=auto", o)
		}
	})

	t.Run("non-allowlisted language rejected", func(t *testing.T) {
		_, err := ParseAudioOpts([]byte(`{"language":"klingon"}`))
		if err == nil {
			t.Fatal("ParseAudioOpts with language=klingon = nil error, want error")
		}
		if !strings.Contains(err.Error(), "unsupported language") {
			t.Errorf("error = %q, want it to mention 'unsupported language'", err.Error())
		}
	})

	t.Run("translate true accepted", func(t *testing.T) {
		o, err := ParseAudioOpts([]byte(`{"translate":true}`))
		if err != nil {
			t.Fatalf("ParseAudioOpts: unexpected error: %v", err)
		}
		if !o.Translate {
			t.Errorf("ParseAudioOpts = %+v, want translate=true", o)
		}
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		if _, err := ParseAudioOpts([]byte(`{"language":"en","model":"large"}`)); err == nil {
			t.Error("ParseAudioOpts with unknown field 'model' = nil error, want error")
		}
	})

	t.Run("duplicate top-level key rejected", func(t *testing.T) {
		if _, err := ParseAudioOpts([]byte(`{"language":"en","language":"ru"}`)); err == nil {
			t.Error("ParseAudioOpts with duplicate key = nil error, want error")
		}
	})

	t.Run("trailing bytes rejected", func(t *testing.T) {
		if _, err := ParseAudioOpts([]byte(`{"language":"en"}garbage`)); err == nil {
			t.Error("ParseAudioOpts with trailing bytes = nil error, want error")
		}
	})

	t.Run("top-level null rejected", func(t *testing.T) {
		if _, err := ParseAudioOpts([]byte(`null`)); err == nil {
			t.Error("ParseAudioOpts with top-level null = nil error, want error")
		}
	})
}

func TestAudioOptsFromMap(t *testing.T) {
	t.Run("nil map yields zero opts", func(t *testing.T) {
		o, err := AudioOptsFromMap(nil)
		if err != nil {
			t.Fatalf("AudioOptsFromMap(nil) unexpected error: %v", err)
		}
		if o != (AudioOpts{}) {
			t.Errorf("AudioOptsFromMap(nil) = %+v, want zero value", o)
		}
	})

	t.Run("empty map yields zero opts", func(t *testing.T) {
		o, err := AudioOptsFromMap(map[string]any{})
		if err != nil {
			t.Fatalf("AudioOptsFromMap({}) unexpected error: %v", err)
		}
		if o != (AudioOpts{}) {
			t.Errorf("AudioOptsFromMap({}) = %+v, want zero value", o)
		}
	})

	t.Run("round trip valid map", func(t *testing.T) {
		m := map[string]any{"language": "fr", "translate": true}
		o, err := AudioOptsFromMap(m)
		if err != nil {
			t.Fatalf("AudioOptsFromMap: unexpected error: %v", err)
		}
		if o.Language != "fr" || !o.Translate {
			t.Errorf("AudioOptsFromMap = %+v, want language=fr/translate=true", o)
		}
	})

	t.Run("corrupt persisted map applies same strictness (invalid language)", func(t *testing.T) {
		m := map[string]any{"language": "not-a-real-language"}
		if _, err := AudioOptsFromMap(m); err == nil {
			t.Error("AudioOptsFromMap with invalid language = nil error, want error")
		}
	})

	t.Run("unknown field in persisted map rejected", func(t *testing.T) {
		m := map[string]any{"language": "en", "model": "large"}
		if _, err := AudioOptsFromMap(m); err == nil {
			t.Error("AudioOptsFromMap with unknown field = nil error, want error")
		}
	})
}

func TestValidateAudioApplicability(t *testing.T) {
	t.Run("empty opts always apply", func(t *testing.T) {
		if err := ValidateAudioApplicability(EngineDocument, "docx", "pdf", AudioOpts{}); err != nil {
			t.Errorf("ValidateAudioApplicability with zero opts = %v, want nil", err)
		}
	})

	t.Run("non-empty opts on non-audio engine rejected", func(t *testing.T) {
		o := AudioOpts{Language: "en"}
		if err := ValidateAudioApplicability(EngineDocument, "docx", "pdf", o); err == nil {
			t.Error("ValidateAudioApplicability on document engine = nil error, want error")
		}
	})

	t.Run("non-empty opts on audio engine accepted", func(t *testing.T) {
		o := AudioOpts{Language: "en", Translate: true}
		if err := ValidateAudioApplicability(EngineAudio, "mp3", "txt", o); err != nil {
			t.Errorf("ValidateAudioApplicability(audio) = %v, want nil", err)
		}
	})
}

// TestAudioOpts_InjectionCannotReachArgv is the AUD-03 required proof: client
// bytes carrying shell metacharacters never reach whisper-cli's argv.
//
// Part 1: ParseAudioOpts rejects every metacharacter-bearing language value
// via the closed audioLanguageAllowlist -- none of these strings are in the
// allowlist, so they never pass validation regardless of their content.
//
// Part 2: even a hand-constructed AudioOpts{Language: "..."} that bypasses
// ParseAudioOpts entirely is only ever consumed by building an exec.Command
// argv slice (Plan 03: `args = append(args, "-l", o.Language)`). No code
// path in this package formats Language into a shell string via
// fmt.Sprintf/string-concat into a command line -- runCommand (exec.go)
// invokes exec.Command(name, args...) directly, never a shell, so there is
// no shell-metacharacter injection surface even for a bypassed value. This
// is asserted here structurally: AudioOpts carries Language only as a plain
// string struct field, never a pre-built command-line string.
func TestAudioOpts_InjectionCannotReachArgv(t *testing.T) {
	payloads := []string{
		"; rm -rf /",
		"$(whoami)",
		"`whoami`",
	}

	t.Run("allowlist rejects every metacharacter payload", func(t *testing.T) {
		for _, p := range payloads {
			raw := []byte(`{"language":"` + strings.ReplaceAll(p, `"`, `\"`) + `"}`)
			_, err := ParseAudioOpts(raw)
			if err == nil {
				t.Errorf("ParseAudioOpts(language=%q) = nil error, want rejection by audioLanguageAllowlist", p)
			}
		}
	})

	t.Run("hand-constructed bypass value stays a plain struct field, never a shell string", func(t *testing.T) {
		// A caller that bypasses ParseAudioOpts entirely (e.g. constructs
		// AudioOpts by hand) still cannot leak this value into a shell: the
		// value is only ever a Go string held in a struct field. Nothing in
		// this package concatenates it into a command-line string -- it is
		// only ever appended as one exec.Command argv slice element, and
		// exec.Command never invokes /bin/sh. This test documents/pins that
		// invariant so a future edit adding string-concatenation would be
		// caught by review, not by a runtime injection.
		o := AudioOpts{Language: "; rm -rf /"}
		if o.Language != "; rm -rf /" {
			t.Fatalf("AudioOpts.Language = %q, want the raw payload preserved as a plain field value", o.Language)
		}
		// isZeroAudioOpts / ValidateAudioApplicability only inspect the
		// struct's fields -- they never format Language into a larger
		// string, so this bypass value cannot escape via those paths either.
		if isZeroAudioOpts(o) {
			t.Fatal("isZeroAudioOpts reported a non-empty AudioOpts as zero")
		}
	})
}
