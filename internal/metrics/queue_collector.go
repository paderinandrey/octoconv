package metrics

import (
	"github.com/hibiken/asynq"
	"github.com/prometheus/client_golang/prometheus"
)

var queueDepthDesc = prometheus.NewDesc(
	"octoconv_queue_depth",
	"Number of tasks per asynq queue and state.",
	[]string{"queue", "state"},
	nil,
)

// queueDepthCollector is a pull-based prometheus.Collector that reports
// per-queue task counts by state at scrape time, via asynq's Inspector.
type queueDepthCollector struct {
	inspector *asynq.Inspector
	queues    []string
}

// NewQueueDepthCollector returns a prometheus.Collector that reports
// pending/active/scheduled/retry/archived task counts for each given queue.
func NewQueueDepthCollector(inspector *asynq.Inspector, queues ...string) prometheus.Collector {
	return &queueDepthCollector{inspector: inspector, queues: queues}
}

func (c *queueDepthCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- queueDepthDesc
}

func (c *queueDepthCollector) Collect(ch chan<- prometheus.Metric) {
	for _, q := range c.queues {
		info, err := c.inspector.GetQueueInfo(q)
		if err != nil {
			// A Redis blip must not crash a scrape — skip this queue, the
			// next scrape retries.
			continue
		}
		ch <- prometheus.MustNewConstMetric(queueDepthDesc, prometheus.GaugeValue, float64(info.Pending), q, "pending")
		ch <- prometheus.MustNewConstMetric(queueDepthDesc, prometheus.GaugeValue, float64(info.Active), q, "active")
		ch <- prometheus.MustNewConstMetric(queueDepthDesc, prometheus.GaugeValue, float64(info.Scheduled), q, "scheduled")
		ch <- prometheus.MustNewConstMetric(queueDepthDesc, prometheus.GaugeValue, float64(info.Retry), q, "retry")
		ch <- prometheus.MustNewConstMetric(queueDepthDesc, prometheus.GaugeValue, float64(info.Archived), q, "archived")
	}
}
