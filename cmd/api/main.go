// Command api runs the OctoConv HTTP API: accepts conversion jobs and reports
// their status.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	"github.com/apaderin/octoconv/internal/api"
	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/metrics"
	"github.com/apaderin/octoconv/internal/presets"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
)

// redisPinger adapts a *redis.Client to api.Pinger. asynq's own Client
// exposes no public Ping method (RESEARCH.md Open Question 2), so the API
// process opens a small, dedicated Redis connection purely for /healthz.
type redisPinger struct {
	client *redis.Client
}

func (p redisPinger) Ping(ctx context.Context) error {
	return p.client.Ping(ctx).Err()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	store, err := storage.New(ctx)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}
	// The API is the single owner of the bucket lifecycle rule (Pitfall 4);
	// SetBucketLifecycle is an idempotent full-document PUT, so calling it
	// on every startup is safe (D-12).
	if err := store.EnsureLifecycle(ctx, envDuration("STORAGE_TTL", 168*time.Hour)); err != nil {
		log.Fatalf("storage lifecycle: %v", err)
	}

	qc, err := queue.NewClient()
	if err != nil {
		log.Fatalf("queue: %v", err)
	}
	defer qc.Close()

	// Dedicated Redis connection for /healthz only (D-16) — asynq's Client
	// exposes no public Ping, so the API opens its own lightweight ping
	// client against the same REDIS_ADDR the queue already uses.
	redisOpt, err := queue.RedisOpt()
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisOpt.Addr})
	defer rdb.Close()

	// KEDA-01/D-01/D-02/D-06 (Phase 35): the queue-depth collector now lives
	// solely on the always-on api process, registered for EVERY engine-class
	// queue — a worker Deployment scaled to genuine 0 replicas by KEDA would
	// otherwise have no pod exposing the metric KEDA needs to scale it back
	// up. The queue list is DERIVED from queue.AllConvertQueues() rather than
	// hand-listed: this call is variadic, so a hand-maintained list can
	// silently drop an engine class with no compile error (RESEARCH.md
	// Pitfall 2) -- queue.TestAllConvertQueuesCoversEveryEngine guards the
	// derivation itself. webhook is not an engine class and is therefore not
	// covered by AllConvertQueues(), so it is appended explicitly here; it is
	// included even though webhook-worker is never KEDA-scaled (D-01 in
	// PROJECT.md), so its depth stays observable. Matching every worker's
	// existing precedent, the Inspector is never closed — Collect() is
	// pull-based/lazy, invoked once per scrape.
	prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt),
		append(queue.AllConvertQueues(), queue.QueueWebhook)...))

	salt := []byte(os.Getenv("API_KEY_SALT"))
	if len(salt) == 0 {
		log.Fatalf("API_KEY_SALT must be set")
	}
	clientRepo := clients.NewRepo(pool)
	resolver := auth.NewResolver(clientRepo, salt)
	presetRepo := presets.NewRepo(pool)

	// D-01/OPER-01: OPERATOR_CLIENT_IDS is parsed once, here, at startup.
	// A malformed entry aborts startup loudly (fail-loud, T-26-03) -- it
	// must never silently shrink the intended operator set. Empty/unset is
	// not an error: it yields zero operators (fail-closed).
	operatorIDs, err := api.ParseOperatorClientIDs(os.Getenv("OPERATOR_CLIENT_IDS"))
	if err != nil {
		log.Fatalf("OPERATOR_CLIENT_IDS: %v", err)
	}

	// D-04: surface a startup-visible warning when the webhook SSRF guard's
	// RFC1918 private-IP block is relaxed, so a relaxed safety posture is
	// obvious from logs without reading .env or source (loopback/link-local/
	// unspecified remain hard-blocked regardless — see internal/api/callbackurl.go).
	if os.Getenv("WEBHOOK_ALLOW_PRIVATE_IPS") == "true" {
		log.Printf("⚠️  WEBHOOK_ALLOW_PRIVATE_IPS=true: webhook SSRF guard allows RFC1918 private-IP callback_url targets")
	}

	health := api.HealthDeps{
		Postgres: pool,
		Redis:    redisPinger{client: rdb},
		S3:       store,
	}
	srv := api.NewServer(jobs.NewRepo(pool), store, qc, presetRepo, presetRepo, resolver, health, api.Config{
		// D-07 (Phase 35, operator-confirmed 2026-07-21): raised from 100
		// MiB to 2 GiB so video uploads are admissible -- this is a pre-
		// parse http.MaxBytesReader bound, enforced BEFORE the engine class
		// is known. MaxEngineBytes is left unset (nil) here deliberately:
		// NewServer's own D-07 defaulting is the single source of truth for
		// the per-engine ceilings that restore image/document/html/audio to
		// their prior 100 MiB effective bound, so that map is not
		// duplicated at this call site.
		MaxUploadBytes:               envInt64("MAX_UPLOAD_BYTES", 2<<30),
		MaxImagePixels:               uint64(envInt64("MAX_IMAGE_PIXELS", 100_000_000)),            // D-05: 100 megapixels default
		MaxDocumentUncompressedBytes: uint64(envInt64("MAX_DOCUMENT_UNCOMPRESSED_BYTES", 500<<20)), // D-04: 500 MiB default
		IPRateLimitRPM:               int(envInt64("RATE_LIMIT_IP_RPM", 60)),
		ClientRateLimitRPM:           int(envInt64("RATE_LIMIT_CLIENT_RPM", 120)),
		OperatorClientIDs:            operatorIDs,
	})

	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8090"
	}
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("🚀 API listening on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Prometheus /metrics is served on its own localhost-only listener,
	// separate from the public API_ADDR (D-19/T-04-13) — operational job/
	// queue data must not be reachable by arbitrary internal API callers.
	metricsAddr := os.Getenv("METRICS_ADDR")
	if metricsAddr == "" {
		metricsAddr = "127.0.0.1:9090"
	}
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           promhttp.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("📊 metrics listening on %s", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("metrics listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("🛑 shutting down API...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics graceful shutdown failed: %v", err)
	}
	log.Println("bye 👋")
}

func envInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		// Tolerate trailing inline comments / whitespace from .env files.
		if n, err := strconv.ParseInt(firstField(v), 10, 64); err == nil {
			return n
		}
	}
	return def
}

func firstField(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(firstField(v)); err == nil {
			return d
		}
	}
	return def
}
