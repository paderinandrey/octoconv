// Command av-worker runs the OctoConv av-class worker: it consumes the av
// queue and executes ffmpeg-based video conversion (transcode / audio-extract
// / thumbnail).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/apaderin/octoconv/internal/convert"
	"github.com/apaderin/octoconv/internal/db"
	"github.com/apaderin/octoconv/internal/jobs"
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

	qc, err := queue.NewClient()
	if err != nil {
		log.Fatalf("queue client: %v", err)
	}
	defer qc.Close()

	repo := jobs.NewRepo(pool)

	// D-09/Pitfall 4 (Phase 36): re-register a CONFIGURED AVConverter,
	// overriding the zero-value registration converters.go's init() already
	// performed -- documented last-write-wins semantics (Registry.Register
	// is a bare map assignment, converters.go). This MUST run before
	// srv.Start(mux) below: that is the point asynq's worker goroutines
	// begin concurrently reading the registered converter, so this single
	// write must happen-before every concurrent read (no mutex needed, same
	// happens-before boundary as AUDIO_MODEL_PATH/AUDIO_THREADS in
	// cmd/audio-worker/main.go).
	maxDuration := envDurationSeconds("AV_MAX_DURATION_SECONDS", 4*time.Hour)
	convert.Default.Register(convert.AVConverter{
		MaxSourceDuration:         maxDuration,
		MaxSourceResolutionHeight: 4320,
	})

	// D-06 (Phase 36): AV_DISK_SAFETY_FACTOR threads into the disk-space
	// guard the SAME env-only-in-main + setter convention as
	// AUDIO_MODEL_PATH/AUDIO_THREADS -- internal/convert never reads any env
	// var directly. Must also complete before srv.Start(mux) below.
	diskSafetyFactor := envFloat("AV_DISK_SAFETY_FACTOR", 3.0)
	convert.SetAVDiskSafetyFactor(diskSafetyFactor)

	threads, threadSource := resolveAVThreads()
	log.Printf("🧵 AV_MAX_DURATION_SECONDS=%v AV_DISK_SAFETY_FACTOR=%.2f threads=%d (source=%s)",
		maxDuration, diskSafetyFactor, threads, threadSource)

	h := worker.NewHandler(
		repo,
		store,
		convert.Default,
		envDuration("AV_ENGINE_TIMEOUT", 600*time.Second), // [ASSUMED] provisional, mirrors AUDIO_ENGINE_TIMEOUT's original 600s placeholder precedent (600s -> 742s after Phase 32's RTF measurement); Phase 36 re-derives the real value from an RTF matrix. Coupled to RECONCILER_ACTIVE_STALE_AFTER (global 900s default, docker-compose.yml) -- raising this timeout toward 900s requires raising that threshold too, in the same change (this near-broke the audio engine once).
		nil, // webhookRepo — webhook-only; HandleAVConvert never reads it
		nil, // deliverer — webhook-only; HandleAVConvert never reads it
		qc,
		nil, // signingSecret — webhook-only; HandleAVConvert never reads it
		0,   // presignTTL — webhook-only; HandleAVConvert never reads it
		0,   // audioMaxDuration — 0 for every non-audio worker cmd (worker.go:350-355); AV's duration/resolution guard is self-contained inside AVConverter.Convert, not spliced through this parameter
	)

	// D-04/D-05: the stale-job sweep loop runs solely in cmd/webhook-worker
	// (under a Postgres advisory lock) — no sweeper of any kind is
	// constructed or run here, avoiding a double-sweep race between
	// independent sweep loops recovering the same stranded job.

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeAVConvert, h.HandleAVConvert)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("AV_WORKER_CONCURRENCY", 2), // transcode is CPU-bound; the container-level CPU/RAM ceiling is Phase 36's to size
		Queues:         map[string]int{queue.QueueAV: 1},
		RetryDelayFunc: queue.RetryDelayFunc,
		// asynq defaults ShutdownTimeout to 8s, silently capping the
		// graceful window regardless of the pod's
		// terminationGracePeriodSeconds. Aligning it to the engine
		// timeout plus margin lets a genuinely long in-flight ffmpeg
		// transcode survive SIGTERM instead of being aborted+requeued
		// (Pitfall 6, mirrors cmd/audio-worker/main.go:113 exactly).
		ShutdownTimeout: envDuration("AV_ENGINE_TIMEOUT", 600*time.Second) + 10*time.Second,
	})

	// KEDA-01/D-01: the queue-depth collector now lives solely on the
	// always-on api process (cmd/api/main.go) — a worker Deployment scaled to
	// genuine 0 replicas by KEDA would otherwise have no pod exposing the
	// metric KEDA needs to scale it back up. /metrics here still serves the
	// promauto-registered job/duration metrics; the endpoint itself is
	// unchanged. QueueAV's collector wiring is Plan 04's job, not this file.

	log.Printf("🐙 av-worker starting (queue=%s)", queue.QueueAV)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("av-worker: %v", err)
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
	log.Println("🛑 shutting down av-worker...")
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

// envDurationSeconds reads a duration env var whose _SECONDS-suffixed name
// invites a bare integer-seconds value (WR-05, cloned verbatim from
// cmd/audio-worker/main.go): time.ParseDuration alone REJECTS "14400"
// ("missing unit in duration"), and the codebase-wide silent-fallback
// pattern would then quietly replace an operator's explicit ceiling with
// the default -- unacceptable for a fail-closed resource guard. Accepts, in
// order: Go duration syntax ("4h", "14400s") and bare non-negative integer
// seconds ("14400"), both tolerant of a trailing inline comment via
// firstField (same convention as envDuration/envInt). A set-but-unparseable
// value is logged before falling back to def -- this env guards a security
// ceiling, so the fallback must never be silent.
func envDurationSeconds(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f := firstField(v)
	// WR-02 (review, audio precedent): both parse branches reject negatives
	// -- a negative ceiling makes EnforceMaxDuration (d > max) terminally
	// reject EVERY job, so "-5s"/"-30m" must fall through to the
	// logged-warning default below, never be returned silently. Zero stays
	// valid: it is an explicit reject-everything ceiling, consistent with
	// the bare-integer branch's sec >= 0.
	if d, err := time.ParseDuration(f); err == nil && d >= 0 {
		return d
	}
	if sec, err := strconv.Atoi(f); err == nil && sec >= 0 {
		return time.Duration(sec) * time.Second
	}
	log.Printf("⚠️ %s=%q is neither a duration (\"4h\") nor bare integer seconds (\"14400\"); using default %v", key, f, def)
	return def
}

// envFloat reads a float env var (AV_DISK_SAFETY_FACTOR), tolerant of a
// trailing inline comment via firstField (same convention as every other
// numeric env helper in this file). A set-but-unparseable or non-positive
// value falls back to def with a logged warning -- this env sizes a
// fail-closed resource guard, so the fallback must never be silent.
func envFloat(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f := firstField(v)
	if n, err := strconv.ParseFloat(f, 64); err == nil && n > 0 {
		return n
	}
	log.Printf("⚠️ %s=%q is not a positive number; using default %v", key, f, def)
	return def
}

// resolveAVThreads mirrors av.go's unexported avThreadCount() resolver
// (CgroupCPULimit -> runtime.NumCPU fallback) so this startup log line can
// report the SAME thread count Convert will actually use, without exporting
// avThreadCount itself. An AV_THREADS operator override is deliberately out
// of scope for this task (plan: "optional and out of scope unless trivial").
func resolveAVThreads() (int, string) {
	if n, ok := convert.CgroupCPULimit(); ok {
		return n, "cgroup"
	}
	return runtime.NumCPU(), "NumCPU fallback"
}

func firstField(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}
