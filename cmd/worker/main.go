// Command worker runs the OctoConv image-class worker: it consumes the image
// queue and executes libvips conversions.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
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

	store, err := storage.New(ctx)
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	redisOpt, err := queue.RedisOpt()
	if err != nil {
		log.Fatalf("redis: %v", err)
	}

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
		convert.Default,
		envDuration("ENGINE_TIMEOUT", 120*time.Second),
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

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)
	mux.HandleFunc(queue.TypeWebhookDeliver, h.HandleWebhookDeliver)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("WORKER_CONCURRENCY", 4),
		Queues:         map[string]int{queue.QueueImage: 2, queue.QueueWebhook: 1},
		RetryDelayFunc: queue.RetryDelayFunc,
	})

	log.Printf("🐙 worker starting (queues=%s,%s)", queue.QueueImage, queue.QueueWebhook)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("worker: %v", err)
	}
	go sweeper.Run(ctx)

	<-ctx.Done()
	log.Println("🛑 shutting down worker...")
	srv.Shutdown()
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
