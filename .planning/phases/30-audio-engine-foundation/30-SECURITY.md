---
phase: 30
slug: audio-engine-foundation
status: verified
threats_open: 0
asvs_level: 1
created: 2026-07-18
---

# Phase 30 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> This phase establishes the audio engine's fail-closed input-validation controls
> (magic-bytes sniffing, declared-duration guard), the validated-opts layer
> (AudioOpts/language allowlist), and the standalone two-stage
> ffmpeg→whisper-cli transcription pipeline. `AudioConverter` is deliberately
> NOT registered into `convert.Default` this phase (Phase 31 wires the live
> API/queue/worker routing) — no client-facing attack surface is live yet,
> but the controls that will gate it once wired are built and tested now.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|----------------|
| untrusted audio bytes → magic-bytes parser | Client-supplied container bytes peeked in-memory by `SniffAudio`/`matchMP3` | audio container bytes |
| untrusted audio file → ffprobe subprocess | `ProbeDuration` shells the raw file into ffprobe before any decode | file path, subprocess stdout |
| client opts JSON → `AudioOpts` → engine argv | Client-supplied transcription options parsed and mapped to whisper-cli flags | opts JSON, argv slice elements |
| normalized audio → whisper-cli subprocess | ffmpeg-normalized WAV shelled into whisper-cli | normalized WAV bytes |
| untrusted input → ffmpeg decode | Raw client audio decoded by ffmpeg in stage 1 | audio container bytes |
| model path selection | Model file path passed to whisper-cli `-m` | server-constant/injected path |
| whisper.cpp source + ggml model download → local toolchain | Setup builds from a pinned git tag and downloads a model over the network | git tag ref, model binary + checksum |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-30-01 | Tampering | `matchMP3` ID3v2 synchsafe decode | mitigate | Bounded `mp3PeekLen` (512 KiB); `tagEnd` past buffer/bound fails closed (return `false`, never grow/seek); adversarial oversized-size + truncated-header tests | closed |
| T-30-02 | Denial of Service | `ProbeDuration`/`EnforceMaxDuration` | mitigate | Declared duration validated in **float space** before conversion (post-review fix in `parseProbedDuration`): NaN/±Inf/negative/implausibly-huge rejected fail-closed; ceiling enforced via `ErrAudioDurationExceeded` before decode | closed |
| T-30-03 | Denial of Service | ffprobe subprocess | mitigate | Invoked through the existing hardened `runCommand` (process-group `Setpgid` + SIGKILL on ctx timeout); ctx carries a short bound distinct from the engine timeout (documented) | closed |
| T-30-04 | Spoofing | `matchM4A` brand check | mitigate | Closed `m4aBrands` allowlist reduced post-review to `M4A `/`M4B ` only (`isom`/`mp42` removed — those are ordinary MP4-video major brands); MP4-video-style ftyp explicitly tested as rejected | closed |
| T-30-SC | Tampering (supply chain) | whisper.cpp build + `ggml-base.bin` download | mitigate | `--branch v1.9.1` pinned clone; model SHA-256 pinned + `sha256sum -c` STOP-on-mismatch; `-DGGML_NATIVE=OFF`. Host-local toolchain (not in repo) — verified via 30-01-SUMMARY evidence, not grep | closed |
| T-30-05 | Tampering / Elevation of Privilege | `AudioOpts.Language` → whisper-cli argv | mitigate | Closed `audioLanguageAllowlist` map-lookup rejects out-of-allowlist values before parse returns; `Language` only ever reaches whisper-cli as a discrete argv slice element (`exec.Command`, no shell); injection test asserts `;`/`$(...)`/backtick payloads rejected pre-argv | closed |
| T-30-06 | Tampering | `ParseAudioOpts`/`AudioOptsFromMap` | mitigate | `checkStrictObject` reused verbatim (not duplicated) + `json.Decoder.DisallowUnknownFields()`; strictness identical on write path (`ParseAudioOpts`) and read path (`AudioOptsFromMap`) | closed |
| T-30-11 | Elevation of Privilege (path traversal) | future model-selector opt | accept (documented) | `AudioOpts` carries no path-shaped field this phase; doc comments in `audioopts.go` and `whisper.go` warn a future path-shaped opt must re-derive the allowlist/server-constant discipline rather than concatenating client bytes into a path | closed |
| T-30-07 | Denial of Service | whisper-cli/ffmpeg runtime | mitigate | Both `runCommand` calls in `Convert` share the single caller-supplied `ctx` (one timeout bound covers both stages); process-group SIGKILL on timeout via the existing hardened `runCommand` | closed |
| T-30-08 | Tampering / Denial of Service | ffmpeg decode of crafted input | mitigate | Hardened `runCommand`; ordering (sniff → duration guard → normalize → transcribe) is documented at the guard/pipeline boundary. **Note:** ordering is documented convention only, not enforced by any code path in this phase — actual invocation-order wiring is Phase 31's job (review IN-02, non-blocking) | closed (see residual note) |
| T-30-09 | Integrity / Information Disclosure | hallucination on silence | accept | Whisper exits 0 with structurally-valid garbage on silence/music; no `no_speech_prob` field exists in the pinned v1.9.1 binary. Recorded as accepted residual risk in `Convert`'s doc comment and the SUMMARY | closed |
| T-30-10 | Elevation of Privilege (path traversal) | whisper-cli `-m` model path | mitigate | `model()` returns `defaultAudioModelPath` (compile-time server constant) or an injected `modelPath` struct field — never client bytes | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Threat Verification Evidence

### Mitigate dispositions (grep-verified in cited implementation files)

| Threat ID | Evidence |
|-----------|----------|
| T-30-01 | `internal/convert/audiosniff.go:14` — `const mp3PeekLen = 512 * 1024`; `audiosniff.go:70-87` `matchMP3` computes `tagEnd` and returns `false` when `tagEnd < 0 \|\| tagEnd+1 >= len(b)` (line 80-82), and returns `false` for a truncated (`<10` byte) header (line 72-74) — never indexes out of bounds. Tests: `internal/convert/audiosniff_test.go:153` `TestMatchMP3_TruncatedID3Header_FailsClosed`, `:163` `TestMatchMP3_OversizedDeclaredSize_FailsClosed`, `:293` `TestSniffAudio_OversizedID3SizeFailsClosed`. |
| T-30-02 | `internal/convert/audioduration.go:31` — `const maxSaneDurationSeconds = 1 << 31`; `:38-47` `parseProbedDuration` rejects `math.IsNaN`/`math.IsInf`/`secs < 0`/`secs > maxSaneDurationSeconds` in float space, **before** the `time.Duration(secs * float64(time.Second))` conversion (post-review CR-01 fix, commit `2a10d70` per 30-REVIEW.md). Test: `internal/convert/audioduration_test.go:29` `TestParseProbedDuration_AdversarialValuesFailClosed` — covers `nan`/`NaN`/`inf`/`+inf`/`-inf`/`-1`/`-0.5`/`1e18`/`9223372036854775807`, all asserted to error with zero-value duration, deliberately platform-independent (does not require ffprobe). `EnforceMaxDuration` ceiling check: `audioduration.go:81-90`, tests `:72` `TestEnforceMaxDuration_UnderCeilingPasses`, `:83` `TestEnforceMaxDuration_OverCeilingRejected`. |
| T-30-03 | `internal/convert/audioduration.go:57-58` — `runCommand(ctx, "ffprobe", ...)` (verbatim reuse, no new exec wrapper: `grep -c 'func runCommand' internal/convert/audioduration.go` = 0). Hardening itself: `internal/convert/exec.go:28-56` — `Setpgid: true` (line 30) + `syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)` on `ctx.Done()` (line 47). Process-group-kill behavior is exercised by the pre-existing `internal/convert/libreoffice_test.go:515` (`runCommand` kill-on-timeout test, shared infrastructure reused unmodified for ffprobe/ffmpeg/whisper-cli). |
| T-30-04 | `internal/convert/audiosniff.go:24-27` — `m4aBrands = map[string]bool{"M4A ": true, "M4B ": true}` (post-review: `isom`/`mp42` confirmed ABSENT — `grep -c '"isom"\|"mp42"' internal/convert/audiosniff.go` inside the map literal returns 0; both strings only appear in doc-comment prose and test names). `matchM4A` (`:45-50`) does a plain map lookup against bytes 8-12 only. Tests: `internal/convert/audiosniff_test.go:63` `TestMatchM4A_ForeignBrandNotDetected` (asserts `qt  `/`mp41`/`isom`/`mp42` all rejected), `:78` `TestMatchM4A_MP4VideoStyleFtypRejected` (realistic `isom`-major MP4-video ftyp box with `isomiso2avc1mp41` compatible-brands list, asserted `matchM4A(...) == false`). |
| T-30-SC | Host-local toolchain, not committed to the repo — verified via `30-01-SUMMARY.md` evidence (per task instructions), not grep: frontmatter `provides:` line 10 "`whisper-cli v1.9.1 built locally (pinned tag, -DGGML_NATIVE=OFF)`"; body line 67 "`ggml-base.bin` downloaded and SHA-256-verified byte-for-byte against the pinned checksum (`60ed5bc3...fba2efe`)" matching `.planning/research/STACK.md:73`'s pinned checksum `60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe`. Plan step (d) requires STOP-on-mismatch via `sha256sum -c -`; SUMMARY records the check passed, no override taken. `-DGGML_NATIVE=OFF` confirmed in SUMMARY line 66 as "load-bearing" and applied. This control is inherently unverifiable by grep (it provisions a local dev-machine binary, not repo code); Phase 32 is documented as the phase that bakes an equivalent toolchain into the container image from source. |
| T-30-05 | `internal/convert/audioopts.go:20-27` — closed `audioLanguageAllowlist` map; `:68-70` `ParseAudioOpts` rejects `o.Language != "" && !audioLanguageAllowlist[o.Language]` before returning. `internal/convert/whisper.go:101-113` `whisperArgs` appends `"-l", lang` as discrete slice elements (never `fmt.Sprintf`/string-concat into a shell string); `runCommand`/`exec.Command` (`exec.go:29`) never invokes `/bin/sh`. Test: `internal/convert/audioopts_test.go:179` `TestAudioOpts_InjectionCannotReachArgv` — asserts `; rm -rf /`, `$(whoami)`, `` `whoami` `` all rejected by `ParseAudioOpts` (lines 186-194), plus a structural assertion (lines 196-215) that a hand-constructed bypass value stays a plain struct field. |
| T-30-06 | `internal/convert/audioopts.go:59` — `checkStrictObject(raw)` called (helper reused from `opts.go`, not duplicated: `grep -c 'func checkStrictObject' internal/convert/audioopts.go` = 0); `:64` `dec.DisallowUnknownFields()`. Read-path parity: `AudioOptsFromMap` (`:80-89`) re-marshals and calls `ParseAudioOpts`, applying identical strictness. Tests: `internal/convert/audioopts_test.go:8` `TestParseAudioOpts` (table-driven, includes unknown-field/dup-key/trailing-data/top-level-null cases per the plan's must_haves) and `:94` `TestAudioOptsFromMap` (round-trip + read-path parity). |
| T-30-07 | `internal/convert/whisper.go:137` — `Convert(ctx context.Context, ...)`; both subprocess calls use the same `ctx` parameter: `runCommand(ctx, "ffmpeg", ...)` at line 169 and `runCommand(ctx, "whisper-cli", args...)` at line 180 — no per-stage `context.WithTimeout` split, confirming "single caller-supplied ctx… one AUDIO_ENGINE_TIMEOUT bound covers both" (doc comment lines 117-119). Hardening: `runCommand`'s process-group SIGKILL (`exec.go:28-56`), same shared infrastructure as T-30-03. |
| T-30-08 | Hardened `runCommand` — same evidence as T-30-03/T-30-07 (`exec.go:28-56`), applied identically to the ffmpeg stage (`whisper.go:169-172`). Ordering: `internal/convert/audioduration.go:67-69` doc comment states "order matters: sniff -> duration guard -> normalize -> transcribe, never normalize-then-check". **Residual note (non-blocking, review IN-02):** this ordering is documented convention only — `AudioConverter.Convert` does not itself call `EnforceMaxDuration`, and no code path in this phase enforces the guard actually ran before `Convert`. This is consistent with the phase's stated scope fence (API/worker wiring deferred to Phase 31) and mirrors the existing dimensions-guard precedent (also wired at the worker level, not inside the converter). Recorded here so Phase 31's audit must show the guard is actually invoked before `Convert`, not just documented. |
| T-30-10 | `internal/convert/whisper.go:28` — `const defaultAudioModelPath = "/models/ggml-base.bin"` (compile-time constant); `:47-52` `model()` returns `c.modelPath` (injected, test-only field, never populated from client input — no code path assigns `modelPath` from `opts`/`inPath`/any client-controlled value) or falls back to the constant. `grep -n 'modelPath' internal/convert/whisper.go` shows only the struct field declaration, the `model()` accessor, and doc-comment prose — no assignment from `opts map[string]any` anywhere in `Convert`. |

### Accept dispositions (documentation verified)

| Threat ID | Verification |
|-----------|--------------|
| T-30-11 | `internal/convert/audioopts.go:41-45` — doc comment: "a future AudioOpts extension MUST re-derive this same allowlist/server-constant discipline rather than concatenating client bytes into a path"; `internal/convert/whisper.go:21-27` — `defaultAudioModelPath` doc comment: "the model is NEVER built from client bytes (Pitfall 11 / anti-pattern...)". No path-shaped field exists on `AudioOpts` today (`Language string`, `Translate bool` only — `audioopts.go:46-49`). Entry present in Accepted Risks Log below. |
| T-30-09 | `internal/convert/whisper.go:127-136` — `Convert`'s doc comment: "whisper-family models hallucinate (loop a short phrase) on silence/music/noise and exit 0 with a structurally-valid transcript. The pinned whisper-cli v1.9.1 binary exposes no no_speech_prob/avg_logprob field to catch this... This is logged here as an explicit accepted risk, not attempted as a build requirement of this phase." Also recorded in `internal/convert/audioduration.go:73-80` (duration-guard half) and `30-03-SUMMARY.md` line 38/98 ("Threat Flags" section explicitly confirms T-30-09 addressed as designed). Entry present in Accepted Risks Log below. |
| T-30-SC | See T-30-SC mitigate-evidence row above — the `accept`-adjacent piece (network fetch of a third-party model artifact) is closed by the SHA-256 pin + STOP-on-mismatch discipline (a `mitigate` control, not an accepted risk), so no separate accept-log entry is needed beyond noting the toolchain is host-local and Phase 32 is the container-image successor. |

---

## Unregistered Flags

None. `30-03-SUMMARY.md`'s `## Threat Flags` section (the only SUMMARY of the three plans containing that heading) explicitly states: "None - all threat model entries (T-30-07 through T-30-10) were already dispositioned by the plan and are addressed as designed." `30-01-SUMMARY.md` and `30-02-SUMMARY.md` contain no `## Threat Flags` section at all (grep-confirmed). No new attack surface was introduced beyond what the three plans' `<threat_model>` blocks already register — `AudioConverter` is not wired into any live routing path this phase (confirmed: `grep -c 'Default.Register(AudioConverter' internal/convert/converters.go` = 0), so no additional client-facing surface exists to flag.

The code-review findings WR-02 (unsupported-target-fails-fast) and WR-03 (empty-language-defaults-to-auto) and WR-04 (MIMEType missing audio input formats) are robustness/correctness fixes, not new attack-surface flags — WR-02 and WR-03 both harden existing T-30-07/T-30-08-adjacent behavior (fail fast before expensive subprocess calls; avoid a silent mis-transcription default), and WR-04 closes a Content-Type-correctness gap for a future (Phase 31) upload path, tracked here as informational since no audio upload path is live yet.

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-30-01 | T-30-11 | `AudioOpts` has no path-shaped field this phase (`Language`, `Translate` only); the risk only materializes if a future phase adds a model-selector or similar client-controllable path opt. Doc comments in `audioopts.go` and `whisper.go` place a discovery-time obligation on that future work to reuse the allowlist/server-constant discipline already proven for `Language`. No code path today accepts a client-controlled path. | Plan 02 author (register-authored-at-plan-time) | 2026-07-18 |
| AR-30-02 | T-30-09 | Whisper-family models (including the pinned v1.9.1 binary) hallucinate structurally-valid transcripts on silence/music/noise and exit 0; the pinned binary's `-oj`/`-ojf` JSON output has no `no_speech_prob`/`avg_logprob` field to use as a cheap detection signal (source-verified in `30-RESEARCH.md`). The duration guard (T-30-02) bounds resource consumption but cannot detect content-level hallucination. `--vad`/`--vad-model` flags are noted as a possible future mitigation lever in `Convert`'s doc comment, not built this phase. Bounded blast radius: a hallucinated transcript is wrong output, not a security compromise (no code execution, no data exfiltration) — classified Integrity/Information-Disclosure, accepted at documentation-only mitigation for this phase. | Plan 01/03 authors (register-authored-at-plan-time) | 2026-07-18 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-18 | 12 | 12 | 0 | gsd-security-auditor |

**Audit notes:**
- All 3 plan `<threat_model>` blocks loaded (30-01, 30-02, 30-03); no duplicate threat IDs across plans.
- Verified against the CURRENT (post-code-review) implementation, not the plans' originally-declared mechanisms, per the task's explicit instruction that CR-01/WR-01 changed two mitigations after the plans were written:
  - T-30-02: plan declared "duration rejected against ceiling BEFORE decode"; the SHIPPED mechanism additionally validates in float space (`parseProbedDuration`) to close an amd64-only implementation-defined float→int64 overflow bypass (CR-01, commit `2a10d70`) that the plan's original text did not anticipate. Verified the fixed code, not the stale plan text.
  - T-30-04: plan/RESEARCH originally specified `m4aBrands` = `{M4A , M4B , isom, mp42}`; the SHIPPED allowlist (post WR-01 fix, commit `2a02140`) is `{M4A , M4B }` only — `isom`/`mp42` REMOVED because they are ordinary MP4-video major brands, not m4a-specific. Verified `isom`/`mp42` are grep-confirmed ABSENT from the map literal and that `TestMatchM4A_MP4VideoStyleFtypRejected` proves the video-misdetection case is now closed.
- T-30-SC (supply chain) verified via `30-01-SUMMARY.md` prose evidence per task instruction (host-local toolchain provisioning is not repo code and cannot be grepped) — cross-checked the recorded SHA-256 against `.planning/research/STACK.md`'s independently-pinned value; both match.
- T-30-08's mitigation plan says "documented ordering sniff → duration guard → normalize → transcribe" (not "enforced ordering") — verified the documentation exists but flagged the residual gap (review IN-01/IN-02, non-blocking per the review's own `info`-severity classification) as a note carried into Phase 31's audit scope, since `AudioConverter.Convert` does not itself invoke `EnforceMaxDuration`.
- No `unregistered_flag` entries found — `30-03-SUMMARY.md`'s `## Threat Flags` section explicitly confirms all four of its scoped threats (T-30-07..T-30-10) were already registered; `30-01-SUMMARY.md`/`30-02-SUMMARY.md` have no such section.
- Implementation files (`audiosniff.go`, `audioduration.go`, `audioopts.go`, `whisper.go`, `convert.go`, `sniff.go`, `exec.go`, and all `_test.go` siblings) were read-only for this audit; no code was modified.
- `AudioConverter` remains unregistered from `convert.Default` this phase (by design) — no live client-facing attack surface exists yet for the mitigate-disposition threats above; this audit verifies the CONTROLS are correctly built and tested ahead of Phase 31's wiring, not that they are currently exposed to traffic.

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-18
