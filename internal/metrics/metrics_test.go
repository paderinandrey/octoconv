package metrics

import (
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordJobOutcome(t *testing.T) {
	before := testutil.ToFloat64(jobOutcomes.WithLabelValues("image", "done"))
	RecordJobOutcome("image", "done", 50*time.Millisecond)
	after := testutil.ToFloat64(jobOutcomes.WithLabelValues("image", "done"))
	if after != before+1 {
		t.Fatalf("job outcome counter: want %v, got %v", before+1, after)
	}
}

func TestRecordWebhookDelivery(t *testing.T) {
	beforeSuccess := testutil.ToFloat64(webhookDeliveries.WithLabelValues("success"))
	beforeFailure := testutil.ToFloat64(webhookDeliveries.WithLabelValues("failure"))

	RecordWebhookDelivery(true)
	RecordWebhookDelivery(false)

	afterSuccess := testutil.ToFloat64(webhookDeliveries.WithLabelValues("success"))
	afterFailure := testutil.ToFloat64(webhookDeliveries.WithLabelValues("failure"))

	if afterSuccess != beforeSuccess+1 {
		t.Fatalf("success counter: want %v, got %v", beforeSuccess+1, afterSuccess)
	}
	if afterFailure != beforeFailure+1 {
		t.Fatalf("failure counter: want %v, got %v", beforeFailure+1, afterFailure)
	}
}

func TestRecordReconcilerAction(t *testing.T) {
	before := testutil.ToFloat64(reconcilerActions.WithLabelValues("recovered"))
	RecordReconcilerAction("recovered")
	after := testutil.ToFloat64(reconcilerActions.WithLabelValues("recovered"))
	if after != before+1 {
		t.Fatalf("recovered counter: want %v, got %v", before+1, after)
	}
}

func TestNewQueueDepthCollectorDescribe(t *testing.T) {
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: "127.0.0.1:0"})
	c := NewQueueDepthCollector(inspector, "image", "webhook")

	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)

	count := 0
	for range ch {
		count++
	}
	if count != 1 {
		t.Fatalf("Describe: want exactly 1 descriptor, got %d", count)
	}
}

// TestNewQueueDepthCollectorDescribeAllFourQueues is the Redis-free half of
// D-03 (KEDA-01, Phase 27 Plan 01): constructing the collector with all four
// engine-class queue names — the exact set now registered once on the
// always-on api process (cmd/api/main.go), not per-worker — must not panic
// and must still Describe exactly one octoconv_queue_depth descriptor,
// regardless of how many queue names are passed in. The per-queue value
// proof (that Collect() actually reports a series for each queue) lives in
// the compose-E2E test (TestQueueDepthMetricRelocationE2E), since Collect
// needs a live Redis to query via the Inspector.
func TestNewQueueDepthCollectorDescribeAllFourQueues(t *testing.T) {
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: "127.0.0.1:0"})
	c := NewQueueDepthCollector(inspector, "image", "document", "html", "webhook")

	ch := make(chan *prometheus.Desc, 10)
	c.Describe(ch)
	close(ch)

	count := 0
	for range ch {
		count++
	}
	if count != 1 {
		t.Fatalf("Describe: want exactly 1 descriptor, got %d", count)
	}
}
