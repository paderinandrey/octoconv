// Command worker runs the OctoConv image-class worker: it consumes the image
// queue and executes libvips conversions.
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

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/metrics"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
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

	store, err := storage.New(ctx)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	redisOpt, err := queue.RedisOpt()
	if err != nil {
		log.Fatalf("redis: %v", err)
	}

	// Webhook delivery now lives solely in cmd/webhook-worker (D-03/D-07);
	// image-worker neither delivers nor signs webhooks, so no signing secret
	// is read here and webhookRepo/deliverer are passed as nil — inert for
	// HandleImageConvert, which never touches h.webhookRepo/h.deliverer/
	// h.signingSecret.
	qc, err := queue.NewClient()
	if err != nil {
		log.Fatalf("queue client: %v", err)
	}
	defer qc.Close()

	repo := jobs.NewRepo(pool)

	h := worker.NewHandler(
		repo,
		store,
		convert.Default,
		envDuration("ENGINE_TIMEOUT", 120*time.Second),
		nil, // webhookRepo — webhook-only; HandleImageConvert never reads it
		nil, // deliverer — webhook-only; HandleImageConvert never reads it
		qc,
		nil, // signingSecret — webhook-only; HandleImageConvert never reads it
		0,   // presignTTL — webhook-only; HandleImageConvert never reads it
	)

	// D-03/D-04: the stale-job sweep loop runs solely in cmd/webhook-worker
	// (under the Postgres advisory lock) — no sweeper of any kind is
	// constructed or run here.

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("WORKER_CONCURRENCY", 4),
		Queues:         map[string]int{queue.QueueImage: 4},
		RetryDelayFunc: queue.RetryDelayFunc,
	})

	// Register the queue-depth collector so /metrics reports per-queue task
	// counts by state (OBS-01); read-only, pull-based on scrape.
	prometheus.MustRegister(metrics.NewQueueDepthCollector(asynq.NewInspector(redisOpt), queue.QueueImage))

	log.Printf("🐙 worker starting (queue=%s)", queue.QueueImage)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("worker: %v", err)
	}

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
	log.Println("🛑 shutting down worker...")
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
