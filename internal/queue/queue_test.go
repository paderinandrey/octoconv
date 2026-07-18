package queue

import (
	"context"
	"errors"
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
		{n: 6, base: schedule[5]}, // clamps past the end of the schedule
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
	documentTask := asynq.NewTask(TypeDocumentConvert, nil)
	otherTask := asynq.NewTask("some:other-type", nil)

	if got, want := RetryDelayFunc(0, nil, imageTask), 2*time.Second; got != want {
		t.Errorf("RetryDelayFunc(image, 0) = %v, want %v", got, want)
	}
	if got, want := RetryDelayFunc(2, nil, imageTask), 15*time.Second; got != want {
		t.Errorf("RetryDelayFunc(image, 2) = %v, want %v", got, want)
	}

	if got, want := RetryDelayFunc(0, nil, documentTask), 5*time.Second; got != want {
		t.Errorf("RetryDelayFunc(document, 0) = %v, want %v", got, want)
	}
	if got, want := RetryDelayFunc(2, nil, documentTask), 30*time.Second; got != want {
		t.Errorf("RetryDelayFunc(document, 2) = %v, want %v", got, want)
	}

	htmlTask := asynq.NewTask(TypeHTMLConvert, nil)
	if got, want := RetryDelayFunc(0, nil, htmlTask), 5*time.Second; got != want {
		t.Errorf("RetryDelayFunc(html, 0) = %v, want %v", got, want)
	}
	if got, want := RetryDelayFunc(2, nil, htmlTask), 30*time.Second; got != want {
		t.Errorf("RetryDelayFunc(html, 2) = %v, want %v", got, want)
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

// TestWebhookUniqueTTL asserts WebhookUniqueTTL derives its result from a
// jitter-inflated worst-case backoff sum (NOT from calling the jittered
// WebhookRetryDelay), evaluates to exactly 2477.5s for (6, 10s), and always
// strictly exceeds the worst-case retry lifetime, is monotonic, and is
// deterministic across repeated calls.
func TestWebhookUniqueTTL(t *testing.T) {
	maxRetry := 6
	perAttemptTimeout := 10 * time.Second
	// Jitter-inflated (x1.25) values computed directly from the raw
	// webhookRetrySchedule (30s,1m,2m,4m,8m,15m) — NOT via WebhookRetryDelay,
	// which would introduce non-determinism into this test.
	backoffSum := 37500*time.Millisecond + 75*time.Second + 150*time.Second + 300*time.Second + 600*time.Second + 1125*time.Second

	want := time.Duration(maxRetry+1)*perAttemptTimeout + backoffSum + uniqueTTLSafetyMargin
	got := WebhookUniqueTTL(maxRetry, perAttemptTimeout)
	if got != want {
		t.Errorf("WebhookUniqueTTL(%d, %v) = %v, want %v", maxRetry, perAttemptTimeout, got, want)
	}
	if want != 2477500*time.Millisecond {
		t.Errorf("expected worked example want = 2477.5s, got %v", want)
	}

	worstCaseRetryLifetime := time.Duration(maxRetry+1)*perAttemptTimeout + backoffSum
	if got <= worstCaseRetryLifetime {
		t.Errorf("WebhookUniqueTTL(%d, %v) = %v, want strictly greater than worst-case retry lifetime %v", maxRetry, perAttemptTimeout, got, worstCaseRetryLifetime)
	}

	// Monotonicity: raising either argument must never shrink the TTL.
	if WebhookUniqueTTL(maxRetry+1, perAttemptTimeout) <= WebhookUniqueTTL(maxRetry, perAttemptTimeout) {
		t.Errorf("WebhookUniqueTTL must grow monotonically with maxRetry")
	}
	if WebhookUniqueTTL(maxRetry, perAttemptTimeout+time.Second) <= WebhookUniqueTTL(maxRetry, perAttemptTimeout) {
		t.Errorf("WebhookUniqueTTL must grow monotonically with perAttemptTimeout")
	}

	// Determinism: no jittered call leaked into the derivation.
	if got2 := WebhookUniqueTTL(maxRetry, perAttemptTimeout); got2 != got {
		t.Errorf("WebhookUniqueTTL is not deterministic: %v != %v", got2, got)
	}
}

// TestDocumentConvertTaskRoundTrip asserts NewDocumentConvertTask builds a
// task routed to TypeDocumentConvert whose payload round-trips through
// ParseConvertPayload back to the same job id — mirrors
// TestConvertPayloadRoundTrip's shape for the document engine class.
func TestDocumentConvertTaskRoundTrip(t *testing.T) {
	id := uuid.New()
	task, err := NewDocumentConvertTask(id, 3, DocumentUniqueTTL(3, 300*time.Second))
	if err != nil {
		t.Fatalf("NewDocumentConvertTask: %v", err)
	}
	if task.Type() != TypeDocumentConvert {
		t.Errorf("task type = %q, want %q", task.Type(), TypeDocumentConvert)
	}
	p, err := ParseConvertPayload(task.Payload())
	if err != nil {
		t.Fatalf("ParseConvertPayload: %v", err)
	}
	if p.JobID != id {
		t.Errorf("job id = %s, want %s", p.JobID, id)
	}
}

// TestDocumentRetryDelaySchedule asserts DocumentRetryDelay returns the exact
// 5s/15s/30s schedule (no jitter, mirrors ImageRetryDelay's shape) and clamps
// on both ends.
func TestDocumentRetryDelaySchedule(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{n: -1, want: 5 * time.Second}, // clamps to first entry
		{n: 0, want: 5 * time.Second},
		{n: 1, want: 15 * time.Second},
		{n: 2, want: 30 * time.Second},
		{n: 99, want: 30 * time.Second}, // clamps past the end of the schedule
	}
	for _, tc := range cases {
		got := DocumentRetryDelay(tc.n, nil, nil)
		if got != tc.want {
			t.Errorf("DocumentRetryDelay(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}
}

// TestDocumentUniqueTTL asserts DocumentUniqueTTL derives its result from the
// corrected worst-case retry lifetime ((maxRetry+1) executions) exactly like
// ImageUniqueTTL, evaluates to exactly 1370s for (3, 300s), and is
// monotonic in both arguments.
func TestDocumentUniqueTTL(t *testing.T) {
	maxRetry := 3
	engineTimeout := 300 * time.Second
	backoffSum := 5*time.Second + 15*time.Second + 30*time.Second // i=0..2

	want := time.Duration(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin
	got := DocumentUniqueTTL(maxRetry, engineTimeout)
	if got != want {
		t.Errorf("DocumentUniqueTTL(%d, %v) = %v, want %v", maxRetry, engineTimeout, got, want)
	}
	if want != 1370*time.Second {
		t.Errorf("expected worked example want = 1370s, got %v", want)
	}

	// Monotonicity: raising either argument must never shrink the TTL.
	if DocumentUniqueTTL(maxRetry+1, engineTimeout) <= DocumentUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("DocumentUniqueTTL must grow monotonically with maxRetry")
	}
	if DocumentUniqueTTL(maxRetry, engineTimeout+time.Second) <= DocumentUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("DocumentUniqueTTL must grow monotonically with engineTimeout")
	}
}

// TestHTMLConvertTaskRoundTrip asserts NewHTMLConvertTask builds a task
// routed to TypeHTMLConvert whose payload round-trips through
// ParseConvertPayload back to the same job id — mirrors
// TestDocumentConvertTaskRoundTrip's shape for the html engine class.
func TestHTMLConvertTaskRoundTrip(t *testing.T) {
	id := uuid.New()
	task, err := NewHTMLConvertTask(id, 3, HTMLUniqueTTL(3, 60*time.Second))
	if err != nil {
		t.Fatalf("NewHTMLConvertTask: %v", err)
	}
	if task.Type() != TypeHTMLConvert {
		t.Errorf("task type = %q, want %q", task.Type(), TypeHTMLConvert)
	}
	p, err := ParseConvertPayload(task.Payload())
	if err != nil {
		t.Fatalf("ParseConvertPayload: %v", err)
	}
	if p.JobID != id {
		t.Errorf("job id = %s, want %s", p.JobID, id)
	}
}

// TestHTMLRetryDelaySchedule asserts HTMLRetryDelay returns the exact
// 5s/15s/30s schedule (no jitter, mirrors DocumentRetryDelay's shape) and
// clamps on both ends.
func TestHTMLRetryDelaySchedule(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{n: -1, want: 5 * time.Second}, // clamps to first entry
		{n: 0, want: 5 * time.Second},
		{n: 1, want: 15 * time.Second},
		{n: 2, want: 30 * time.Second},
		{n: 99, want: 30 * time.Second}, // clamps past the end of the schedule
	}
	for _, tc := range cases {
		got := HTMLRetryDelay(tc.n, nil, nil)
		if got != tc.want {
			t.Errorf("HTMLRetryDelay(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}
}

// TestHTMLUniqueTTL asserts HTMLUniqueTTL derives its result from the
// corrected worst-case retry lifetime ((maxRetry+1) executions) exactly like
// DocumentUniqueTTL, evaluates to exactly 290s for (3, 60s), and is
// monotonic in both arguments.
func TestHTMLUniqueTTL(t *testing.T) {
	maxRetry := 3
	engineTimeout := 60 * time.Second
	backoffSum := 5*time.Second + 15*time.Second + 30*time.Second // i=0..2

	want := time.Duration(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin
	got := HTMLUniqueTTL(maxRetry, engineTimeout)
	if got != want {
		t.Errorf("HTMLUniqueTTL(%d, %v) = %v, want %v", maxRetry, engineTimeout, got, want)
	}
	if want != 410*time.Second {
		t.Errorf("expected worked example want = 410s, got %v", want)
	}

	// Monotonicity: raising either argument must never shrink the TTL.
	if HTMLUniqueTTL(maxRetry+1, engineTimeout) <= HTMLUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("HTMLUniqueTTL must grow monotonically with maxRetry")
	}
	if HTMLUniqueTTL(maxRetry, engineTimeout+time.Second) <= HTMLUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("HTMLUniqueTTL must grow monotonically with engineTimeout")
	}
}

// TestAudioConvertTaskRoundTrip asserts NewAudioConvertTask builds a task
// routed to TypeAudioConvert whose payload round-trips through
// ParseConvertPayload back to the same job id — mirrors
// TestHTMLConvertTaskRoundTrip's shape for the audio engine class.
func TestAudioConvertTaskRoundTrip(t *testing.T) {
	id := uuid.New()
	task, err := NewAudioConvertTask(id, 3, AudioUniqueTTL(3, 600*time.Second))
	if err != nil {
		t.Fatalf("NewAudioConvertTask: %v", err)
	}
	if task.Type() != TypeAudioConvert {
		t.Errorf("task type = %q, want %q", task.Type(), TypeAudioConvert)
	}
	p, err := ParseConvertPayload(task.Payload())
	if err != nil {
		t.Fatalf("ParseConvertPayload: %v", err)
	}
	if p.JobID != id {
		t.Errorf("job id = %s, want %s", p.JobID, id)
	}
}

// TestAudioRetryDelaySchedule asserts AudioRetryDelay returns the exact
// 5s/15s/30s schedule (no jitter, mirrors DocumentRetryDelay's/
// HTMLRetryDelay's shape) and clamps on both ends.
func TestAudioRetryDelaySchedule(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{n: -1, want: 5 * time.Second}, // clamps to first entry
		{n: 0, want: 5 * time.Second},
		{n: 1, want: 15 * time.Second},
		{n: 2, want: 30 * time.Second},
		{n: 99, want: 30 * time.Second}, // clamps past the end of the schedule
	}
	for _, tc := range cases {
		got := AudioRetryDelay(tc.n, nil, nil)
		if got != tc.want {
			t.Errorf("AudioRetryDelay(%d) = %v, want %v", tc.n, got, tc.want)
		}
	}
}

// TestAudioUniqueTTL asserts AudioUniqueTTL derives its result from the
// corrected worst-case retry lifetime ((maxRetry+1) executions) exactly like
// DocumentUniqueTTL/HTMLUniqueTTL, evaluates to exactly 2570s for (3, 600s),
// is monotonic in both arguments, and — the SC3-specific proof — strictly
// EXCEEDS the zero-margin worst-case retry lifetime, demonstrating
// uniqueTTLSafetyMargin is a load-bearing term, not accidentally zero.
func TestAudioUniqueTTL(t *testing.T) {
	maxRetry := 3
	engineTimeout := 600 * time.Second
	backoffSum := 5*time.Second + 15*time.Second + 30*time.Second // i=0..2

	want := time.Duration(maxRetry+1)*engineTimeout + backoffSum + uniqueTTLSafetyMargin
	got := AudioUniqueTTL(maxRetry, engineTimeout)
	if got != want {
		t.Errorf("AudioUniqueTTL(%d, %v) = %v, want %v", maxRetry, engineTimeout, got, want)
	}
	if want != 2570*time.Second {
		t.Errorf("expected worked example want = 2570s, got %v", want)
	}

	// Monotonicity: raising either argument must never shrink the TTL.
	if AudioUniqueTTL(maxRetry+1, engineTimeout) <= AudioUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("AudioUniqueTTL must grow monotonically with maxRetry")
	}
	if AudioUniqueTTL(maxRetry, engineTimeout+time.Second) <= AudioUniqueTTL(maxRetry, engineTimeout) {
		t.Errorf("AudioUniqueTTL must grow monotonically with engineTimeout")
	}

	// SC3: AudioUniqueTTL must strictly exceed the zero-margin worst-case
	// retry lifetime -- (maxRetry+1)*engineTimeout + audioBackoffSum(maxRetry)
	// -- proving the safety-margin term is load-bearing, not accidentally
	// zero.
	worstCaseNoMargin := time.Duration(maxRetry+1)*engineTimeout + audioBackoffSum(maxRetry)
	if AudioUniqueTTL(maxRetry, engineTimeout) <= worstCaseNoMargin {
		t.Errorf("AudioUniqueTTL(%d, %v) = %v must strictly exceed the zero-margin worst-case lifetime %v",
			maxRetry, engineTimeout, AudioUniqueTTL(maxRetry, engineTimeout), worstCaseNoMargin)
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

// TestEnqueueWebhookDeliverDuplicate asserts that a second
// EnqueueWebhookDeliver for the same job id, while the first task/lock is
// still live, returns asynq.ErrDuplicateTask instead of creating a second
// concurrent webhook task (D-01). Requires a live Redis (REDIS_ADDR);
// skipped otherwise.
func TestEnqueueWebhookDeliverDuplicate(t *testing.T) {
	if os.Getenv("REDIS_ADDR") == "" {
		t.Skip("REDIS_ADDR not set; skipping integration test")
	}

	cl, err := NewClient()
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer cl.Close()

	id := uuid.New()
	if err := cl.EnqueueWebhookDeliver(context.Background(), id); err != nil {
		t.Fatalf("first EnqueueWebhookDeliver: %v", err)
	}
	err = cl.EnqueueWebhookDeliver(context.Background(), id)
	if !errors.Is(err, asynq.ErrDuplicateTask) {
		t.Fatalf("second EnqueueWebhookDeliver = %v, want asynq.ErrDuplicateTask", err)
	}

	opt, _ := RedisOpt()
	insp := asynq.NewInspector(opt)
	defer insp.Close()

	infos, err := insp.ListPendingTasks(QueueWebhook)
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	for _, ti := range infos {
		p, perr := ParseWebhookPayload(ti.Payload)
		if perr == nil && p.JobID == id {
			_ = insp.DeleteTask(QueueWebhook, ti.ID)
			break
		}
	}
}
