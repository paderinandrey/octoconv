// Command audio-worker runs the OctoConv audio-class worker: it consumes
// the audio queue and executes ffmpeg->whisper-cli transcription.
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
	"strings"
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

	h := worker.NewHandler(
		repo,
		store,
		convert.Default,
		envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second), // [ASSUMED] placeholder, Phase 32 re-derives from RTF measurement -- do NOT copy DOCUMENT_ENGINE_TIMEOUT's 300s
		nil, // webhookRepo — webhook-only; HandleAudioConvert never reads it
		nil, // deliverer — webhook-only; HandleAudioConvert never reads it
		qc,
		nil, // signingSecret — webhook-only; HandleAudioConvert never reads it
		0,   // presignTTL — webhook-only; HandleAudioConvert never reads it
		envDurationSeconds("AUDIO_MAX_DURATION_SECONDS", 4*time.Hour),
	)

	// D-04/D-05: the stale-job sweep loop runs solely in cmd/webhook-worker
	// (under a Postgres advisory lock) — no sweeper of any kind is
	// constructed or run here, avoiding a double-sweep race between
	// independent sweep loops recovering the same stranded job.

	// AUD-05/D-01: AUDIO_MODEL_PATH is read ONLY here (env-only-in-main,
	// mirroring VERAPDF_TIMEOUT's threading into internal/convert) and
	// injected via a setter -- internal/convert never calls os.Getenv
	// directly. This MUST run before srv.Start(mux) below: that is the
	// point asynq's worker goroutines begin concurrently reading
	// audioModelPath, so this single write must happen-before every
	// concurrent read (no mutex needed). stripInlineComment (WR-06): non-
	// shell env-file loaders (docker --env-file, compose env_file:, k8s
	// configmaps) do NOT strip trailing "# comment" text, which would hand
	// whisper-cli a garbage -m path — same defense firstField provides for
	// the numeric/duration envs, but conservative enough to keep paths with
	// spaces working.
	convert.SetAudioModelPath(stripInlineComment(os.Getenv("AUDIO_MODEL_PATH")))

	// T-32-04/AUD-06/AUD-07 SC4: size whisper-cli's --threads to the
	// container's real cgroup CPU quota, not host core count (PITFALLS.md
	// Pitfall 5) -- same env-only-in-main + setter convention as
	// AUDIO_MODEL_PATH above, and the same happens-before boundary (must
	// run before srv.Start(mux) below, the point asynq's worker goroutines
	// begin concurrently reading audioThreads). Logged at startup: this is
	// operator-visible evidence the RTF measurement and Phase 33 depend on.
	threads, threadSource := resolveAudioThreads()
	convert.SetAudioThreads(threads)
	log.Printf("🧵 audio threads=%d (source=%s)", threads, threadSource)

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeAudioConvert, h.HandleAudioConvert)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency:    envInt("AUDIO_WORKER_CONCURRENCY", 2),
		Queues:         map[string]int{queue.QueueAudio: 1},
		RetryDelayFunc: queue.RetryDelayFunc,
		// Pattern 2: asynq defaults ShutdownTimeout to 8s, silently capping
		// the graceful window regardless of the pod's
		// terminationGracePeriodSeconds. Aligning it to
		// AUDIO_ENGINE_TIMEOUT+margin lets a genuinely long in-flight
		// whisper-cli transcription survive SIGTERM instead of being
		// aborted+requeued.
		ShutdownTimeout: envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second) + 10*time.Second,
	})

	// KEDA-01/D-01: the queue-depth collector now lives solely on the
	// always-on api process (cmd/api/main.go) — a worker Deployment scaled to
	// genuine 0 replicas by KEDA would otherwise have no pod exposing the
	// metric KEDA needs to scale it back up. /metrics here still serves the
	// promauto-registered job/duration metrics; the endpoint itself is
	// unchanged. NewQueueDepthCollector's QueueAudio wiring is explicitly
	// out of scope for this phase (AUD-08, Phase 33) — not touched here.

	log.Printf("🐙 audio-worker starting (queue=%s)", queue.QueueAudio)
	if err := srv.Start(mux); err != nil {
		log.Fatalf("audio-worker: %v", err)
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
	log.Println("🛑 shutting down audio-worker...")
	srv.Shutdown()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("metrics graceful shutdown failed: %v", err)
	}
	log.Println("bye 👋")
}

// resolveAudioThreads implements the AUDIO_THREADS -> cgroup -> NumCPU
// precedence chain (T-32-04/AUD-07 SC4): an explicit operator override
// (AUDIO_THREADS, an escape hatch matching every other tunable's env-override
// convention) wins outright when set to a positive value; otherwise the
// container's real cgroup v2 CPU quota (convert.CgroupCPULimit()) is used
// when available; a process running outside any cgroup v2 container (e.g.
// local `go run` dev flow, or a cgroup v1 host) falls through to
// runtime.NumCPU() as the last resort. Returns the resolved thread count
// and a short source label for startup logging.
func resolveAudioThreads() (int, string) {
	if n := envInt("AUDIO_THREADS", 0); n > 0 {
		return n, "env override"
	}
	if n, ok := convert.CgroupCPULimit(); ok {
		return n, "cgroup"
	}
	return runtime.NumCPU(), "NumCPU fallback"
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
// invites a bare integer-seconds value (WR-05): time.ParseDuration alone
// REJECTS "14400" ("missing unit in duration"), and the codebase-wide
// silent-fallback pattern would then quietly replace an operator's explicit
// ceiling with the default — unacceptable for a fail-closed resource guard.
// Accepts, in order: Go duration syntax ("4h", "14400s") and bare
// non-negative integer seconds ("14400"), both tolerant of a trailing inline
// comment via firstField (same convention as envDuration/envInt). A set-but-
// unparseable value is logged before falling back to def — this env guards a
// security ceiling, so the fallback must never be silent.
func envDurationSeconds(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f := firstField(v)
	// WR-02 (review): both parse branches reject negatives -- a negative
	// ceiling makes EnforceMaxDuration (d > max) terminally reject EVERY
	// job, so "-5s"/"-30m" must fall through to the logged-warning default
	// below, never be returned silently. Zero stays valid: it is an
	// explicit reject-everything ceiling, consistent with the bare-integer
	// branch's sec >= 0.
	if d, err := time.ParseDuration(f); err == nil && d >= 0 {
		return d
	}
	if sec, err := strconv.Atoi(f); err == nil && sec >= 0 {
		return time.Duration(sec) * time.Second
	}
	log.Printf("⚠️ %s=%q is neither a duration (\"4h\") nor bare integer seconds (\"14400\"); using default %v", key, f, def)
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

// stripInlineComment conservatively removes a trailing inline "# comment"
// from a path-valued env var (WR-06). Unlike firstField (which cuts at the
// FIRST space and would truncate a path that legitimately contains spaces),
// it only cuts at a '#' immediately preceded by a space or tab — the only
// shape a .env-style inline comment takes — then trims surrounding
// whitespace. A '#' embedded directly in the path itself (no preceding
// whitespace) is preserved.
func stripInlineComment(s string) string {
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			s = s[:i]
			break
		}
	}
	return strings.TrimSpace(s)
}
