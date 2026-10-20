package httpapi

import (
	"net/http"
	"strconv"

	"github.com/felixge/httpsnoop"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

type httpMetrics struct {
	requests        *prometheus.CounterVec
	duration        *prometheus.HistogramVec
	inFlight        prometheus.Gauge
	bookingsCreated prometheus.Counter
}

func newHTTPMetrics(registry *prometheus.Registry) *httpMetrics {
	metrics := &httpMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests handled by the API.",
		}, []string{"method", "route", "status_code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		inFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Current number of HTTP requests being handled by the API.",
		}),
		bookingsCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "travel_bookings_created_total",
			Help: "Total number of successfully created travel bookings.",
		}),
	}
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		metrics.requests,
		metrics.duration,
		metrics.inFlight,
		metrics.bookingsCreated,
	)
	return metrics
}

func (m *httpMetrics) instrument(method, route string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.inFlight.Inc()
		defer m.inFlight.Dec()

		observed := httpsnoop.CaptureMetrics(next, w, r)
		status := observed.Code
		if status == 0 {
			status = http.StatusOK
		}
		m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
		m.duration.WithLabelValues(method, route).Observe(observed.Duration.Seconds())
	})
}
