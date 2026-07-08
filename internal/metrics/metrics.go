// Package metrics defines the Prometheus counters, histograms, and
// collectors this service exposes.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	jobOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "octoconv_job_outcomes_total",
		Help: "Total number of conversion jobs reaching a terminal state, labeled by engine and status.",
	}, []string{"engine", "status"})

	jobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "octoconv_job_duration_seconds",
		Help:    "Wall-clock duration of a conversion attempt, labeled by engine and status.",
		Buckets: prometheus.DefBuckets,
	}, []string{"engine", "status"})

	webhookDeliveries = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "octoconv_webhook_deliveries_total",
		Help: "Total number of webhook delivery attempts, labeled by result (success/failure).",
	}, []string{"result"})

	reconcilerActions = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "octoconv_reconciler_actions_total",
		Help: "Total number of reconciler actions, labeled by action (recovered/exhausted/webhook_gap_recovered).",
	}, []string{"action"})
)

// RecordJobOutcome increments the job-outcome counter and observes the
// conversion duration for the given engine and terminal status.
func RecordJobOutcome(engine, status string, d time.Duration) {
	jobOutcomes.WithLabelValues(engine, status).Inc()
	jobDuration.WithLabelValues(engine, status).Observe(d.Seconds())
}

// RecordWebhookDelivery increments the webhook-delivery counter for a
// success or failure result.
func RecordWebhookDelivery(success bool) {
	result := "failure"
	if success {
		result = "success"
	}
	webhookDeliveries.WithLabelValues(result).Inc()
}

// RecordReconcilerAction increments the reconciler-action counter for the
// given action ("recovered", "exhausted", or "webhook_gap_recovered").
func RecordReconcilerAction(action string) {
	reconcilerActions.WithLabelValues(action).Inc()
}
