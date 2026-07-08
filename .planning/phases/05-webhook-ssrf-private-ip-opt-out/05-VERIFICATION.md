---
phase: 05-webhook-ssrf-private-ip-opt-out
verified: 2026-07-08T02:31:33Z
status: passed
score: 4/4 success criteria verified (7/7 PLAN must_haves truths verified)
overrides_applied: 0
---

# Phase 5: Webhook SSRF Private-IP Opt-Out Verification Report

**Phase Goal:** Operators running on internal private networks can enable webhook delivery to private-IP `callback_url` targets via an explicit config flag, without weakening the default-safe SSRF posture for everyone else.
**Verified:** 2026-07-08T02:31:33Z
**Status:** passed
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| SC1 | Flag unset (default) → RFC1918/loopback/link-local `callback_url` still rejected with 400 | VERIFIED | `internal/api/callbackurl.go:77-84` — `isBlockedIP` unconditionally blocks loopback/link-local/unspecified; `IsPrivate()` is only skipped when `allowPrivate` is true, so with the env var unset/false, all three categories remain blocked. `internal/api/handlers.go:155-161` returns `http.StatusBadRequest` (400) with "invalid callback_url" when `validateCallbackURL` errors. `TestValidateCallbackURL` (loopback, cloud-metadata cases) and `TestIsBlockedIPAllowPrivate/flag_off` both pass, confirming no regression from v1.0 posture. |
| SC2 | Flag=true → RFC1918 `callback_url` succeeds at job creation and webhook is actually delivered; loopback/link-local (incl. `169.254.169.254`) remain rejected even with flag on | VERIFIED | `isBlockedIP` gates only `addr.IsPrivate()` on `!allowPrivate` (line 80); `IsLoopback()`, `IsLinkLocalUnicast()`, `IsLinkLocalMulticast()`, `IsUnspecified()` are unconditional (D-01). Confirmed via `go doc net/netip Addr.IsPrivate`: this is exactly RFC1918 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) + RFC4193 IPv6 ULA — no broader ranges (e.g. CGNAT 100.64.0.0/10) are accidentally swept in. `TestIsBlockedIPAllowPrivate/flag_on` asserts 10.0.0.1/192.168.0.1/172.16.0.1 become allowed while 127.0.0.1/169.254.169.254/0.0.0.0 stay blocked — all pass. Traced the delivery path (`internal/webhook/deliver.go` `Deliverer.Deliver`, called from `internal/worker/worker.go:225` `HandleWebhookDeliver`) and confirmed there is no second SSRF/IP re-check between job-creation validation and the actual outbound POST — once `validateCallbackURL` passes with the flag on, delivery proceeds unimpeded to the private address. The delivery mechanism itself (HTTP POST + retry/backoff) is unchanged by this phase and was already established/tested in Phase 2 (v1.0); this phase's only functional change is the SSRF gate, which is unit-tested directly. See Notes below regarding a recommended (non-blocking) live smoke test. |
| SC3 | Flag=true → non-https schemes and syntactically invalid URLs still rejected | VERIFIED | `validateCallbackURL` gates `http` scheme on the *separate* `WEBHOOK_ALLOW_INSECURE_HTTP` flag (line 29-39), untouched by this phase's `WEBHOOK_ALLOW_PRIVATE_IPS` change. `TestValidateCallbackURLAllowPrivate` (flag=true) asserts `http://10.0.0.1/hook` still errors, `not-a-url` and `""` still error, while `https://10.0.0.1/hook` succeeds — all pass. |
| SC4 | New env var + default (disabled) documented in `.env.example` | VERIFIED | `.env.example:28` — `WEBHOOK_ALLOW_PRIVATE_IPS=false   # opt-in: allow webhook callback_url to target RFC1918 private-IP addresses; loopback/link-local/unspecified remain hard-blocked regardless (default false, D-01/D-02)`. Matches existing `key=value   # comment` style. Drive-by fix also documented the previously-undocumented `WEBHOOK_ALLOW_INSECURE_HTTP=false` (line 27). |

**Score:** 4/4 ROADMAP success criteria verified.

### PLAN.md Frontmatter Must-Haves (Truths)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Flag unset → RFC1918/loopback/link-local rejected (unchanged v1.0) | VERIFIED | See SC1 above |
| 2 | Flag=true → isBlockedIP returns false for RFC1918 | VERIFIED | See SC2 above |
| 3 | Flag=true → loopback/link-local (incl. 169.254.169.254)/unspecified remain blocked | VERIFIED | See SC2 above |
| 4 | Flag=true → non-https schemes and invalid URLs still rejected | VERIFIED | See SC3 above |
| 5 | Startup emits one-time warning when flag enabled (D-04) | VERIFIED | `cmd/api/main.go:90-92` — `if os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true" { log.Printf("⚠️  WEBHOOK_ALLOW_PRIVATE_IPS=true: ...") }`, placed before `api.NewServer(...)` per D-04 placement requirement. Uses `⚠️` prefix as specified (not one of the reserved `🚀`/`📊`/`🛑` lifecycle emojis). Purely conditional log statement — mechanically verifiable by inspection, no runtime ambiguity. |
| 6 | WEBHOOK_ALLOW_PRIVATE_IPS documented in .env.example with default false | VERIFIED | See SC4 above |
| 7 | Flag read inline via os.Getenv in isBlockedIP, no config struct/per-client column/API surface | VERIFIED | `internal/api/callbackurl.go:78` — `allowPrivate := os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true"` inline in the function body; no signature change to `isBlockedIP` or `validateCallbackURL`, no new struct fields anywhere in `internal/jobs`, `internal/api/api.go`, or DB schema. |

**Score:** 7/7 PLAN must-haves truths verified.

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/api/callbackurl.go` | Conditional RFC1918 check, unconditional others | VERIFIED | Exists, substantive (real conditional logic, not a stub), wired (called from `handlers.go:157`) |
| `internal/api/callbackurl_test.go` | Flag-on/flag-off test coverage via `t.Setenv` | VERIFIED | Contains `TestIsBlockedIPAllowPrivate` and `TestValidateCallbackURLAllowPrivate`, both using `t.Setenv("WEBHOOK_ALLOW_PRIVATE_IPS", "true")`; all subtests pass (`go test ./internal/api/... -run 'TestIsBlockedIP\|TestValidateCallbackURL' -v` — 20/20 subtests PASS) |
| `cmd/api/main.go` | D-04 startup warning | VERIFIED | `⚠️`-prefixed `log.Printf` gated on the env var, placed at lines 90-92 before server construction |
| `.env.example` | `WEBHOOK_ALLOW_PRIVATE_IPS=false` documented | VERIFIED | Line 28, correct default and explanatory comment |

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| `internal/api/callbackurl.go` (isBlockedIP) | `WEBHOOK_ALLOW_PRIVATE_IPS` env var | `os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS")` inline read | WIRED | Line 78, read and used to gate `IsPrivate()` on line 80 |
| `cmd/api/main.go` | `WEBHOOK_ALLOW_PRIVATE_IPS` env var | `os.Getenv` startup check gating warning log | WIRED | Line 90, checked and used to conditionally log |
| `internal/api/handlers.go` | `validateCallbackURL` (→ `isBlockedIP`) | direct function call at job creation | WIRED | Line 157, called before storage write, 400 on error |
| `internal/worker/worker.go` (HandleWebhookDeliver) | `internal/webhook/deliver.go` (Deliverer.Deliver) | direct call, no re-validation | WIRED, no re-block | Confirmed delivery path does not re-check `isBlockedIP` post-validation — the opt-in flows through end-to-end for the address itself |

### Boundary Precision Check (flagged during plan-checking as needing scrutiny)

This was explicitly called out as the risk area: does the opt-out relax *exactly* RFC1918, nothing broader or narrower?

- `isBlockedIP` gates **only** the `addr.IsPrivate()` term — verified by reading the full function body (`internal/api/callbackurl.go:77-84`) and confirming `IsLoopback()`, `IsLinkLocalUnicast()`, `IsLinkLocalMulticast()`, `IsUnspecified()` are combined with `||` outside any conditional.
- `net/netip Addr.IsPrivate()` is documented (`go doc net/netip Addr.IsPrivate`) to cover exactly RFC 1918 (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16) plus RFC 4193 IPv6 ULA (fc00::/7) — no CGNAT (100.64.0.0/10), no broader "internal-looking" heuristics. This matches the D-01 decision's intended scope precisely (the phase context only discusses IPv4 RFC1918; the IPv6 ULA companion range is an accepted, unavoidable consequence of using the stdlib helper — not a scope violation, and IPv6 handling is untouched by prior phases too).
- `169.254.169.254` (cloud metadata) falls under `IsLinkLocalUnicast()` (169.254.0.0/16), which is untouched by the flag — explicitly tested in both `TestIsBlockedIP` (flag absent) and `TestIsBlockedIPAllowPrivate/flag_on` (flag present).
- No code path threads the flag through `validateCallbackURL`'s scheme/format/DNS-resolution logic — confirmed by reading lines 16-67: the flag is read only inside `isBlockedIP`, called identically from both the IP-literal branch (lines 46-51) and the DNS-resolved branch (lines 57-65), so the relaxation applies consistently regardless of whether the client supplied an IP literal or a hostname that resolves to a private address.

**Verdict: the security boundary is exactly as narrow as intended — RFC1918 only, nothing more, nothing less.**

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| WEBHOOK-06 | 05-01-PLAN.md | Operator can disable RFC1918 SSRF block via `WEBHOOK_ALLOW_PRIVATE_IPS`; loopback/link-local/unspecified always blocked | SATISFIED (code) | All artifacts and truths above. **Note:** `.planning/REQUIREMENTS.md` line 12 still shows the `WEBHOOK-06` checkbox unchecked (`- [ ]`) and the tracking table (line 38) still says "Pending" — this is a documentation-bookkeeping lag (last touched by commit `78521c2`, before the phase was executed), not a functional gap. The code itself fully satisfies the requirement text. Recommend updating REQUIREMENTS.md as a trivial follow-up; does not block this verification. |

### Anti-Patterns Found

None. Scanned all four modified files (`internal/api/callbackurl.go`, `internal/api/callbackurl_test.go`, `cmd/api/main.go`, `.env.example`) for `TBD|FIXME|XXX|TODO|HACK|PLACEHOLDER` and placeholder-language patterns — zero matches. No `log.` calls added to `internal/api/callbackurl.go` (project convention: internal/* never logs) — confirmed via grep, zero matches. `go vet ./...` clean across the whole module.

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Flag off: RFC1918/loopback/link-local rejected | `go test ./internal/api/... -run TestIsBlockedIP -v` | 8/8 (+2 grouped) subtests PASS | PASS |
| Flag on: RFC1918 allowed, hard-blocks unchanged | `go test ./internal/api/... -run TestIsBlockedIPAllowPrivate -v` | 2/2 subtests PASS | PASS |
| Flag on: scheme/invalid-URL validation unaffected | `go test ./internal/api/... -run TestValidateCallbackURLAllowPrivate -v` | 5/5 subtests PASS | PASS |
| Full module vet | `go vet ./...` | clean, exit 0 | PASS |

### Probe Execution

No `scripts/*/tests/probe-*.sh` files found in the repository and none referenced in the PLAN/SUMMARY. Step 7c: SKIPPED (no probes declared or found).

### Human Verification Required

None. All success criteria and must-haves are verified directly against source and passing unit tests that exercise the exact code paths this phase modified (`isBlockedIP`, `validateCallbackURL`). This is a pure backend config-flag change with no UI, no new external dependency, and no change to the already-tested (Phase 2) delivery mechanism itself — a live docker-compose smoke test is optional/recommended for extra operator confidence but is not required to establish that the code correctly implements the stated goal.

### Gaps Summary

No functional gaps. All four ROADMAP success criteria and all seven PLAN must-haves truths are verified directly against the source in `internal/api/callbackurl.go`, `internal/api/callbackurl_test.go`, `cmd/api/main.go`, and `.env.example`, all present on `main` (commits `e4047f5`, `7bfe27d`, `fef7193`, merged via `361c6af`, tracked via `b337782`). The security boundary is confirmed to be exactly RFC1918-only (verified against `net/netip`'s documented semantics), with loopback/link-local (including the `169.254.169.254` cloud metadata address)/unspecified remaining hard-blocked regardless of the flag — this was the specific precision concern raised during plan-checking and it holds.

**One non-blocking follow-up:** `.planning/REQUIREMENTS.md` still lists `WEBHOOK-06` as unchecked/"Pending" even though the phase implementing it is complete and merged to `main`. This is a documentation-tracking inconsistency, not a code defect — recommend a small follow-up commit to check it off.

**One optional recommendation:** a live docker-compose smoke test (create a job with `WEBHOOK_ALLOW_PRIVATE_IPS=true` and a real private-IP listener as `callback_url`, confirm the webhook actually arrives) would add end-to-end operator confidence beyond the unit-level verification performed here, since SC2's literal wording includes "the webhook is actually delivered." This is not required to pass verification given the delivery mechanism itself is unchanged and previously verified in Phase 2, and the only new logic (the SSRF gate) is directly unit-tested.

---

*Verified: 2026-07-08T02:31:33Z*
*Verifier: Claude (gsd-verifier)*
