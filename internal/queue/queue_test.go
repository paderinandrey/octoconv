package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func TestConvertPayloadRoundTrip(t *testing.T) {
	id := uuid.New()
	task, err := NewImageConvertTask(id, 4, ImageUniqueTTL(4, 120*time.Second))
	if err != nil {
		t.Fatalf("NewImageConvertTask: %v", err)
	}
	if task.Type() != TypeImageConvert {
		t.Errorf("task type = %q, want %q", task.Type(), TypeImageConvert)
	}
	p, err := ParseConvertPayload(task.Payload())
	if err != nil {
		t.Fatalf("ParseConvertPayload: %v", err)
	}
	if p.JobID != id {
		t.Errorf("job id = %s, want %s", p.JobID, id)
	}
}

// TestWebhookRetryDelaySchedule asserts WebhookRetryDelay indexes
// webhookRetrySchedule with asynq's 0-based retry count (msg.Retried),
// within the ±25% jitter band, and clamps at the schedule's last entry.
func TestWebhookRetryDelaySchedule(t *testing.T) {
	schedule := []time.Duration{
		30 * time.Second,
		1 * time.Minute,
		2 * time.Minute,
		4 * time.Minute,
		8 * time.Minute,
		15 * time.Minute,
	}
	cases := []struct {
		n    int
		base time.Duration
	}{
		{n: 0, base: schedule[0]},
		{n: 1, base: schedule[1]},
		{n: 5, base: schedule[5]},
		{n: 6, base: schedule[5]},  // clamps past the end of the schedule
		{n: 100, base: schedule[5]},
	}
	for _, tc := range cases {
		delay := WebhookRetryDelay(tc.n, nil, nil)
		lo := time.Duration(float64(tc.base) * 0.75)
		hi := time.Duration(float64(tc.base) * 1.25)
		if delay < lo || delay > hi {
			t.Errorf("WebhookRetryDelay(%d) = %v, want within [%v, %v] (base %v)", tc.n, delay, lo, hi, tc.base)
		}
	}
}

// TestImageRetryDelaySchedule asserts ImageRetryDelay returns the exact
// 2s/5s/15s schedule (no jitter, D-06) and clamps on both ends.
func TestImageRetryDelaySchedule(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{n: -1, want: 2 * time.Second}, // clamps to first entry
		{n: 0, want: 2 * time.Second},
		{n: 1, want: 5 * time.Second},
		{n: 2, want: 15 * time.Second},
		{n: 3, want: 15 * time.Second}, // clamps past the end of the schedule
		{n: 100, want: 15 * time.Second},
	}
	for _, tc := range cases {
		got := ImageRetryDelay(tc.n, nil, nil)
		if got != tc.want {
			t.Errorf("ImageRetryDelay(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}
}

// TestRetryDelayFuncDispatch asserts RetryDelayFunc routes each task type to
// its own retry delay function: image tasks get the exact seconds-scale
// schedule, webhook tasks stay within the existing ±25% jitter band, and an
// unknown task type falls back to asynq's own default.
func TestRetryDelayFuncDispatch(t *testing.T) {
	imageTask := asynq.NewTask(TypeImageConvert, nil)
	webhookTask := asynq.NewTask(TypeWebhookDeliver, nil)
	otherTask := asynq.NewTask("some:other-type", nil)

	if got, want := RetryDelayFunc(0, nil, imageTask), 2*time.Second; got != want {
		t.Errorf("RetryDelayFunc(image, 0) = %v, want %v", got, want)
	}
	if got, want := RetryDelayFunc(2, nil, imageTask), 15*time.Second; got != want {
		t.Errorf("RetryDelayFunc(image, 2) = %v, want %v", got, want)
	}

	base := 30 * time.Second
	lo := time.Duration(float64(base) * 0.75)
	hi := time.Duration(float64(base) * 1.25)
	if got := RetryDelayFunc(0, nil, webhookTask); got < lo || got > hi {
		t.Errorf("RetryDelayFunc(webhook, 0) = %v, want within [%v, %v]", got, lo, hi)
	}

	// asynq.DefaultRetryDelayFunc(0, ...) = 0^4 + 15 + rand(30)*1 seconds
	// (Sidekiq formula, server.go:403) — random, so assert the dispatched
	// value falls in its known [15s, 45s) range rather than exact equality.
	got := RetryDelayFunc(0, nil, otherTask)
	if lo, hi := 15*time.Second, 45*time.Second; got < lo || got >= hi {
		t.Errorf("RetryDelayFunc(other, 0) = %v, want within [%v, %v) (asynq default)", got, lo, hi)
	}
}

// TestImageUniqueTTL asserts ImageUniqueTTL derives its result from the
// corrected worst-case retry lifetime ((maxRetry+1) executions, since
// asynq's archive condition msg.Retried >= msg.Retry is checked AFTER each
// failed attempt) and that the derived TTL always strictly exceeds that
// worst-case lifetime.
func TestImageUniqueTTL(t *testing.T) {
	maxRetry := 4
	engineTimeout := 120 * time.Second
	backoffSum := 2*time.Second + 5*time.Second + 15*time.Second + 15*time.Second // i=0..3, clamped at i>=2

	want := time.Duration(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin
	got := ImageUniqueTTL(maxRetry, engineTimeout)
	if got != want {
		t.Errorf("ImageUniqueTTL(%d, %v) = %v, want %v", maxRetry, engineTimeout, got, want)
	}

	worstCaseRetryLifetime := time.Duration(maxRetry+1)*engineTimeout + backoffSum
	if got <= worstCaseRetryLifetime {
		t.Errorf("ImageUniqueTTL(%d, %v) = %v, want strictly greater than worst-case retry lifetime %v", maxRetry, engineTimeout, got, worstCaseRetryLifetime)
	}

	// Clamp case: maxRetry=6 sums backoff for i=0..5, clamped to the last
	// schedule entry (15s) for i>=2.
	maxRetry6 := 6
	backoffSum6 := 2*time.Second + 5*time.Second + 15*time.Second + 15*time.Second + 15*time.Second + 15*time.Second
	want6 := time.Duration(maxRetry6+1)*engineTimeout + backoffSum6 + uniqueTTLSafetyMargin
	if got6 := ImageUniqueTTL(maxRetry6, engineTimeout); got6 != want6 {
		t.Errorf("ImageUniqueTTL(%d, %v) = %v, want %v", maxRetry6, engineTimeout, got6, want6)
	}

	// Monotonicity: raising either argument must never shrink the TTL.
	if ImageUniqueTTL(maxRetry+1, engineTimeout) <= ImageUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("ImageUniqueTTL must grow monotonically with maxRetry")
	}
	if ImageUniqueTTL(maxRetry, engineTimeout+time.Second) <= ImageUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("ImageUniqueTTL must grow monotonically with engineTimeout")
	}
}

// TestEnqueueImageConvert enqueues a task and confirms it lands in the image
// queue. Requires a live Redis (REDIS_ADDR); skipped otherwise.
func TestEnqueueImageConvert(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("REDIS_ADDR not set; skipping integration test")
	}

	cl, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cl.Close()

	id := uuid.New()
	if err := cl.EnqueueImageConvert(context.Background(), id); err != nil {
		t.Fatalf("EnqueueImageConvert: %v", err)
	}

	opt, _ := RedisOpt()
	insp := asynq.NewInspector(opt)
	defer insp.Close()

	infos, err := insp.ListPendingTasks(QueueImage)
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	found := false
	for _, ti := range infos {
		p, err := ParseConvertPayload(ti.Payload)
		if err == nil && p.JobID == id {
			found = true
			if ti.Type != TypeImageConvert {
				t.Errorf("enqueued type = %q, want %q", ti.Type, TypeImageConvert)
			}
			// cleanup
			_ = insp.DeleteTask(QueueImage, ti.ID)
			break
		}
	}
	if !found {
		t.Errorf("enqueued task for job %s not found in queue %q", id, QueueImage)
	}
}
