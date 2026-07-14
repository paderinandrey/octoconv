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
	"github.com/apaderin/octoconv/internal/presets"
)

// Repo is the subset of the jobs repository the API depends on.
type Repo interface {
	Create(ctx context.Context, p jobs.CreateParams) (uuid.UUID, error)
	Get(ctx context.Context, id uuid.UUID) (*jobs.Job, error)
	Outputs(ctx context.Context, id uuid.UUID) ([]jobs.Output, error)
}

// PresetRepo is the narrow, interface-segregated subset of the presets
// repository handleCreateJob depends on (D-09): resolution only. The
// pre-Create active re-check (Pitfall 8) re-uses this SAME Resolve method
// rather than adding a second method to the interface.
type PresetRepo interface {
	Resolve(ctx context.Context, clientID uuid.UUID, name string) (*presets.Preset, error)
}

// PresetAdmin is the SECOND, separate interface (D-08) the /v1/presets REST
// handlers depend on: client-scope CRUD plus the merged effective-view reads
// (D-09/D-10). It exists alongside PresetRepo (interface segregation
// preserved, ARCHITECTURE Anti-Pattern 3) — do not widen PresetRepo instead.
// Both are backed by the same *presets.Repo.
type PresetAdmin interface {
	Create(ctx context.Context, p presets.CreateParams) (uuid.UUID, int, error)
	Update(ctx context.Context, scope string, clientID *uuid.UUID, name, targetFormat string, options map[string]any, description string) (int, error)
	Deactivate(ctx context.Context, scope string, clientID *uuid.UUID, name string) error
	Get(ctx context.Context, scope string, clientID *uuid.UUID, name string) (*presets.Preset, error)
	List(ctx context.Context, scope string, clientID *uuid.UUID, includeInactive bool) ([]presets.Preset, error)
	ListForClient(ctx context.Context, clientID *uuid.UUID, includeInactive bool) ([]presets.Preset, error)
	GetForClient(ctx context.Context, clientID *uuid.UUID, name string) (*presets.Preset, error)
}

// Storage is the subset of the storage client the API depends on.
type Storage interface {
	Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Enqueuer dispatches conversion work to the appropriate engine-class queue.
type Enqueuer interface {
	EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error
	EnqueueHTMLConvert(ctx context.Context, jobID uuid.UUID) error
}

// Pinger is a narrow, read-only reachability probe for a single dependency,
// bounded by ctx's deadline (D-16). It must never write/mutate dependency
// state.
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthDeps holds the three dependency pingers /healthz probes (OBS-02,
// D-16/D-17).
type HealthDeps struct {
	Postgres Pinger
	Redis    Pinger
	S3       Pinger
}

// Server holds the API dependencies and configuration.
type Server struct {
	repo          Repo
	storage       Storage
	queue         Enqueuer
	presets       PresetRepo
	presetAdmin   PresetAdmin
	resolver      auth.ClientResolver
	health        HealthDeps
	maxUploadByte int64
	// maxImagePixels is intentionally uint64 — WIDER than maxUploadByte's
	// int64 — because it is compared against a product of two uint32
	// declared dimensions (Pitfall 1); an adversarial max-uint32 x max-uint32
	// product must not wrap or misbehave under signed/narrower arithmetic.
	maxImagePixels uint64
	// maxDocumentUncompressedBytes is the zip-bomb guard ceiling (D-04):
	// the max total declared uncompressed size summed across every ZIP
	// central-directory entry in an office document, compared against
	// ContainerResult.TotalUncompressed (also uint64).
	maxDocumentUncompressedBytes uint64
	presignTTL                   time.Duration
	ipRateRPM                    int
	clientRateRPM                int
	// operators is the OPERATOR_CLIENT_IDS membership set (D-01, Phase 26):
	// callers whose resolved client.ID is a key here may reach the
	// /v1/system/presets subtree via requireOperator. Never nil after
	// NewServer (an unset/empty env var yields an empty, non-nil set --
	// fail-closed, T-26-03).
	operators map[uuid.UUID]struct{}
}

// Config configures a Server.
type Config struct {
	MaxUploadBytes               int64
	MaxImagePixels               uint64
	MaxDocumentUncompressedBytes uint64
	PresignTTL                   time.Duration
	IPRateLimitRPM               int
	ClientRateLimitRPM           int
	// OperatorClientIDs is the parsed OPERATOR_CLIENT_IDS allowlist (D-01).
	// A nil or empty map means zero operators (fail-closed) -- every caller,
	// including an otherwise-valid resolved client, is denied the
	// /v1/system/presets subtree.
	OperatorClientIDs map[uuid.UUID]struct{}
}

// NewServer builds an API server. resolver authenticates every /v1 request
// (see routes.go); it is a narrow interface, keeping interfaces positional
// and Config reserved for tunables only. health carries the three
// dependency pingers /healthz probes (OBS-02). presets is the narrow
// PresetRepo used by handleCreateJob to resolve preset=<name> (D-09).
// presetAdmin is the second, separate interface (D-08) the /v1/presets REST
// handlers depend on — both are typically backed by the same *presets.Repo.
func NewServer(repo Repo, storage Storage, queue Enqueuer, presets PresetRepo, presetAdmin PresetAdmin, resolver auth.ClientResolver, health HealthDeps, cfg Config) *Server {
	if cfg.PresignTTL == 0 {
		cfg.PresignTTL = 15 * time.Minute
	}
	if cfg.MaxUploadBytes == 0 {
		cfg.MaxUploadBytes = 100 << 20 // 100 MiB
	}
	if cfg.MaxImagePixels == 0 {
		cfg.MaxImagePixels = 100_000_000 // D-05: 100 megapixels default
	}
	if cfg.MaxDocumentUncompressedBytes == 0 {
		cfg.MaxDocumentUncompressedBytes = 500 << 20 // D-04: 500 MiB default
	}
	if cfg.IPRateLimitRPM == 0 {
		cfg.IPRateLimitRPM = 60 // coarse pre-auth flood guard, conservative default
	}
	if cfg.ClientRateLimitRPM == 0 {
		cfg.ClientRateLimitRPM = 120 // per-client, conservative for internal batch + interactive usage
	}
	operators := cfg.OperatorClientIDs
	if operators == nil {
		operators = map[uuid.UUID]struct{}{}
	}
	return &Server{
		repo:                         repo,
		storage:                      storage,
		queue:                        queue,
		presets:                      presets,
		presetAdmin:                  presetAdmin,
		resolver:                     resolver,
		health:                       health,
		maxUploadByte:                cfg.MaxUploadBytes,
		maxImagePixels:               cfg.MaxImagePixels,
		maxDocumentUncompressedBytes: cfg.MaxDocumentUncompressedBytes,
		presignTTL:                   cfg.PresignTTL,
		ipRateRPM:                    cfg.IPRateLimitRPM,
		clientRateRPM:                cfg.ClientRateLimitRPM,
		operators:                    operators,
	}
}
