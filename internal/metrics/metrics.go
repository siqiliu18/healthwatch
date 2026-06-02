package metrics

import "github.com/prometheus/client_golang/prometheus"

// QueueDepth is updated on every KEDA poll (GET /metrics/queue-depth).
// Prometheus scraping /metrics will see the most recently observed value.
var QueueDepth = prometheus.NewGauge(prometheus.GaugeOpts{
	Name: "healthwatch_queue_pending",
	Help: "Current count of pending check jobs",
})

func init() {
	prometheus.MustRegister(QueueDepth)
}
