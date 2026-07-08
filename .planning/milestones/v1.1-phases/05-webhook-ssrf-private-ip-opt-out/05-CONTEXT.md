# Phase 5: Webhook SSRF Private-IP Opt-Out - Context

**Gathered:** 2026-07-08
**Status:** Ready for planning

<domain>
## Phase Boundary

Operators running OctoConv where internal client services live on private IP space (RFC1918) can enable webhook delivery to `callback_url` targets in that space, via an explicit opt-in config flag — without weakening the default-safe SSRF posture for everyone else. This phase covers: a new `WEBHOOK_ALLOW_PRIVATE_IPS` env var that narrowly relaxes `isBlockedIP`'s RFC1918 check only, plus a startup-time visibility signal when the flag is enabled. It does NOT cover: loosening the loopback, link-local (including the `169.254.169.254` cloud metadata endpoint), or unspecified-address blocks — those remain hard-blocked regardless of this flag, there is no legitimate `callback_url` use case for them. Also out of scope: per-client opt-out (this is a single global operator-level flag, matching the existing `WEBHOOK_ALLOW_INSECURE_HTTP` pattern), and any change to the scheme/DNS-resolution logic already in `validateCallbackURL`.

</domain>

<decisions>
## Implementation Decisions

### Flag scope
- **D-01:** `WEBHOOK_ALLOW_PRIVATE_IPS=true` relaxes ONLY the `addr.IsPrivate()` (RFC1918) check inside `isBlockedIP`. `addr.IsLoopback()`, `addr.IsLinkLocalUnicast()`, `addr.IsLinkLocalMulticast()`, and `addr.IsUnspecified()` remain hard-blocked unconditionally, flag or no flag — there is no legitimate internal-service scenario for a `callback_url` pointing at loopback, link-local (which covers the `169.254.169.254` cloud metadata SSRF vector), or the unspecified address, even on a fully internal deployment.
- **D-02:** Default is disabled (unset/false) — matches the existing `WEBHOOK_ALLOW_INSECURE_HTTP` convention (safe-by-default, explicit opt-in for the internal-network case).
- **D-03:** This is a single global, operator-level flag (env var, no config file, no per-client override) — matches D-01's naming and the project's existing environment-variable-only configuration convention. No new per-client schema/API surface.

### Visibility
- **D-04:** When `WEBHOOK_ALLOW_PRIVATE_IPS=true` at startup, `cmd/api/main.go` logs a one-time `log.Printf` warning that private-IP SSRF blocking is disabled — this is a deliberate, scoped exception to "only `cmd/*/main.go` logs, `internal/*` never logs" (already true here, no `internal/*` change needed) and gives an operator glancing at logs a clear signal the safety posture is relaxed, without requiring them to read `.env` or source to notice.

### Claude's Discretion
- Exact wording of the startup log line (D-04) — follow the existing emoji-prefixed startup-message convention (`cmd/api/main.go`'s `🚀`/`🛑` style) or a plain `⚠` prefix, planner/executor to decide based on what reads clearest.
- Whether the check lives inline in `isBlockedIP` (reading `os.Getenv` directly, matching `validateCallbackURL`'s existing `WEBHOOK_ALLOW_INSECURE_HTTP` inline-read pattern) or is threaded through as a parameter — technical detail, follow whichever existing pattern in `callbackurl.go` is more consistent.

</decisions>

<canonical_refs>
## Canonical References

**Downstream agents MUST read these before planning or implementing.**

### Project & Requirements
- `.planning/PROJECT.md` — Current Milestone v1.1 section, Key Decisions table (SSRF row marked ⚠️ Revisit, now being addressed here)
- `.planning/REQUIREMENTS.md` — `WEBHOOK-06` (locked v1.1 scope for this phase)
- `.planning/ROADMAP.md` — Phase 5 goal, success criteria

### Prior Phase Context (the decision being revisited)
- `.planning/milestones/v1.0-phases/02-webhook-delivery/02-CONTEXT.md` — D-03: the original SSRF guard decision this phase narrowly relaxes. Rationale for the original hard-block and the internal-only-clients context that motivated it.
- `.planning/milestones/v1.0-MILESTONE-AUDIT.md` — the specific tech-debt finding that triggered this phase (SSRF validation could make webhooks undeliverable on a real internal network using private addressing)

### Existing Codebase (reference patterns to follow)
- `internal/api/callbackurl.go` — `validateCallbackURL` and `isBlockedIP`, the exact functions this phase modifies. `WEBHOOK_ALLOW_INSECURE_HTTP` (lines ~29-36) is the direct pattern to mirror for `WEBHOOK_ALLOW_PRIVATE_IPS` — same inline `os.Getenv` read, same boolean-flag style.
- `cmd/api/main.go` — where the D-04 startup log line lands; existing emoji-prefixed log.Printf startup-message convention to follow or deliberately deviate from with a `⚠` prefix.
- `.env.example` — where `WEBHOOK_ALLOW_INSECURE_HTTP` is already documented; `WEBHOOK_ALLOW_PRIVATE_IPS` should be documented immediately alongside it with the same style.
- `internal/api/callbackurl_test.go` (referenced in Phase 2's review as having zero coverage for `WEBHOOK_ALLOW_INSECURE_HTTP` via `t.Setenv` — IN-01 finding) — this phase's tests for the new flag should not repeat that gap; cover both flag-on and flag-off behavior explicitly.

</canonical_refs>

<code_context>
## Existing Code Insights

### Reusable Assets
- `isBlockedIP(addr netip.Addr) bool` — single choke point already separating the four blocked categories (loopback/private/link-local-unicast/link-local-multicast/unspecified) as independent boolean checks; D-01 just needs the `addr.IsPrivate()` term made conditional on the new flag, the rest of the function is untouched.
- `os.Getenv("WEBHOOK_ALLOW_INSECURE_HTTP") == "true"` — exact string-comparison idiom to copy for `WEBHOOK_ALLOW_PRIVATE_IPS`.

### Established Patterns
- Environment-variable-only configuration, `os.Getenv` read at the point of use (not threaded through a config struct) — `validateCallbackURL`'s existing `WEBHOOK_ALLOW_INSECURE_HTTP` check is the model.
- Only `cmd/*/main.go` logs — `internal/api/callbackurl.go` itself must not gain a `log.Printf`; the D-04 visibility signal belongs in `cmd/api/main.go`'s startup sequence, checking the same env var and logging once.

### Integration Points
- `internal/api/callbackurl.go` `isBlockedIP` — where D-01's conditional relaxation lands
- `cmd/api/main.go` — where D-04's startup warning log lands
- `.env.example` — new `WEBHOOK_ALLOW_PRIVATE_IPS` entry, documented as disabled-by-default

</code_context>

<specifics>
## Specific Ideas

No UI/UX references — backend-only, single-file-scoped phase. The concrete ask is narrow: one new boolean env var that relaxes exactly the RFC1918 check (not loopback/link-local/unspecified), off by default, with a startup log when enabled.

</specifics>

<deferred>
## Deferred Ideas

- **Per-client opt-out for private-IP webhooks** — not raised this phase; the current global-flag model matches `WEBHOOK_ALLOW_INSECURE_HTTP`'s existing precedent and there's no signal yet that different clients need different SSRF postures.
- **Re-resolving/re-validating `callback_url` before each delivery attempt (DNS-rebinding protection)** — still explicitly out of scope, unchanged from Phase 2's original D-03 acceptance of this residual risk for internal-only clients.

</deferred>

---

*Phase: 5-Webhook SSRF Private-IP Opt-Out*
*Context gathered: 2026-07-08*
