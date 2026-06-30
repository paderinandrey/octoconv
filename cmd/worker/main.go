// Command worker runs the OctoConv image-class worker: it consumes the image
// queue and executes libvips conversions.
package main

import (
	"context"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
	"github.com/apaderin/octoconv/internal/worker"
)

func main() {
	ctx := context.Background()

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

	h := worker.NewHandler(jobs.NewRepo(pool), store, convert.Default, envDuration("ENGINE_TIMEOUT", 120*time.Second))

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeImageConvert, h.HandleImageConvert)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: envInt("WORKER_CONCURRENCY", 4),
		Queues:      map[string]int{queue.QueueImage: 1},
	})

	log.Printf("🐙 image worker starting (queue=%s)", queue.QueueImage)
	// Run blocks until SIGINT/SIGTERM and shuts down gracefully.
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker: %v", err)
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
