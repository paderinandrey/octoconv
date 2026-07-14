// Command mcp-http runs the OctoConv MCP server over streamable HTTP
// (Phase 25, MCPH-01): the same five tools as cmd/mcp-server, but with
// per-request caller-key pass-through (D-03) and presigned-only remote
// results (D-04). The pod holds NO API key of its own (D-06): every request
// must carry "Authorization: ApiKey <key>", which is parsed once by the
// shared auth.ParseAPIKey, rejected with 401 before any MCP JSON-RPC code
// runs when missing/malformed, and otherwise used to build an isolated
// per-request Client via mcpserver.NewClientForKey.
//
// D-02 verdict (stateless_spike_test.go): Stateless mode DOES deliver
// convert_file's in-flight progress notifications (they are made in the
// context of the incoming tool-call request), so the handler runs with
// StreamableHTTPOptions{Stateless: true}.
//
// Session-key binding (T-25-04b) is applied UNCONDITIONALLY regardless of
// the stateless verdict: the go-sdk's stateful path reuses a session-bound
// server WITHOUT re-invoking getServer when Mcp-Session-Id matches
// (streamable.go:400), and its only built-in anti-hijack check keys off
// auth.TokenInfoFromContext, which this service never sets. A process-local
// map of sessionID -> sha256(creatorKey) rejects (403) any request whose key
// does not match the session's creator BEFORE ServeHTTP. In the current
// stateless mode getServer already runs per request, so the binding is inert
// belt-and-suspenders -- but it becomes the active isolation control if the
// options ever flip to stateful.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/mcpserver"
)

const (
	envAddr    = "MCP_HTTP_ADDR"
	envBaseURL = "OCTOCONV_BASE_URL"

	defaultAddr = ":8070" // D-01

	// Defaults mirror internal/mcpserver's own (unexported) defaults --
	// duplicated per the project's duplication-per-package convention, since
	// mcpserver.Load() is not used here (it requires OCTOCONV_API_KEY, which
	// this binary must never read, D-03/D-06).
	defaultConvertTimeout = 10 * time.Minute
	defaultPollInterval   = time.Second

	// bindingIdleTTL bounds how long a session-key binding survives without
	// being touched; sweepInterval is how often stale entries are collected.
	// In stateless mode the SDK holds no session state at all, so the TTL
	// only bounds this process-local map (belt-and-suspenders hygiene: a
	// client that never DELETEs would otherwise leak one entry per session).
	// If the handler ever flips to stateful, keep bindingIdleTTL comfortably
	// above the SDK SessionTimeout so a binding never expires before its
	// live session does.
	bindingIdleTTL = 30 * time.Minute
	sweepInterval  = 5 * time.Minute
)

// callerKeyContextKey carries the parsed caller API key from the auth-parse
// middleware to the getServer closure -- exactly one parse site (D-03).
type callerKeyContextKey struct{}

// callerKeyFrom extracts the caller key stashed by the auth middleware.
func callerKeyFrom(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(callerKeyContextKey{}).(string)
	return key, ok && key != ""
}

// writeJSONError writes a JSON error body, mirroring internal/auth's
// writeError shape (duplicated per package convention -- cmd/* cannot be
// imported and internal/auth's helper is unexported).
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// withAuthParse is middleware (1): parse "Authorization: ApiKey <key>" via
// the shared auth.ParseAPIKey; 401 with a JSON error BEFORE any MCP JSON-RPC
// code on a missing/malformed header (D-03); on success stash the key in the
// request context. The key itself is never logged and never echoed.
func withAuthParse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := auth.ParseAPIKey(r.Header.Get("Authorization"))
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKeyContextKey{}, key)))
	})
}

// sessionBindings is the process-local session-key binding map (T-25-04b):
// sessionID -> sha256(creatorKey). The raw key is never stored.
type sessionBindings struct {
	mu sync.Mutex
	m  map[string]binding
}

type binding struct {
	keyHash  [sha256.Size]byte
	lastSeen time.Time
}

func newSessionBindings() *sessionBindings {
	return &sessionBindings{m: make(map[string]binding)}
}

// bind records (or refreshes) sessionID -> keyHash.
func (b *sessionBindings) bind(sessionID string, keyHash [sha256.Size]byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[sessionID] = binding{keyHash: keyHash, lastSeen: time.Now()}
}

// check reports whether sessionID is unbound (ok to proceed and bind) or
// bound to keyHash. A bound-to-someone-else session returns false.
// A matching check also refreshes lastSeen.
func (b *sessionBindings) check(sessionID string, keyHash [sha256.Size]byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, bound := b.m[sessionID]
	if !bound {
		return true
	}
	if entry.keyHash != keyHash {
		return false
	}
	entry.lastSeen = time.Now()
	b.m[sessionID] = entry
	return true
}

// drop removes sessionID's binding (session close via DELETE).
func (b *sessionBindings) drop(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, sessionID)
}

// len reports the number of live bindings (tests + observability).
func (b *sessionBindings) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.m)
}

// sweep drops every binding whose lastSeen is older than bindingIdleTTL as
// of now. LIFECYCLE: DELETE prunes graceful closes, but a client that
// vanishes without DELETE would leak its entry forever -- the periodic sweep
// bounds the map in both the stateless (inert map, pure hygiene) and any
// future stateful configuration.
func (b *sessionBindings) sweep(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, entry := range b.m {
		if now.Sub(entry.lastSeen) > bindingIdleTTL {
			delete(b.m, id)
		}
	}
}

// startSweeper runs sweep every sweepInterval until ctx is done.
func (b *sessionBindings) startSweeper(ctx context.Context) {
	ticker := time.NewTicker(sweepInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				b.sweep(now)
			}
		}
	}()
}

// sessionIDCapture wraps a ResponseWriter to record the Mcp-Session-Id the
// SDK sets on the initialize response, at the moment headers are committed.
// Unwrap keeps http.NewResponseController able to reach Flush -- the SDK's
// SSE path depends on it.
type sessionIDCapture struct {
	http.ResponseWriter
	once      sync.Once
	sessionID string
}

func (c *sessionIDCapture) capture() {
	c.once.Do(func() { c.sessionID = c.Header().Get("Mcp-Session-Id") })
}

func (c *sessionIDCapture) WriteHeader(status int) {
	c.capture()
	c.ResponseWriter.WriteHeader(status)
}

func (c *sessionIDCapture) Write(p []byte) (int, error) {
	c.capture()
	return c.ResponseWriter.Write(p)
}

// Unwrap exposes the underlying writer to http.ResponseController (Flush).
func (c *sessionIDCapture) Unwrap() http.ResponseWriter {
	return c.ResponseWriter
}

// middleware is (2) in the chain: session-key binding, BEFORE ServeHTTP
// (T-25-04b). Incoming Mcp-Session-Id already bound to a different key ->
// 403, no MCP code runs. Otherwise the request proceeds; any session id
// observed (incoming first-sight, or set by the SDK on the initialize
// response) is bound to sha256(callerKey); DELETE drops the binding.
func (b *sessionBindings) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := callerKeyFrom(r.Context())
		if !ok {
			// withAuthParse always runs first; this is unreachable in the
			// composed stack, but fail closed rather than open.
			writeJSONError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
			return
		}
		keyHash := sha256.Sum256([]byte(key))

		sessionID := r.Header.Get("Mcp-Session-Id")
		if sessionID != "" {
			if !b.check(sessionID, keyHash) {
				writeJSONError(w, http.StatusForbidden, "session does not belong to this api key")
				return
			}
			if r.Method == http.MethodDelete {
				// Session close: serve the DELETE (the SDK answers 204), then
				// drop the binding.
				next.ServeHTTP(w, r)
				b.drop(sessionID)
				return
			}
			// First sight of an incoming session id: bind it to its presenter
			// so no other key can ride it from now on. (Refresh is handled in
			// check for already-bound ids.)
			b.bind(sessionID, keyHash)
		}

		cw := &sessionIDCapture{ResponseWriter: w}
		next.ServeHTTP(cw, r)

		// Bind any session id the SDK just issued (the initialize response).
		if cw.sessionID != "" {
			b.bind(cw.sessionID, keyHash)
		}
	})
}

// newHandler composes the full mcp-http stack: /healthz (unauthenticated
// probe, D-05) plus the MCP endpoint wrapped in, order-critical and both
// before ServeHTTP: (1) auth-parse -> 401, (2) session-key binding -> 403.
// getServer builds a per-request remote-mode Client bound to the caller's
// own key (D-03/D-04).
func newHandler(base mcpserver.Config, bindings *sessionBindings) http.Handler {
	getServer := func(r *http.Request) *mcp.Server {
		key, ok := callerKeyFrom(r.Context())
		if !ok {
			// Unreachable behind withAuthParse; the SDK turns nil into 400.
			return nil
		}
		client, err := mcpserver.NewClientForKey(base, key)
		if err != nil {
			return nil
		}
		return mcpserver.NewServer(base, client)
	}

	streamable := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{
		// D-02 spike verdict (stateless_spike_test.go): PASS -- stateless
		// mode delivers in-flight progress notifications, so no stateful
		// session store is needed.
		Stateless: true,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/", withAuthParse(bindings.middleware(streamable)))
	return mux
}

// loadBaseConfig builds the shared per-request base Config from the
// environment: OCTOCONV_BASE_URL only (D-06) -- deliberately NOT
// mcpserver.Load(), which requires OCTOCONV_API_KEY; this pod must hold no
// key (D-03). ResultMode is always remote (D-04); OutputDir stays empty
// because remote mode never touches the filesystem.
func loadBaseConfig() (mcpserver.Config, error) {
	baseURL := os.Getenv(envBaseURL)
	if baseURL == "" {
		return mcpserver.Config{}, fmt.Errorf("missing required environment variable %s", envBaseURL)
	}
	return mcpserver.Config{
		BaseURL:        baseURL,
		ResultMode:     mcpserver.ResultRemote,
		ConvertTimeout: envDuration("OCTOCONV_CONVERT_TIMEOUT", defaultConvertTimeout),
		PollInterval:   envDuration("OCTOCONV_POLL_INTERVAL", defaultPollInterval),
	}, nil
}

func main() {
	log.SetOutput(os.Stderr)

	base, err := loadBaseConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	addr := os.Getenv(envAddr)
	if addr == "" {
		addr = defaultAddr
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	bindings := newSessionBindings()
	bindings.startSweeper(ctx)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           newHandler(base, bindings),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("🐙 octoconv-mcp-http listening on %s (base_url=%s, stateless=true)", addr, base.BaseURL)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("🛑 shutting down mcp-http...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("bye 👋")
}

// envDuration reads key as a time.Duration, falling back to def if unset or
// unparsable -- mirrors cmd/api and cmd/worker's identical helper (the
// project's duplication-per-package convention).
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(firstField(v)); err == nil {
			return d
		}
	}
	return def
}

// firstField returns the leading whitespace-delimited token of s, tolerating
// trailing inline comments from .env-sourced values.
func firstField(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}
