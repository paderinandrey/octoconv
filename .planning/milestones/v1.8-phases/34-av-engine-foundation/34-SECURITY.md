---
phase: 34
slug: 34-av-engine-foundation
status: verified
threats_open: 0
asvs_level: 2
created: 2026-07-20
---

# Phase 34 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> Audit method: every `mitigate` disposition verified by direct read/grep of the
> implementation at HEAD plus execution of its pinning tests — not from SUMMARY/plan
> prose. Post-execution review fixes (34-REVIEW.md → 34-REVIEW-FIX.md, commits
> `d60ac84`, `123189a`, `5ceb898`, `64386de`) were independently confirmed present
> in code.

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| uploaded file bytes → sniffer | Untrusted container bytes inspected (fixed-window + bounded-peek EBML) before any storage write or ffmpeg decode | raw attacker-controlled bytes |
| client JSON opts → AVOpts parse | Untrusted client JSON strict-parsed and allowlist-validated before influencing any ffmpeg argv | client-supplied JSON |
| uploaded file → ffprobe probes (duration / resolution / audio-codec) | Untrusted container metadata read by hardened, protocol-whitelisted ffprobe before the expensive stage | container metadata |
| uploaded file bytes → ffmpeg subprocess | Untrusted container decoded/encoded by external engine via runCommand | raw attacker-controlled bytes |
| persisted opts (jobs.options) → AVConverter.Convert | Persisted opts re-parsed strictly on the converter read path (D-10 parity) | previously-validated client JSON |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-34-01 | Spoofing | matchMP4/matchMOV/matchAVI/matchEBML | mitigate | Fail-closed magic-bytes matching keyed on brand/DocType, never extension: `avsniff.go:28-52` (ftyp+`mp4VideoBrands`/`"qt  "`/RIFF+`"AVI "`), `avsniff.go:141-191` (EBML DocType). Rejection tests `TestMatchMP4_RejectsM4ABrand`/`RejectsHEICBrand`/`RejectsQuickTimeBrand`, `TestMatchAVI_RejectsWAV` pass | closed |
| T-34-02 | Tampering | Cross-sniffer brand collision | mitigate | `TestVideoBrandDisjointness` (`avsniff_test.go:123-140`) asserts pairwise-empty intersection of `mp4VideoBrands`/`m4aBrands`/`heicBrands`; `mp4VideoBrands` doc comment (`avsniff.go:8-14`) codifies the invariant; test passes at HEAD | closed |
| T-34-03 | Denial of Service | matchEBML declared-length parsing | mitigate | Bounded 4KiB peek `avPeekLen` (`avsniff.go:62`); every declared vint size/offset compared in **uint64 space** before narrowing (`avsniff.go:157-159,175-177`, WR-03 fix `d60ac84` closing the 32-bit truncation bypass); never grows buffer or seeks. `TestMatchEBML_RejectsOversizedElementSize` + `TestMatchEBML_RejectsHugeSizeVint` (0x80000000/0x100000000/2^56-1) pass | closed |
| T-34-04 | Tampering | Malformed/truncated EBML header | mitigate | `matchEBML` returns `("", false)` on truncation, unrecognized DocType, or missing DocType within the window (`avsniff.go:162-190`) — never guesses. `TestMatchEBML_RejectsTruncated`/`RejectsUnknownDocType`/`RejectsNonEBML` pass | closed |
| T-34-05 | Tampering | ParseAVOpts (client bytes → argv) | mitigate | `ParseAVOpts` (`avopts.go:99-119`): `checkStrictObject` (reused, `avopts.go:100`) + `DisallowUnknownFields` + closed `avResolutionHeights`/`avCodecAllowlist` maps + timecode range check incl. NaN/±Inf rejection (`avopts.go:109`). Validated values only select server constants (`avScaleFilter`, `x264DefaultCRF`/`x265DefaultCRF`, fixed codec literals) — no client string concatenated into argv. `TestParseAVOpts*` pass | closed |
| T-34-06 | Elevation of Privilege | Filtergraph/source-filter injection | mitigate | `AVOpts` is a closed typed struct (`avopts.go:69-90`). The only `-vf` in the package is `avScaleFilter` output built from the validated int enum (`av.go:84-89,123-124,144-145`); zero `-filter_complex` or `-safe` anywhere in `internal/convert` (grep-confirmed) | closed |
| T-34-07 | Denial of Service | Decode bomb (huge resolution) | mitigate | `probeVideoStreams` + `EnforceMaxResolution`/`enforceMaxResolutionOf` (`avduration.go:71-182`) reject over-ceiling declared resolution before the expensive stage; post-CR-03, the guard uses `avMaxVideoHeight` across **all** streams (cover art included), so a bomb hidden behind a small `v:0` cover-art stream trips the ceiling (`avduration.go:127-140`). `TestProbeVideoStream_IgnoresCoverArt` (CR-03 regression, MKV fixture) passes | closed |
| T-34-08 | Information Disclosure | SSRF/LFI during resolution probe | mitigate | `ffprobeStreamArgs` carries `-protocol_whitelist file,crypto` + `file:`-prefixed path (`avduration.go:60-64`); pinned by `TestFfprobeStreamArgs_Hardening` (passes) | closed |
| T-34-08b | Information Disclosure | SSRF/LFI during reused duration probe | mitigate | `ffprobeDurationArgs` carries `-protocol_whitelist file,crypto` + existing `file:` prefix (`audioduration.go:75-78`); pinned by `TestFfprobeDurationArgs` (passes). Repo-wide invariant note: see WR-08 entry below | closed |
| T-34-09 | Tampering | x265 inheriting x264 CRF | mitigate | Two distinct constants `x264DefaultCRF=23` / `x265DefaultCRF=28` (`avopts.go:52,59`); HEVC branch references `x265DefaultCRF` only (`av.go:117-120`); `TestCRFConstantsDistinct` + `TestHEVCUsesX265CRF` pass | closed |
| T-34-10 | Information Disclosure | SSRF/LFI via HLS/concat/subtitle refs in container content | mitigate | Every ffmpeg/ffprobe argv builder in `av.go` carries `-nostdin -protocol_whitelist file,crypto` + `file:`-prefixed input AND output (via `avInputArgs` `av.go:97-102`; `thumbnailArgs` `av.go:211-217`; `ffprobeAudioCodecArgs` `av.go:299-303`). Deterministic regression pin: `TestAVBuildersHardenEveryInvocation` (`av_test.go:149-175`) asserts the flag pair + both path prefixes across all 5 builders. Post-WR-10 canary `TestProtocolWhitelist_BlocksHTTP_Canary` (`av_test.go:769-807`) drives the crafted HLS `http://` playlist through the **production** builders (`transcodeToMP4Args`/`streamCopyArgs`/`thumbnailArgs`) and the full `AVConverter{}.Convert` entry point — genuinely load-bearing. No `-safe 0` / concat demuxer anywhere (grep-confirmed) | closed |
| T-34-11 | Tampering | Codec-mismatch silent contract violation (VP9/Opus remuxed into mp4) — was DEFECTIVE per CR-03 | mitigate | Fix `5ceb898` verified at HEAD: (1) `streamCopyArgs` pins `-map 0:<videoIndex> -map 0:a:0` so exactly the two gate-inspected streams are muxed (`av.go:161-170`); transcode builders carry matching `-map` (`av.go:121-122,142-143`); (2) `ffprobeStreamArgs` selects **every** video stream with `disposition=attached_pic` (`avduration.go:60-64`), `avPrimaryVideoStream` picks largest-area non-cover-art (`avduration.go:104-125`), guard reads the real stream. `avStreamCopyLegal` remains project-owned allowlist (`av.go:284-293`), gated through `avStreamCopyEligible` (CR-02, `av.go:495-509`) so explicit codec/resize requests disqualify the copy. `TestStreamCopyArgs_MapsExactlyTheGatedStreams`, `TestAVStreamCopyLegal`, `TestAVStreamCopyEligible`, `TestProbeVideoStream_IgnoresCoverArt`, `TestAVConverter_VP9SourceToMP4_ReEncodes` all pass | closed |
| T-34-12 | Denial of Service | Multi-axis decode bomb (duration × resolution) | mitigate | Guard stage in `Convert` (`av.go:388-400`): `avProbeSource` (single probe, 15s `avProbeTimeout` child ctx — WR-05) then `enforceMaxDurationOf(4h)` AND `enforceMaxResolutionOf(4320, max-across-all-streams)` run strictly BEFORE `convertTranscode`/`convertAudioExtract`/`convertThumbnail`; both fail-closed. Cover-art resolution bypass closed per T-34-11 evidence | closed |
| T-34-13 | Elevation of Privilege | Opts injection reaching ffmpeg argv/filtergraph | mitigate | `AVOptsFromMap` strict-parses persisted opts as first line of `Convert` (`av.go:372`, round-trips through `ParseAVOpts`, `avopts.go:126-135`); validated values select fixed argv templates; `runCommand` uses `exec.Command` — never a shell (`exec.go:29`) | closed |
| T-34-14 | Denial of Service | Runaway ffmpeg on crafted input | mitigate | `runCommand` sets `Setpgid` and SIGKILLs the whole process group on ctx cancel/timeout (`exec.go:30,45-49`); every AV invocation goes through it. Probe stage additionally bounded by `avProbeTimeout` (`av.go:258,430`) | closed |
| T-34-15 | Tampering | Thumbnail out-of-range `-ss` silent no-output — was DEFECTIVE per CR-04 | mitigate | Fix `5ceb898` verified at HEAD: `AVOpts.Timecode` is `*float64` (`avopts.go:80`) distinguishing unset from explicit `0`; unset default clamped to `min(1.0, duration/2)` so sub-second sources convert (`av.go:554-559`); explicit out-of-range rejected pre-flight with distinct input-fault sentinel `ErrAVTimecodeOutOfRange` (`av.go:269-276,548-553`, WR-04 — not folded into the engine-fault class); `validateAVOutput` maps missing file and zero-byte identically to `ErrAVOutputMissingOrEmpty` and re-`Sniff()`s thumbnail output (`av.go:339-363`). `TestAVConverter_Thumbnail_OutOfRangeSS`/`_SubSecondSource`/`_ExplicitZeroTimecode`, `TestValidateAVOutput_SniffMismatch`, `TestParseAVOpts_TimecodeUnsetVsExplicitZero` all pass | closed |
| T-34-SC | Tampering | Package-manager supply chain | accept | See Accepted Risks Log AR-34-01 | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-34-01 | T-34-SC | No package-manager dependencies added in Phase 34 — verified: zero commits touching `go.mod`/`go.sum` since phase start (git log). ffmpeg/ffprobe are OS binaries invoked via `runCommand`. Decoder-RCE version pinning (CVE-2026-8461 "PixelSmash") is explicitly Phase 36 scope (`Dockerfile.av-worker`); no Docker surface for the AV engine exists in Phase 34, and `AVConverter` is unregistered so no untrusted input reaches ffmpeg in production yet | gsd-security-auditor (per plan-time disposition, 34-01/02/03 PLAN threat models) | 2026-07-20 |
| AR-34-02 | T-34-10 (residual) | Concat-demuxer local-file-read residual: scoped out per 34-03-PLAN — this phase uses no concat demuxer and never passes `-safe 0` (grep-confirmed zero occurrences in `internal/convert`). Re-check if any future phase introduces concat/playlist-driven inputs | gsd-security-auditor (per 34-03-PLAN threat model) | 2026-07-20 |

*Accepted risks do not resurface in future audit runs.*

---

## Unregistered Flags / Observations

No `## Threat Flags` sections exist in 34-01/02/03 SUMMARY.md — nothing to map.
Observations recorded for Phase 35 (informational, not blockers):

1. **WR-08 (repo-wide AVE-02 invariant) — FIXED at HEAD.** The "protocol-whitelist on
   every ffmpeg/ffprobe invocation" claim was false at review time: Phase 30's
   `whisper.go` `ffmpegNormalizeArgs` ran ffmpeg on untrusted audio with neither
   `-protocol_whitelist` nor `-nostdin`. Out-of-phase fix commit `64386de` verified
   present: `whisper.go:182-183` now carries `-y -nostdin -protocol_whitelist
   file,crypto -i file:<in> ... file:<norm>`. The invariant now holds repo-wide.
2. **Deliberate Phase 34/35 scope fences (NOT gaps):** `AVConverter` is intentionally
   unregistered (`grep -c 'AVConverter{}' internal/convert/converters.go` → 0) and
   `SniffVideo` has zero production callers (grep-confirmed). Consequence: mkv/webm
   are undetectable at upload while mp4/mov/avi are live in `sniff.go`'s signatures
   table — both halves fail closed in the interim (documented in `sniff.go:40-48`,
   WR-11 fix `123189a`). **Phase 35 must** wire `SniffVideo` into the handlers chain
   (off `rest`, not `file`) in the same change that registers `AVConverter`, and carry
   IN-01 (generic-brand `.m4a` misdetected as `mp4` → reclassify when no video stream).
3. **Phase 36 dependencies:** guard ceilings (`avMaxSourceDuration=4h`,
   `avMaxSourceResolutionHeight=4320`) are plain constants with env wiring deferred;
   ffmpeg version pinning for decoder-RCE defense lands with `Dockerfile.av-worker`.
4. **Canary nuance (recorded for future auditors):** `TestProtocolWhitelist_BlocksHTTP_Canary`
   asserts a non-zero ffmpeg exit, which an offline network failure could also produce;
   the *deterministic* regression pin for flag deletion is
   `TestAVBuildersHardenEveryInvocation`'s literal argv assertion across all five
   builders. Both exist at HEAD; keep both.

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-07-20 | 17 | 17 | 0 | gsd-security-auditor (Claude) |

Threat count: 16 unique register entries across the three plans (T-34-01..15 incl.
T-34-08b) + T-34-SC (deduplicated; appears in all three plans with identical
disposition) = 17 rows verified, counting T-34-08 and T-34-08b separately.

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-07-20
