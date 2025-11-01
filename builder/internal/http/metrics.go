package httpx

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var histogramBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10}

func (r *Router) initMetrics() {
	r.metricsOnce.Do(func() {
		r.requestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "peep",
			Subsystem: "builder",
			Name:      "http_requests_total",
			Help:      "Count of processed HTTP requests",
		}, []string{"method", "route", "status"})

		r.requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "peep",
			Subsystem: "builder",
			Name:      "http_request_duration_seconds",
			Help:      "Latency distribution of HTTP handlers",
			Buckets:   histogramBuckets,
		}, []string{"method", "route", "status"})

		r.deployResults = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "peep",
			Subsystem: "builder",
			Name:      "deploy_results_total",
			Help:      "Number of deploy handler outcomes",
		}, []string{"outcome"})

		collectors := []prometheus.Collector{r.requestTotal, r.requestDuration, r.deployResults}
		for _, collector := range collectors {
			if err := prometheus.Register(collector); err != nil {
				if already, ok := err.(prometheus.AlreadyRegisteredError); ok {
					switch existing := already.ExistingCollector.(type) {
					case *prometheus.CounterVec:
						if collector == r.requestTotal {
							r.requestTotal = existing
						} else {
							r.deployResults = existing
						}
					case *prometheus.HistogramVec:
						r.requestDuration = existing
					}
				}
			}
		}
		r.metricsInitialized = true
	})
}

func (r *Router) instrument(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if !r.metricsInitialized {
			next(w, req)
			return
		}
		recorder := &responseRecorder{ResponseWriter: w}
		start := time.Now()
		next(recorder, req)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		r.recordRequest(req.Method, route, status, time.Since(start))
	}
}

func (r *Router) recordRequest(method, route string, status int, duration time.Duration) {
	if !r.metricsInitialized {
		return
	}
	labels := prometheus.Labels{
		"method": method,
		"route":  route,
		"status": strconv.Itoa(status),
	}
	r.requestTotal.With(labels).Inc()
	r.requestDuration.With(labels).Observe(duration.Seconds())
}

func (r *Router) recordDeployResult(outcome string) {
	if !r.metricsInitialized {
		return
	}
	r.deployResults.With(prometheus.Labels{"outcome": outcome}).Inc()
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.status = code
	rr.ResponseWriter.WriteHeader(code)
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	if rr.status == 0 {
		rr.status = http.StatusOK
	}
	return rr.ResponseWriter.Write(b)
}
