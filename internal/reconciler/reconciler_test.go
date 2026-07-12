package reconciler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"

	"github.com/apaderin/octoconv/internal/jobs"
)

// fakeStore is an in-memory jobStore implementation for unit tests, with
// configurable errors and call tracking so tests can exercise the sweeper's
// enqueue-first ordering and bounded RequeueStale retry without a live DB.
type fakeStore struct {
	stale         []jobs.StaleJob
	findStaleErr  error
	recoveryCount map[uuid.UUID]int
	jobs          map[uuid.UUID]*jobs.Job

	requeueStaleCalls int
	// requeueStaleErrs, if non-empty, is popped one error per call (nil
	// entries mean "succeed"); once exhausted, calls succeed.
	requeueStaleErrs []error

	markFailedCalls []uuid.UUID

	webhookGaps              []jobs.WebhookGapJob
	findWebhookGapsErr       error
	webhookGapRecoveredCalls []uuid.UUID
}

func (f *fakeStore) FindStale(ctx context.Context, queuedStaleAfter, activeStaleAfter time.Duration) ([]jobs.StaleJob, error) {
	if f.findStaleErr != nil {
		return nil, f.findStaleErr
	}
	return f.stale, nil
}

func (f *fakeStore) RecoveryCount(ctx context.Context, id uuid.UUID) (int, error) {
	return f.recoveryCount[id], nil
}

func (f *fakeStore) RequeueStale(ctx context.Context, id uuid.UUID, reason string) error {
	idx := f.requeueStaleCalls
	f.requeueStaleCalls++
	if idx < len(f.requeueStaleErrs) && f.requeueStaleErrs[idx] != nil {
		return f.requeueStaleErrs[idx]
	}
	f.recoveryCount[id]++
	return nil
}

func (f *fakeStore) MarkFailed(ctx context.Context, id uuid.UUID, code, message string, detail map[string]any) error {
	f.markFailedCalls = append(f.markFailedCalls, id)
	return nil
}

func (f *fakeStore) Get(ctx context.Context, id uuid.UUID) (*jobs.Job, error) {
	if j, ok := f.jobs[id]; ok {
		return j, nil
	}
	return &jobs.Job{ID: id}, nil
}

func (f *fakeStore) FindWebhookGaps(ctx context.Context, activeStaleAfter time.Duration) ([]jobs.WebhookGapJob, error) {
	if f.findWebhookGapsErr != nil {
		return nil, f.findWebhookGapsErr
	}
	return f.webhookGaps, nil
}

func (f *fakeStore) RecordWebhookGapRecovered(ctx context.Context, id uuid.UUID, status string) error {
	f.webhookGapRecoveredCalls = append(f.webhookGapRecoveredCalls, id)
	return nil
}

// fakeEnqueuer is an in-memory enqueuer implementation for unit tests. The
// synchronous unit tests below read the call-counter slices directly after
// sweep() returns (single-threaded, race-free); the soak test in
// reconciler_soak_test.go, however, reads a counter while a live Sweeper.Run
// goroutine is concurrently appending to it, so the slices are guarded by mu
// and exposed only through locked snapshot accessors for that concurrent
// reader.
type fakeEnqueuer struct {
	mu sync.Mutex

	enqueueImageErr    error
	imageCalls         []uuid.UUID
	webhookCalls       []uuid.UUID
	enqueueWebhookErr  error
	enqueueDocumentErr error
	documentCalls      []uuid.UUID
	enqueueHTMLErr     error
	htmlCalls          []uuid.UUID
}

func (f *fakeEnqueuer) EnqueueImageConvert(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.imageCalls = append(f.imageCalls, id)
	return f.enqueueImageErr
}

func (f *fakeEnqueuer) EnqueueWebhookDeliver(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.webhookCalls = append(f.webhookCalls, id)
	return f.enqueueWebhookErr
}

func (f *fakeEnqueuer) EnqueueDocumentConvert(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.documentCalls = append(f.documentCalls, id)
	return f.enqueueDocumentErr
}

func (f *fakeEnqueuer) EnqueueHTMLConvert(ctx context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.htmlCalls = append(f.htmlCalls, id)
	return f.enqueueHTMLErr
}

// imageCallIDs returns a locked snapshot copy of imageCalls — used by the
// soak test to read the counter concurrently with a running Sweeper.Run
// goroutine without racing on the underlying slice.
func (f *fakeEnqueuer) imageCallIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uuid.UUID, len(f.imageCalls))
	copy(out, f.imageCalls)
	return out
}

// webhookCallIDs returns a locked snapshot copy of webhookCalls.
func (f *fakeEnqueuer) webhookCallIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uuid.UUID, len(f.webhookCalls))
	copy(out, f.webhookCalls)
	return out
}

// documentCallIDs returns a locked snapshot copy of documentCalls.
func (f *fakeEnqueuer) documentCallIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uuid.UUID, len(f.documentCalls))
	copy(out, f.documentCalls)
	return out
}

// htmlCallIDs returns a locked snapshot copy of htmlCalls.
func (f *fakeEnqueuer) htmlCallIDs() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uuid.UUID, len(f.htmlCalls))
	copy(out, f.htmlCalls)
	return out
}

func testConfig() Config {
	return Config{
		QueuedStaleAfter: 90 * time.Second,
		ActiveStaleAfter: 5 * time.Minute,
		SweepInterval:    10 * time.Millisecond,
		MaxRecoveries:    3,
	}
}

func TestSweepRecoversUnderCap(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.imageCalls) != 1 {
		t.Fatalf("EnqueueImageConvert calls = %d, want 1", len(enq.imageCalls))
	}
	if len(enq.documentCalls) != 0 {
		t.Fatalf("EnqueueDocumentConvert should not be called for an image job, got %d calls", len(enq.documentCalls))
	}
	if store.requeueStaleCalls != 1 {
		t.Fatalf("RequeueStale calls = %d, want 1", store.requeueStaleCalls)
	}
	if len(store.markFailedCalls) != 0 {
		t.Fatalf("MarkFailed should not be called, got %d calls", len(store.markFailedCalls))
	}
}

func TestSweepRoutesDocumentJobsToDocumentQueue(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "document"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.documentCalls) != 1 || enq.documentCalls[0] != id {
		t.Fatalf("EnqueueDocumentConvert calls = %+v, want [%s]", enq.documentCalls, id)
	}
	if len(enq.imageCalls) != 0 {
		t.Fatalf("EnqueueImageConvert should not be called for a document job, got %d calls", len(enq.imageCalls))
	}
	if store.requeueStaleCalls != 1 {
		t.Fatalf("RequeueStale calls = %d, want 1", store.requeueStaleCalls)
	}
	if len(store.markFailedCalls) != 0 {
		t.Fatalf("MarkFailed should not be called, got %d calls", len(store.markFailedCalls))
	}
}

func TestSweepRoutesHTMLJobsToHTMLQueue(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "html"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.htmlCalls) != 1 || enq.htmlCalls[0] != id {
		t.Fatalf("EnqueueHTMLConvert calls = %+v, want [%s]", enq.htmlCalls, id)
	}
	if len(enq.imageCalls) != 0 {
		t.Fatalf("EnqueueImageConvert should not be called for an html job, got %d calls", len(enq.imageCalls))
	}
	if len(enq.documentCalls) != 0 {
		t.Fatalf("EnqueueDocumentConvert should not be called for an html job, got %d calls", len(enq.documentCalls))
	}
	if store.requeueStaleCalls != 1 {
		t.Fatalf("RequeueStale calls = %d, want 1", store.requeueStaleCalls)
	}
	if len(store.markFailedCalls) != 0 {
		t.Fatalf("MarkFailed should not be called, got %d calls", len(store.markFailedCalls))
	}
}

func TestSweepSkipsUnknownEngine(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "av"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.imageCalls) != 0 {
		t.Fatalf("EnqueueImageConvert should not be called for an unrecognized engine, got %d calls", len(enq.imageCalls))
	}
	if len(enq.documentCalls) != 0 {
		t.Fatalf("EnqueueDocumentConvert should not be called for an unrecognized engine, got %d calls", len(enq.documentCalls))
	}
	if store.requeueStaleCalls != 0 {
		t.Fatalf("RequeueStale should not be called for an unrecognized engine, got %d calls", store.requeueStaleCalls)
	}
	if len(store.markFailedCalls) != 0 {
		t.Fatalf("MarkFailed should not be called, got %d calls", len(store.markFailedCalls))
	}
}

func TestSweepSkipsDuplicateEnqueue(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusQueued, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
	}
	enq := &fakeEnqueuer{enqueueImageErr: asynq.ErrDuplicateTask}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.imageCalls) != 1 {
		t.Fatalf("EnqueueImageConvert calls = %d, want 1", len(enq.imageCalls))
	}
	if store.requeueStaleCalls != 0 {
		t.Fatalf("RequeueStale should NOT be called on duplicate enqueue, got %d calls", store.requeueStaleCalls)
	}
	if len(store.markFailedCalls) != 0 {
		t.Fatalf("MarkFailed should not be called, got %d calls", len(store.markFailedCalls))
	}
	if store.recoveryCount[id] != 0 {
		t.Fatalf("recovery count = %d, want 0 (no spurious recovery recorded)", store.recoveryCount[id])
	}
}

func TestSweepRequeueStaleRetriedOnce(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:            []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount:    map[uuid.UUID]int{id: 0},
		requeueStaleErrs: []error{errors.New("transient write failure"), nil},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if store.requeueStaleCalls != 2 {
		t.Fatalf("RequeueStale calls = %d, want 2", store.requeueStaleCalls)
	}
	if store.recoveryCount[id] != 1 {
		t.Fatalf("recovery count = %d, want 1 (ultimately recorded)", store.recoveryCount[id])
	}
}

func TestSweepRequeueStaleBoundedRetry(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 0},
		requeueStaleErrs: []error{
			errors.New("transient write failure"),
			errors.New("transient write failure again"),
		},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	done := make(chan struct{})
	go func() {
		s.sweep(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep did not return — possible unbounded retry loop")
	}

	if store.requeueStaleCalls != 2 {
		t.Fatalf("RequeueStale calls = %d, want exactly 2 (bounded)", store.requeueStaleCalls)
	}
	if len(store.markFailedCalls) != 0 {
		t.Fatalf("MarkFailed should not be called, got %d calls", len(store.markFailedCalls))
	}
}

func TestSweepExhaustsAtCap(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 3},
		jobs:          map[uuid.UUID]*jobs.Job{id: {ID: id, CallbackURL: "https://example.test/hook"}},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(store.markFailedCalls) != 1 || store.markFailedCalls[0] != id {
		t.Fatalf("MarkFailed calls = %+v, want [%s]", store.markFailedCalls, id)
	}
	if len(enq.webhookCalls) != 1 || enq.webhookCalls[0] != id {
		t.Fatalf("EnqueueWebhookDeliver calls = %+v, want [%s]", enq.webhookCalls, id)
	}
	if store.requeueStaleCalls != 0 {
		t.Fatalf("RequeueStale should not be called at cap, got %d calls", store.requeueStaleCalls)
	}
}

func TestSweepExhaustNoCallbackNoWebhook(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		stale:         []jobs.StaleJob{{ID: id, Status: jobs.StatusActive, Engine: "image"}},
		recoveryCount: map[uuid.UUID]int{id: 3},
		jobs:          map[uuid.UUID]*jobs.Job{id: {ID: id, CallbackURL: ""}},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(store.markFailedCalls) != 1 {
		t.Fatalf("MarkFailed calls = %d, want 1", len(store.markFailedCalls))
	}
	if len(enq.webhookCalls) != 0 {
		t.Fatalf("EnqueueWebhookDeliver should not be called without callback_url, got %d calls", len(enq.webhookCalls))
	}
}

func TestSweepRecoversWebhookGap(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		recoveryCount: map[uuid.UUID]int{},
		webhookGaps:   []jobs.WebhookGapJob{{ID: id, Status: jobs.StatusDone}},
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.webhookCalls) != 1 || enq.webhookCalls[0] != id {
		t.Fatalf("EnqueueWebhookDeliver calls = %+v, want [%s]", enq.webhookCalls, id)
	}
	if len(store.webhookGapRecoveredCalls) != 1 || store.webhookGapRecoveredCalls[0] != id {
		t.Fatalf("RecordWebhookGapRecovered calls = %+v, want [%s]", store.webhookGapRecoveredCalls, id)
	}
}

func TestSweepSkipsDuplicateWebhookGap(t *testing.T) {
	id := uuid.New()
	store := &fakeStore{
		recoveryCount: map[uuid.UUID]int{},
		webhookGaps:   []jobs.WebhookGapJob{{ID: id, Status: jobs.StatusFailed}},
	}
	enq := &fakeEnqueuer{enqueueWebhookErr: asynq.ErrDuplicateTask}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.webhookCalls) != 1 || enq.webhookCalls[0] != id {
		t.Fatalf("EnqueueWebhookDeliver calls = %+v, want [%s]", enq.webhookCalls, id)
	}
	if len(store.webhookGapRecoveredCalls) != 0 {
		t.Fatalf("RecordWebhookGapRecovered should NOT be called on duplicate enqueue, got %d calls", len(store.webhookGapRecoveredCalls))
	}
}

func TestSweepWebhookGapFindErrorBestEffort(t *testing.T) {
	store := &fakeStore{
		recoveryCount:      map[uuid.UUID]int{},
		findWebhookGapsErr: errors.New("transient query failure"),
	}
	enq := &fakeEnqueuer{}
	s := NewSweeper(store, enq, testConfig())

	s.sweep(context.Background())

	if len(enq.webhookCalls) != 0 {
		t.Fatalf("EnqueueWebhookDeliver should not be called on FindWebhookGaps error, got %d calls", len(enq.webhookCalls))
	}
	if len(store.webhookGapRecoveredCalls) != 0 {
		t.Fatalf("RecordWebhookGapRecovered should not be called on FindWebhookGaps error, got %d calls", len(store.webhookGapRecoveredCalls))
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	store := &fakeStore{recoveryCount: map[uuid.UUID]int{}}
	enq := &fakeEnqueuer{}
	cfg := testConfig()
	cfg.SweepInterval = 5 * time.Millisecond
	s := NewSweeper(store, enq, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return within 1s of context cancellation")
	}
}
