// Command webhook-worker consumes the webhook-delivery queue and runs the
// fleet-wide reconciler sweep under a Postgres advisory lock; it performs no
// file conversion.
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

	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/metrics"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/reconciler"
	"github.com/apaderin/octoconv/internal/storage"
	"github.com/apaderin/octoconv/internal/webhook"
	"github.com/apaderin/octoconv/internal/worker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer pool.Close()

	// Required by HandleWebhookDeliver's PresignGet call for done-status jobs
	// (D-07 corrected): the webhook payload embeds a freshly presigned S3
	// download_url, so this binary needs a full storage client even though it
	// runs no conversion engine.
	store, err := storage.New(ctx)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	redisOpt, err := queue.RedisOpt()
	if err != nil {
		log.Fatalf("redis: %v", err)
	}

	// webhook-worker is now the ONLY signer of webhook deliveries (D-03 clean
	// cut), so a missing secret must fail closed at startup rather than
	// silently deliver unsigned/unverifiable callbacks.
	signingSecret := []byte(os.Getenv("WEBHOOK_SIGNING_SECRET"))
	if len(signingSecret) == 0 {
		log.Fatalf("WEBHOOK_SIGNING_SECRET must be set")
	}

	qc, err := queue.NewClient()
	if err != nil {
		log.Fatalf("queue client: %v", err)
	}
	defer qc.Close()

	repo := jobs.NewRepo(pool)

	h := worker.NewHandler(
		repo,
		store,
		nil, // convert.Registry — engine-only; HandleWebhookDeliver never reads h.registry
		0,   // engineTimeout — engine-only; HandleWebhookDeliver never reads h.engineTimout
		webhook.NewRepo(pool),
		webhook.NewDeliverer(),
		qc,
		signingSecret,
		envDuration("WEBHOOK_PRESIGN_TTL", 6*time.Hour),
	)

	sweeper := reconciler.NewSweeper(repo, qc, reconciler.Config{
		QueuedStaleAfter: envDuration("RECONCILER_QUEUED_STALE_AFTER", 90*time.Second),
		ActiveStaleAfter: envDuration("RECONCILER_ACTIVE_STALE_AFTER", 5*time.Minute),
		SweepInterval:    envDuration("RECONCILER_SWEEP_INTERVAL", 1*time.Minute),
		MaxRecoveries:    envInt("RECONCILER_MAX_RECOVERIES", 3),
	})

	// D-01/D-02: the sweeper now runs fleet-wide, so it must be gated by a
	// Postgres session-level advisory lock — exactly one webhook-worker
	// replica sweeps at a time; the rest only consume the webhook queue.
	lock, err := reconciler.NewPGAdvisoryLock(ctx, pool)
	if err != nil {
		log.Fatalf("advisory lock: %v", err)
	}
	// Registered AFTER defer pool.Close() (line ~39) so that under LIFO it
	// runs FIRST — releasing the dedicated advisory-lock connection before
	// pool.Close() waits on it. Otherwise pool.Close() blocks forever on the
	// never-released connection (WR-01).
	defer lock.Close()

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("WEBHOOK_WORKER_CONCURRENCY", 4),
		Queues:         map[string]int{queue.QueueWebhook: 1},
		RetryDelayFunc: queue.RetryDelayFunc,
	})

	// Register the queue-depth collector so /metrics reports per-queue task
	// counts by state (OBS-01); read-only, pull-based on scrape.
	prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueWebhook))

	log.Printf("🐙 webhook-worker starting (queue=%s)", queue.QueueWebhook)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("webhook-worker: %v", err)
	}
	go sweeper.RunWithLock(ctx, lock)

	// Prometheus /metrics is served on its own localhost-only listener,
	// separate from the public API_ADDR (D-19/T-04-13) — same trust-model
	// reasoning as the API process.
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
	log.Println("🛑 shutting down webhook-worker...")
	srv.Shutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics graceful shutdown failed: %v", err)
	}
	log.Println("bye 👋")
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(firstField(v)); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(firstField(v)); err == nil {
			return d
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
