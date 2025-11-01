package httpx

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	histogramBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10}
)

func (r *Router) initMetrics() {
	r.metricsOnce.Do(func() {
		r.requestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "peep",
			Subsystem: "api",
			Name:      "http_requests_total",
			Help:      "Count of processed HTTP requests",
		}, []string{"method", "route", "status"})

		r.requestLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "peep",
			Subsystem: "api",
			Name:      "http_request_duration_seconds",
			Help:      "Latency distribution of HTTP handlers",
			Buckets:   histogramBuckets,
		}, []string{"method", "route", "status"})

		r.rateLimitHits = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "peep",
			Subsystem: "api",
			Name:      "rate_limit_hits_total",
			Help:      "Number of rate-limited responses",
		}, []string{"route", "key"})

		collectors := []prometheus.Collector{r.requestTotal, r.requestLatency, r.rateLimitHits}
		for _, collector := range collectors {
			if err := prometheus.Register(collector); err != nil {
				if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
					switch v := are.ExistingCollector.(type) {
					case *prometheus.CounterVec:
						if collector == r.requestTotal {
							r.requestTotal = v
						} else if collector == r.rateLimitHits {
							r.rateLimitHits = v
						}
					case *prometheus.HistogramVec:
						r.requestLatency = v
					}
				}
			}
		}
		r.metricsInitialized = true
	})
}

func (r *Router) recordRequestMetrics(method, route string, status int, duration time.Duration) {
	if !r.metricsInitialized {
		return
	}
	labels := prometheus.Labels{
		"method": method,
		"route":  route,
		"status": strconv.Itoa(status),
	}
	r.requestTotal.With(labels).Inc()
	r.requestLatency.With(labels).Observe(duration.Seconds())
}

func (r *Router) recordRateLimitHit(route, key string) {
	if !r.metricsInitialized {
		return
	}
	r.rateLimitHits.With(prometheus.Labels{"route": route, "key": key}).Inc()
}
