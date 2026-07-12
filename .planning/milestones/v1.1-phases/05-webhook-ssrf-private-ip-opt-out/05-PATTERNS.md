# Phase 5: Webhook SSRF Private-IP Opt-Out - Pattern Map

**Mapped:** 2026-07-08
**Files analyzed:** 4 (all modifications, no new files)
**Analogs found:** 4 / 4 (all self-referential — this phase extends existing files using their own established patterns)

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|--------------------|------|-----------|-----------------|---------------|
| `internal/api/callbackurl.go` | utility (validation) | request-response (inline validation call) | itself — `WEBHOOK_ALLOW_INSECURE_HTTP` block, same file, lines 29-39 | exact |
| `internal/api/callbackurl_test.go` | test | request-response | itself — table-driven `TestIsBlockedIP` / `TestValidateCallbackURL`, same file | exact |
| `cmd/api/main.go` | config/entry-point | event-driven (startup sequence) | itself — existing `log.Fatalf("API_KEY_SALT must be set")` env-gate + `log.Printf("🚀 API listening...")` startup lines | exact |
| `.env.example` | config | — | itself — `WEBHOOK_SIGNING_SECRET` / `WEBHOOK_PRESIGN_TTL` entries under the `# Worker` section | exact |

No cross-package analog search was needed — this phase is a narrow, self-contained extension of one existing subsystem (SSRF guard in `internal/api/callbackurl.go`) plus its two integration points (`cmd/api/main.go` startup, `.env.example` docs). All patterns to copy already live in the exact files being modified.

## Pattern Assignments

### `internal/api/callbackurl.go` (utility, validation)

**Analog:** same file — `WEBHOOK_ALLOW_INSECURE_HTTP` handling (lines 29-39) is the direct model for `WEBHOOK_ALLOW_PRIVATE_IPS`.

**Existing inline-env-read pattern to copy** (lines 29-39):
```go
allowInsecureHTTP := os.Getenv("WEBHOOK_ALLOW_INSECURE_HTTP") == "true"
switch u.Scheme {
case "https":
	// ok
case "http":
	if !allowInsecureHTTP {
		return errors.New("callback_url: http scheme not allowed")
	}
default:
	return errors.New("callback_url: unsupported scheme")
}
```

**Function to modify — `isBlockedIP`** (lines 69-79):
```go
// isBlockedIP reports whether addr falls in a range this service refuses to
// deliver webhooks to: loopback, RFC1918 private space, link-local (which
// covers the 169.254.0.0/16 cloud metadata endpoint, e.g. 169.254.169.254),
// or unspecified.
func isBlockedIP(addr netip.Addr) bool {
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified()
}
```

**Pattern to apply (D-01):** Make only the `addr.IsPrivate()` term conditional on `os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true"`, matching the exact string-comparison idiom above. All other terms (`IsLoopback`, `IsLinkLocalUnicast`, `IsLinkLocalMulticast`, `IsUnspecified`) stay unconditional per D-01. Since `isBlockedIP(addr netip.Addr) bool` currently takes no env-dependent parameter, the read can happen inline at the top of the function body (same style as `validateCallbackURL`'s `allowInsecureHTTP` local var) — this keeps the "read `os.Getenv` at point of use, no config struct" convention (CONTEXT.md Established Patterns) and requires no signature change / no threading through `validateCallbackURL`.

**Constraint carried over:** `internal/api/callbackurl.go` must NOT gain a `log.Printf` — logging only happens in `cmd/*/main.go` (project convention, reaffirmed in CONTEXT.md D-04 note). The doc comment on `isBlockedIP` should be updated to reflect that the private-IP block is now conditional (mirrors how the file already documents intent inline, e.g. `validateCallbackURL`'s D-03 residual-risk comment at lines 11-15).

---

### `internal/api/callbackurl_test.go` (test)

**Analog:** same file — existing table-driven tests `TestIsBlockedIP` (lines 8-33) and `TestValidateCallbackURL` (lines 35-56).

**Table-driven pattern to copy** (lines 8-33):
```go
func TestIsBlockedIP(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{"loopback", "127.0.0.1", true},
		{"rfc1918-10", "10.0.0.1", true},
		{"rfc1918-192", "192.168.0.1", true},
		{"rfc1918-172", "172.16.0.1", true},
		{"link-local-metadata", "169.254.169.254", true},
		{"unspecified", "0.0.0.0", true},
		{"public", "8.8.8.8", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, err := netip.ParseAddr(c.ip)
			if err != nil {
				t.Fatalf("ParseAddr(%q): %v", c.ip, err)
			}
			if got := isBlockedIP(addr); got != c.want {
				t.Errorf("isBlockedIP(%q) = %v, want %v", c.ip, got, c.want)
			}
		})
	}
}
```

**Env-var-gated test pattern (missing today — the IN-01 gap called out in CONTEXT.md):** No existing test in this file sets an env var via `t.Setenv`; `WEBHOOK_ALLOW_INSECURE_HTTP` currently has zero coverage. There is no in-repo analog for `t.Setenv`-based env-flag testing to copy verbatim, so use the standard Go idiom directly:
```go
func TestIsBlockedIPAllowPrivate(t *testing.T) {
	t.Setenv("WEBHOOK_ALLOW_PRIVATE_IPS", "true")
	addr, _ := netip.ParseAddr("10.0.0.1")
	if isBlockedIP(addr) {
		t.Errorf("isBlockedIP(10.0.0.1) = true, want false when WEBHOOK_ALLOW_PRIVATE_IPS=true")
	}
	// loopback/link-local/unspecified must remain blocked regardless (D-01)
	for _, ip := range []string{"127.0.0.1", "169.254.169.254", "0.0.0.0"} {
		addr, _ := netip.ParseAddr(ip)
		if !isBlockedIP(addr) {
			t.Errorf("isBlockedIP(%q) = false, want true even with WEBHOOK_ALLOW_PRIVATE_IPS=true", ip)
		}
	}
}
```
**Coverage requirement (CONTEXT.md canonical_refs):** Tests must cover both flag-on and flag-off behavior explicitly for `WEBHOOK_ALLOW_PRIVATE_IPS` — do not repeat the `WEBHOOK_ALLOW_INSECURE_HTTP` zero-coverage gap. `t.Setenv` auto-restores the env var after each subtest, so no manual cleanup/defer is needed (stdlib `testing` idiom, consistent with "no third-party assertion/mocking library" convention).

---

### `cmd/api/main.go` (entry point, startup sequence)

**Analog:** same file — existing env-gated startup checks and emoji-prefixed `log.Printf` lines.

**Env-gate-at-startup pattern to copy** (lines 79-82):
```go
salt := []byte(os.Getenv("API_KEY_SALT"))
if len(salt) == 0 {
	log.Fatalf("API_KEY_SALT must be set")
}
```

**Emoji-prefixed startup log pattern to copy** (line 108, 128):
```go
log.Printf("🚀 API listening on %s", addr)
...
log.Printf("📊 metrics listening on %s", metricsAddr)
```

**Pattern to apply (D-04):** Add a one-time startup check, placed alongside the other early env-driven setup (near the `salt`/`clientRepo`/`resolver` block, before `srv := api.NewServer(...)` — this is where other security-relevant config gates already live) that reads `os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true"` and logs a warning once via `log.Printf`. Per CONTEXT.md discretion, prefer a distinct `⚠` prefix (not `🚀`/`🛑`/`📊`, which are reserved for lifecycle/listener messages) to visually flag it as a safety-posture warning, e.g.:
```go
if os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true" {
	log.Printf("⚠️  WEBHOOK_ALLOW_PRIVATE_IPS=true: webhook SSRF guard allows RFC1918 private-IP callback_url targets")
}
```
This is a deliberate, scoped exception to "only `cmd/*/main.go` logs, `internal/*` never logs" — already satisfied since the log line lives in `cmd/api/main.go`, not `internal/api/callbackurl.go` (no violation, just confirms placement).

---

### `.env.example` (config)

**Analog:** same file — existing `# Worker` section entries with inline `#` comments explaining default/rationale (lines 21-26).

**Entry-with-rationale-comment pattern to copy** (lines 24-26):
```
IMAGE_MAX_RETRY=4   # small bounded retry budget for image conversion (D-05, smaller than webhook's 6); also feeds the derived per-job uniqueness-lock TTL together with ENGINE_TIMEOUT
WEBHOOK_SIGNING_SECRET=change-me-to-a-long-random-secret   # HMAC-SHA256 secret for signed webhook callbacks (required)
WEBHOOK_PRESIGN_TTL=6h   # presigned download_url lifetime per webhook delivery attempt (optional, default 6h)
```

**Gap found — flag this to the planner:** `WEBHOOK_ALLOW_INSECURE_HTTP` (the flag CONTEXT.md says `WEBHOOK_ALLOW_PRIVATE_IPS` should sit "immediately alongside") is **not currently documented in `.env.example`** at all, despite being read in `internal/api/callbackurl.go:29`. Grep confirms it only appears in code and historical phase-2 planning docs, never in `.env.example`. The planner/executor should decide whether to:
  (a) add both `WEBHOOK_ALLOW_INSECURE_HTTP` and `WEBHOOK_ALLOW_PRIVATE_IPS` together (closing the pre-existing doc gap as a drive-by fix), or
  (b) add only `WEBHOOK_ALLOW_PRIVATE_IPS` and leave the pre-existing gap alone (strictly in-scope).
Given the project's "every key config documented in `.env.example`" convention (all other env vars used in code appear there), option (a) is more consistent — but this is a scope-boundary call, not a pattern-mapping one.

**Suggested new entry (matching existing style, placed near `WEBHOOK_SIGNING_SECRET`/`WEBHOOK_PRESIGN_TTL`):**
```
WEBHOOK_ALLOW_PRIVATE_IPS=false   # opt-in: allow webhook callback_url to target RFC1918 private-IP addresses; loopback/link-local/unspecified remain hard-blocked regardless (default false, D-01/D-02)
```

---

## Shared Patterns

### Environment-variable-only configuration
**Source:** `internal/api/callbackurl.go:29` (`os.Getenv("WEBHOOK_ALLOW_INSECURE_HTTP") == "true"`)
**Apply to:** `internal/api/callbackurl.go` (new `WEBHOOK_ALLOW_PRIVATE_IPS` read)
```go
os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true"
```
Read at point of use, no config struct threading — matches project-wide "environment-variable configuration only" architectural constraint.

### Only `cmd/*/main.go` logs
**Source:** `cmd/api/main.go` (all `log.Printf`/`log.Fatalf` calls); `internal/*` packages never call `log`
**Apply to:** `cmd/api/main.go` (new D-04 startup warning); confirms `internal/api/callbackurl.go` must stay silent

### Table-driven tests, stdlib-only
**Source:** `internal/api/callbackurl_test.go` (`TestIsBlockedIP`, `TestValidateCallbackURL`)
**Apply to:** `internal/api/callbackurl_test.go` (new flag-on/flag-off cases), using `t.Setenv` for the env-var toggle since no existing analog for env-gated tests exists in-repo (first instance of this sub-pattern in the codebase)

## No Analog Found

None — all 4 files are modifications to existing files, and each has a directly reusable pattern within itself. The one genuine gap is not a missing analog but a missing prior artifact: no existing test in the repo uses `t.Setenv` to test an env-gated boolean flag (see `callbackurl_test.go` section above); the stdlib `t.Setenv` idiom is straightforward enough that no analog is needed.

## Metadata

**Analog search scope:** `internal/api/` (callbackurl.go, callbackurl_test.go), `cmd/api/main.go`, `.env.example`, plus a repo-wide grep for `WEBHOOK_ALLOW_INSECURE_HTTP` to check `.env.example` documentation completeness
**Files scanned:** 4 target files + grep across `.go`/`.example`/`.md`
**Pattern extraction date:** 2026-07-08
