---
phase: 30-audio-engine-foundation
verified: 2026-07-18T02:19:44Z
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
---

# Phase 30: Audio Engine Foundation Verification Report

**Phase Goal:** A standalone `AudioConverter` transcribes a local audio file to txt/srt/vtt/json with fail-closed content validation, built and testable against the binary before any queue/k8s plumbing.
**Verified:** 2026-07-18T02:19:44Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths

| # | Truth (ROADMAP Success Criterion) | Status | Evidence |
|---|---|---|---|
| 1 | A local mp3/wav/m4a/ogg file is validated by magic bytes — incl. bespoke ID3v2-aware, variable-offset MP3 detector (synchsafe skip, not fixed-window) — and content mismatches rejected fail-closed before any S3 write | ✓ VERIFIED | `internal/convert/audiosniff.go` implements `SniffAudio`/`matchWAV`/`matchOGG`/`matchM4A`/`matchMP3` with hand-decoded synchsafe size + footer-flag handling and a bounded-peek fail-closed guard (`mp3PeekLen`, `tagEnd+1 >= len(b)` → reject). MP3 is deliberately NOT in `sniff.go`'s fixed-window `signatures` table (grep confirms 0 matches). All `internal/convert/audiosniff_test.go` cases pass live, including `TestMatchMP3_FooterFlag`, `TestMatchMP3_OversizedDeclaredSize_FailsClosed`, `TestMatchMP3_TruncatedID3Header_FailsClosed`, `TestMatchM4A_MP4VideoStyleFtypRejected`. S3-write-boundary wiring is explicitly out of scope for Phase 30 (deferred to Phase 31; see Requirements Coverage note below). |
| 2 | `AudioConverter` transcribes a validated file through the two-stage ffmpeg→whisper-cli pipeline (one `AUDIO_ENGINE_TIMEOUT`-bounded context) to any of txt/srt/vtt/json selected via the existing `Pair` mechanism | ✓ VERIFIED | `internal/convert/whisper.go`: `Convert()` runs `runCommand(ctx, "ffmpeg", ...)` then `runCommand(ctx, "whisper-cli", ...)` sharing one caller-supplied `ctx`; `Pairs()` returns the 16-pair cross-product of 4 sources × 4 targets; `whisperOutputFlag` maps txt/srt/vtt/json to `-otxt/-osrt/-ovtt/-ojf`. Live test `TestAudioConverter_TextFormats_LiveBinary` ran for real (15.67s, not skipped) and produced non-empty txt/srt/vtt output against `jfk.wav`. |
| 3 | `target=json` output carries segment- and word-level start/end/text timestamps, verified live against pinned whisper-cli v1.9.1 (SEED-001 hinge) | ✓ VERIFIED | `TestAudioConverter_JSONFull_LiveBinary` ran live (0.85s, real binary, not skipped) and asserted `transcription[].{timestamps,offsets,text}` present on every segment and `tokens[].{text,p,id}` present with per-token `timestamps`/`offsets` optional (Assumption A3), matching RESEARCH.md's source-verified schema. `-ojf` (not `-oj`) selected specifically so segment AND token timestamps come from one invocation. |
| 4 | `AudioOpts{language (closed allowlist), translate}` flows through the validated-opts pattern (OPTS-01 precedent) — injection test proves client bytes never reach engine argv | ✓ VERIFIED | `internal/convert/audioopts.go`: `ParseAudioOpts`/`AudioOptsFromMap` reuse `checkStrictObject` verbatim (0 duplication), validate `Language` against a closed 6-entry `audioLanguageAllowlist` map lookup. `TestAudioOpts_InjectionCannotReachArgv` asserts `;`, `$(whoami)`, and backtick payloads are all rejected by the allowlist; `whisperArgs` in `whisper.go` only ever appends `Language` as a discrete argv slice element (`args = append(args, "-l", lang)`), never a shell string — `runCommand`/`exec.Command` never invokes a shell. `TestWhisperArgs` pins the exact argv. |
| 5 | An input whose ffprobe-measured duration exceeds `AUDIO_MAX_DURATION_SECONDS` is rejected with a predictable terminal/422 (decompression-bomb analog), and hallucination-on-silence is recorded as an accepted residual risk in the phase decision log | ✓ VERIFIED (converter-side mechanics; env-var/422 wiring explicitly deferred to Phase 31 per phase scope fence) | `internal/convert/audioduration.go`: `EnforceMaxDuration`/`ProbeDuration`/`ErrAudioDurationExceeded` exist, are `errors.Is`-matchable, and fail closed on NaN/±Inf/negative/implausible float→Duration conversions (CR-01 fix, `parseProbedDuration`, commit `2a10d70`) — this was a genuine bypassable-guard bug caught and fixed post-review. Hallucination-on-silence is documented as an accepted residual risk in both `audioduration.go`'s package doc comment and `whisper.go`'s `Convert` doc comment (no separate DECISIONS.md file exists in this repo; doc-comment recording matches the plan's explicit instruction). `AUDIO_MAX_DURATION_SECONDS` env wiring and the actual HTTP 422 surface are explicitly out of scope for Phase 30 per the PLAN's scope fence and Phase 31's roadmap goal — orchestrator instructions for this verification explicitly confirm this deferral should not fail the phase. |

**Score:** 5/5 truths verified

### Required Artifacts

| Artifact | Expected | Status | Details |
|---|---|---|---|
| `internal/convert/audiosniff.go` | `SniffAudio` + matchers, ID3v2 synchsafe/footer/fail-closed | ✓ VERIFIED | Exists, substantive, matches plan's `<behavior>` spec exactly; unit-tested |
| `internal/convert/audiosniff_test.go` | Magic-bytes + adversarial fail-closed coverage | ✓ VERIFIED | 22 test functions, all pass (see test run below) |
| `internal/convert/audioduration.go` | `ProbeDuration` + `EnforceMaxDuration` + `ErrAudioDurationExceeded` | ✓ VERIFIED | Present, plus post-review CR-01 fix (`parseProbedDuration` float-space validation) |
| `internal/convert/audioduration_test.go` | Under/over ceiling + unparseable-output coverage | ✓ VERIFIED | `TestProbeDuration_NonAudioFileReturnsError` + platform-independent adversarial tests pass |
| `internal/convert/audioopts.go` | `AudioOpts`, `ParseAudioOpts`, `AudioOptsFromMap`, `ValidateAudioApplicability`, `audioLanguageAllowlist` | ✓ VERIFIED | All present; reuses `checkStrictObject` (grep confirms 0 local redefinition) |
| `internal/convert/audioopts_test.go` | Injection + allowlist-rejection + strictness + FromMap round-trip | ✓ VERIFIED | `TestParseAudioOpts`, `TestAudioOptsFromMap`, `TestValidateAudioApplicability`, `TestAudioOpts_InjectionCannotReachArgv` all pass |
| `internal/convert/whisper.go` | `AudioConverter` (Pairs/Convert/Engine), two-stage pipeline, `whisperOutputFlag` | ✓ VERIFIED | Present; `Engine()` returns `EngineAudio`; 16 Pairs; post-review WR-02/WR-03 fixes present (fail-fast on unsupported target, explicit `-l auto` default) |
| `internal/convert/whisper_test.go` | Live-gated JSON/timestamp assertions + txt/srt/vtt smoke + MIME/NormalizeFormat checks | ✓ VERIFIED | All tests ran live against real binaries (not skipped — `command -v ffmpeg/ffprobe/whisper-cli` all resolve; `~/.cache/whisper/ggml-base.bin` present) |
| `internal/convert/sniff.go` (modified) | MIMEType cases for txt/srt/vtt/json + audio inputs | ✓ VERIFIED | All 8 audio-related cases present (4 inputs + 4 outputs), post-review WR-04 fix added the 4 input MIME types |

### Key Link Verification

| From | To | Via | Status | Details |
|---|---|---|---|---|
| `audioduration.go` | `exec.go runCommand` | hardened ffprobe subprocess | ✓ WIRED | `runCommand(ctx, "ffprobe", ...)` present, grep-confirmed |
| `audiosniff.go matchMP3` | bounded peek fail-closed discipline | `mp3PeekLen` bound check | ✓ WIRED | `tagEnd+1 >= len(b)` guard present before indexing |
| `audioopts.go ParseAudioOpts` | `opts.go checkStrictObject` | shared strict-object helper | ✓ WIRED | `checkStrictObject(raw)` called; 0 local redefinitions |
| `audioopts.go ValidateAudioApplicability` | `convert.go EngineAudio` | engine-scoped applicability gate | ✓ WIRED | `engine != EngineAudio` check present |
| `whisper.go Convert` | `exec.go runCommand` | two hardened subprocess calls | ✓ WIRED | Both `runCommand(ctx, "ffmpeg", ...)` and `runCommand(ctx, "whisper-cli", ...)` present, share one `ctx` |
| `whisper.go Convert` | `audioopts.go AudioOptsFromMap` | strict opts re-parse on converter read path | ✓ WIRED | `AudioOptsFromMap(opts)` called first, before any subprocess |
| `whisper.go AudioConverter.Engine` | `convert.go EngineAudio` | engine-class identity | ✓ WIRED | `func (AudioConverter) Engine() string { return EngineAudio }` |

### Behavioral Spot-Checks / Live Test Run

Ran exactly the command specified in the verification brief:

```
go test ./internal/convert/ -count=1 -v -run 'Audio|Whisper|Sniff'
```

Result: **PASS**, 16.992s total. All 45+ subtests passed, including:
- `TestAudioConverter_JSONFull_LiveBinary` — 0.85s (genuinely ran against the real whisper-cli v1.9.1 binary, not skipped)
- `TestAudioConverter_TextFormats_LiveBinary/{txt,srt,vtt}` — 15.67s combined (genuinely ran, not skipped)
- `TestMatchM4A_MP4VideoStyleFtypRejected`, `TestMatchMP3_FooterFlag`, `TestMatchMP3_OversizedDeclaredSize_FailsClosed` — all pass

Local toolchain confirmed present and used (not merely claimed): `ffmpeg`/`ffprobe`/`whisper-cli` all resolve via `command -v` to `/opt/homebrew/bin/`, and `~/.cache/whisper/ggml-base.bin` (147,951,465 bytes) is present.

Full repo test suite (`go test ./... -count=1`) also passes cleanly across all 14 packages with test files — no regression introduced in `internal/api`, `internal/worker`, `internal/reconciler`, etc.

`go build ./...`, `go vet ./internal/convert/`, and `gofmt -l internal/convert/` are all clean (no output).

### Code Review Fix Verification (30-REVIEW.md)

All 5 claimed fix commits verified present in `git log` and their content verified in the current source:

| Finding | Commit | Status |
|---|---|---|
| CR-01 (duration guard bypassable via float→Duration overflow/NaN/negative) | `2a10d70` | ✓ Fixed — `parseProbedDuration` validates in float space (`math.IsNaN`/`IsInf`/negative/`maxSaneDurationSeconds` checks) before any conversion |
| WR-01 (m4a allowlist admits plain MP4 video via `isom`/`mp42`) | `2a02140` | ✓ Fixed — `m4aBrands` now only `{"M4A ", "M4B "}`; `TestMatchM4A_MP4VideoStyleFtypRejected` added and passes |
| WR-02 (Convert runs full pipeline before failing on unsupported target) | `f898b1a` | ✓ Fixed — `Convert` checks `outFlags == nil` before stage 1; `TestAudioConverter_UnsupportedTargetFailsFast` passes |
| WR-03 (absent language silently defaults to whisper-cli's English) | `47b5c9e` | ✓ Fixed — `whisperArgs` defaults empty `Language` to `"auto"` explicitly; `TestWhisperArgs` passes |
| WR-04 (MIMEType lacks audio input formats) | `8d1112d` | ✓ Fixed — `mp3`/`wav`/`m4a`/`ogg` cases added to `MIMEType` |

IN-01 and IN-02 (informational, deferred to Phase 31) are correctly left as documented deferrals, not fixed in this phase — consistent with review disposition.

### Requirements Coverage

| Requirement | Source Plan | Description (abbreviated) | Status | Evidence |
|---|---|---|---|---|
| AUD-01 | 30-01 | mp3/wav/m4a/ogg magic-bytes fail-closed validation incl. ID3v2 MP3 detector | ✓ SATISFIED (content-validation half only — see note) | `audiosniff.go` + tests |
| AUD-02 | 30-03 | txt/srt/vtt/json via Pair mechanism; target=json segment+word timestamps live-verified | ✓ SATISFIED | `whisper.go` + `whisper_test.go` live run |
| AUD-03 | 30-02 | AudioOpts{language allowlist, translate} validated-opts + injection test | ✓ SATISFIED | `audioopts.go` + `audioopts_test.go` |
| AUD-04 | 30-01 | Duration guard via ffprobe, decompression-bomb analog | ✓ SATISFIED (converter-side mechanics only — see note) | `audioduration.go` + tests |

**Note on AUD-01/AUD-04 scope:** The full Russian-language requirement text in REQUIREMENTS.md for AUD-01 additionally describes "клиент может отправить аудиофайл через `POST /v1/jobs`" (client can submit via the live API) and AUD-04 describes the env var `AUDIO_MAX_DURATION_SECONDS` and a live 422 response. Phase 30's PLAN files and ROADMAP success criteria explicitly scope-fence this API/queue/env-var wiring to Phase 31 (registration of `AudioConverter` into `convert.Default`, `EngineFor` routing, `AUDIO_MAX_DURATION_SECONDS`/`AUDIO_ENGINE_TIMEOUT` env plumbing, and the 422 HTTP surface are all explicitly deferred, with rationale documented in all three SUMMARYs and `whisper.go`'s inline comments). This verification's brief explicitly instructed not to fail the phase for this deferral, provided it is documented — it is. REQUIREMENTS.md's phase-mapping table currently lists AUD-01 and AUD-04 as "Phase 30" only (not also Phase 31), which is a minor requirements-tracking inconsistency worth flagging for the milestone owner, but is not a Phase 30 code gap.

No orphaned requirements found — AUD-05 through AUD-08 are correctly out of Phase 30's `requirements:` frontmatter and are mapped to Phases 31-33 in REQUIREMENTS.md.

### Anti-Patterns Found

None. Scanned all Phase 30 files (`audiosniff.go`, `audioduration.go`, `audioopts.go`, `whisper.go`, `sniff.go`, `convert.go`) for `TBD`/`FIXME`/`XXX`/`TODO`/`HACK`/`PLACEHOLDER`/"not yet implemented"/"coming soon" — zero matches. No empty-return stubs, no hardcoded-empty data flowing to output.

### Human Verification Required

None. This phase produces no UI, no live API surface, and no external-service-dependent behavior beyond the already-live-tested local whisper-cli/ffmpeg binaries. All success criteria are verifiable by code inspection + automated test execution.

### Gaps Summary

No gaps. All 5 ROADMAP success criteria are verified against actual, running code — not SUMMARY.md claims. The live-gated whisper-cli/ffmpeg tests were re-run independently in this verification (not trusted from prior claims) and genuinely executed against the real pinned v1.9.1 binary (confirmed by non-trivial execution time: 0.85s + 15.67s, not an instant skip). All 5 code-review fix commits (CR-01, WR-01..04) were independently confirmed present both in `git log` and in the current source content, not merely trusted from 30-REVIEW.md's "Status: fixed" claims. The full repository test suite passes with no regressions.

The only nuance is the deliberate, well-documented scope fence deferring API/queue/env-var wiring for AUD-01/AUD-04 to Phase 31 — explicitly sanctioned by this verification's brief and consistent with the ROADMAP's phase-30-specific (narrower) success-criteria wording.

---

*Verified: 2026-07-18T02:19:44Z*
*Verifier: Claude (gsd-verifier)*
