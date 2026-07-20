package queue

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// Client enqueues conversion tasks. Wraps an asynq.Client.
type Client struct {
	c *asynq.Client

	// imageMaxRetry is the per-task MaxRetry budget for image conversion
	// tasks (IMAGE_MAX_RETRY, default 4 — D-05, small compared to
	// webhook's 6).
	imageMaxRetry int
	// imageUniqueTTL is the per-job asynq.Unique lock TTL for image
	// conversion tasks, derived once at construction from imageMaxRetry
	// and ENGINE_TIMEOUT via ImageUniqueTTL — see its doc comment for the
	// worst-case-lifetime derivation this TTL must always exceed.
	imageUniqueTTL time.Duration
	// webhookUniqueTTL is the per-job asynq.Unique lock TTL for webhook
	// delivery tasks, derived once at construction from the fixed
	// webhookMaxRetry/webhookPerAttemptTimeout constants via
	// WebhookUniqueTTL — see its doc comment for the worst-case-lifetime
	// derivation this TTL must always exceed. Unlike imageUniqueTTL, its
	// inputs are not env-configurable (D-05/Phase 2 fixed them).
	webhookUniqueTTL time.Duration
	// documentMaxRetry is the per-task MaxRetry budget for document
	// conversion tasks (DOCUMENT_MAX_RETRY, default 3 — bounded lower than
	// image's 4, since each document attempt is expensive at up to
	// DOCUMENT_ENGINE_TIMEOUT seconds and DOC-08 requires documents not be
	// retried forever).
	documentMaxRetry int
	// documentUniqueTTL is the per-job asynq.Unique lock TTL for document
	// conversion tasks, derived once at construction from documentMaxRetry
	// and DOCUMENT_ENGINE_TIMEOUT via DocumentUniqueTTL — see its doc
	// comment for the worst-case-lifetime derivation this TTL must always
	// exceed.
	documentUniqueTTL time.Duration
	// htmlMaxRetry is the per-task MaxRetry budget for html-to-pdf
	// conversion tasks (HTML_MAX_RETRY, default 3 — mirrors
	// documentMaxRetry's bounded-lower-than-image reasoning).
	htmlMaxRetry int
	// htmlUniqueTTL is the per-job asynq.Unique lock TTL for html-to-pdf
	// conversion tasks, derived once at construction from htmlMaxRetry and
	// HTML_ENGINE_TIMEOUT via HTMLUniqueTTL — see its doc comment for the
	// worst-case-lifetime derivation this TTL must always exceed.
	htmlUniqueTTL time.Duration
	// audioMaxRetry is the per-task MaxRetry budget for audio transcription
	// tasks (AUDIO_MAX_RETRY, default 3 — mirrors documentMaxRetry's/
	// htmlMaxRetry's bounded-lower-than-image reasoning).
	audioMaxRetry int
	// audioUniqueTTL is the per-job asynq.Unique lock TTL for audio
	// transcription tasks, derived once at construction from audioMaxRetry
	// and AUDIO_ENGINE_TIMEOUT via AudioUniqueTTL — see its doc comment for
	// the worst-case-lifetime derivation this TTL must always exceed.
	// Derived fresh, never reused from another engine class's TTL (per the
	// AudioUniqueTTL binding decision, STATE.md).
	audioUniqueTTL time.Duration
	// avMaxRetry is the per-task MaxRetry budget for av conversion tasks
	// (AV_MAX_RETRY, LOCKED default 2 per D-03 — fewer attempts than
	// audio's 3, since each av attempt costs materially more).
	avMaxRetry int
	// avUniqueTTL is the per-job asynq.Unique lock TTL for av conversion
	// tasks, derived once at construction from avMaxRetry and
	// AV_ENGINE_TIMEOUT via AVUniqueTTL. Derived fresh, never reused from
	// another engine class's TTL.
	avUniqueTTL time.Duration
}

// NewClient builds a queue client from REDIS_ADDR, IMAGE_MAX_RETRY (default
// 4), ENGINE_TIMEOUT (default 120s — same env var the worker reads to bound
// a conversion attempt), DOCUMENT_MAX_RETRY (default 3),
// DOCUMENT_ENGINE_TIMEOUT (default 300s — Phase 9 D-01), HTML_MAX_RETRY
// (default 3), HTML_ENGINE_TIMEOUT (default 60s), AUDIO_MAX_RETRY (default
// 3), AUDIO_ENGINE_TIMEOUT (default 600s — an [ASSUMED] placeholder; Phase 32
// re-derives this from real-time-factor measurement against the pinned
// whisper-cli model), AV_MAX_RETRY (default 2, D-03 LOCKED), and
// AV_ENGINE_TIMEOUT (default 600s — [ASSUMED] provisional, mirroring the
// exact precedent AUDIO_ENGINE_TIMEOUT set: a 600s Go-code placeholder later
// replaced by a 742s production value after Phase 32's RTF measurement;
// Phase 36 re-derives AV_ENGINE_TIMEOUT the same way from an RTF matrix).
//
// COUPLING (Pitfall 4, RESEARCH.md): RECONCILER_ACTIVE_STALE_AFTER is a
// single global threshold (default 900s) that must comfortably exceed every
// engine's *_ENGINE_TIMEOUT, or the reconciler will re-enqueue a still-
// legitimately-running job as stale. This is a documented near-miss from the
// audio engine (see docker-compose.yml): audio's duration-ceiling-derived
// timeout once breached the 900s/15m reconciler cap and forced
// AUDIO_MAX_DURATION_SECONDS to be lowered as a NO-GO lever. Any future
// raise of AV_ENGINE_TIMEOUT toward 900s is therefore a coupled decision
// requiring RECONCILER_ACTIVE_STALE_AFTER to be raised in the same change —
// do not raise one without the other.
func NewClient() (*Client, error) {
	opt, err := RedisOpt()
	if err != nil {
		return nil, err
	}
	imageMaxRetry := envInt("IMAGE_MAX_RETRY", 4)
	engineTimeout := envDuration("ENGINE_TIMEOUT", 120*time.Second)
	documentMaxRetry := envInt("DOCUMENT_MAX_RETRY", 3)
	documentEngineTimeout := envDuration("DOCUMENT_ENGINE_TIMEOUT", 300*time.Second)
	htmlMaxRetry := envInt("HTML_MAX_RETRY", 3)
	htmlEngineTimeout := envDuration("HTML_ENGINE_TIMEOUT", 60*time.Second)
	audioMaxRetry := envInt("AUDIO_MAX_RETRY", 3)
	audioEngineTimeout := envDuration("AUDIO_ENGINE_TIMEOUT", 600*time.Second)
	avMaxRetry := envInt("AV_MAX_RETRY", 2)
	avEngineTimeout := envDuration("AV_ENGINE_TIMEOUT", 600*time.Second)
	return &Client{
		c:                 asynq.NewClient(opt),
		imageMaxRetry:     imageMaxRetry,
		imageUniqueTTL:    ImageUniqueTTL(imageMaxRetry, engineTimeout),
		webhookUniqueTTL:  WebhookUniqueTTL(webhookMaxRetry, webhookPerAttemptTimeout),
		documentMaxRetry:  documentMaxRetry,
		documentUniqueTTL: DocumentUniqueTTL(documentMaxRetry, documentEngineTimeout),
		htmlMaxRetry:      htmlMaxRetry,
		htmlUniqueTTL:     HTMLUniqueTTL(htmlMaxRetry, htmlEngineTimeout),
		audioMaxRetry:     audioMaxRetry,
		audioUniqueTTL:    AudioUniqueTTL(audioMaxRetry, audioEngineTimeout),
		avMaxRetry:        avMaxRetry,
		avUniqueTTL:       AVUniqueTTL(avMaxRetry, avEngineTimeout),
	}, nil
}

// Close releases the underlying Redis connections.
func (c *Client) Close() error { return c.c.Close() }

// EnqueueImageConvert puts an image conversion job onto the image queue.
func (c *Client) EnqueueImageConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewImageConvertTask(jobID, c.imageMaxRetry, c.imageUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue image convert %s: %w", jobID, err)
	}
	return nil
}

// EnqueueWebhookDeliver puts a webhook delivery task onto the webhook queue.
func (c *Client) EnqueueWebhookDeliver(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewWebhookDeliverTask(jobID, c.webhookUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue webhook deliver %s: %w", jobID, err)
	}
	return nil
}

// EnqueueDocumentConvert puts a document conversion job onto the document
// queue.
func (c *Client) EnqueueDocumentConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewDocumentConvertTask(jobID, c.documentMaxRetry, c.documentUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue document convert %s: %w", jobID, err)
	}
	return nil
}

// EnqueueHTMLConvert puts an html-to-pdf conversion job onto the html
// queue.
func (c *Client) EnqueueHTMLConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewHTMLConvertTask(jobID, c.htmlMaxRetry, c.htmlUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue html convert %s: %w", jobID, err)
	}
	return nil
}

// EnqueueAudioConvert puts an audio transcription job onto the audio queue.
func (c *Client) EnqueueAudioConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewAudioConvertTask(jobID, c.audioMaxRetry, c.audioUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue audio convert %s: %w", jobID, err)
	}
	return nil
}

// EnqueueAVConvert puts a video conversion job onto the av queue.
func (c *Client) EnqueueAVConvert(ctx context.Context, jobID uuid.UUID) error {
	task, err := NewAVConvertTask(jobID, c.avMaxRetry, c.avUniqueTTL)
	if err != nil {
		return err
	}
	if _, err := c.c.EnqueueContext(ctx, task); err != nil {
		return fmt.Errorf("enqueue av convert %s: %w", jobID, err)
	}
	return nil
}

// envInt reads an integer environment variable, tolerating a trailing
// inline `# comment` (see firstField), falling back to def if unset or
// unparseable. Mirrors the convention in cmd/worker/main.go.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(firstField(v)); err == nil {
			return n
		}
	}
	return def
}

// envDuration reads a time.Duration environment variable, tolerating a
// trailing inline `# comment` (see firstField), falling back to def if
// unset or unparseable. Mirrors the convention in cmd/worker/main.go.
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(firstField(v)); err == nil {
			return d
		}
	}
	return def
}

// firstField strips a trailing whitespace-delimited inline comment from an
// env value, e.g. "120s   # comment" -> "120s". Duplicated from
// cmd/worker/main.go's helper of the same name (unexported, per-package
// convention — see cmd/api/main.go for the sibling copy).
func firstField(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\t' {
			return s[:i]
		}
	}
	return s
}
