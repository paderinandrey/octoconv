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

	"github.com/apaderin/octoconv/internal/api"
	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/clients"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
	"github.com/apaderin/octoconv/internal/queue"
	"github.com/apaderin/octoconv/internal/storage"
)

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

	qc, err := queue.NewClient()
	if err != nil {
		log.Fatalf("queue: %v", err)
	}
	defer qc.Close()

	salt := []byte(os.Getenv("API_KEY_SALT"))
	if len(salt) == 0 {
		log.Fatalf("API_KEY_SALT must be set")
	}
	clientRepo := clients.NewRepo(pool)
	resolver := auth.NewResolver(clientRepo, salt)

	srv := api.NewServer(jobs.NewRepo(pool), store, qc, resolver, api.Config{
		MaxUploadBytes: envInt64("MAX_UPLOAD_BYTES", 100<<20),
	})

	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
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

	<-ctx.Done()
	log.Println("🛑 shutting down API...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
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
