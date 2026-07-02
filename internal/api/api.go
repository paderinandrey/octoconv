// Package api implements the HTTP layer: accepting conversion jobs and
// reporting their status.
package api

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/apaderin/octoconv/internal/auth"
	"github.com/apaderin/octoconv/internal/jobs"
)

// Repo is the subset of the jobs repository the API depends on.
type Repo interface {
	Create(ctx context.Context, p jobs.CreateParams) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*jobs.Job, error)
	Outputs(ctx context.Context, id uuid.UUID) ([]jobs.Output, error)
}

// Storage is the subset of the storage client the API depends on.
type Storage interface {
	Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Enqueuer dispatches image conversion work.
type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
}

// Server holds the API dependencies and configuration.
type Server struct {
	repo          Repo
	storage       Storage
	queue         Enqueuer
	resolver      auth.ClientResolver
	maxUploadByte int64
	presignTTL    time.Duration
}

// Config configures a Server.
type Config struct {
	MaxUploadBytes int64
	PresignTTL     time.Duration
}

// NewServer builds an API server. resolver authenticates every /v1 request
// (see routes.go); it is a narrow interface, keeping interfaces positional
// and Config reserved for tunables only.
func NewServer(repo Repo, storage Storage, queue Enqueuer, resolver auth.ClientResolver, cfg Config) *Server {
	if cfg.PresignTTL == 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 100 << 20 // 100 MiB
	}
	return &Server{
		repo:          repo,
		storage:       storage,
		queue:         queue,
		resolver:      resolver,
		maxUploadByte: cfg.MaxUploadBytes,
		presignTTL:    cfg.PresignTTL,
	}
}
