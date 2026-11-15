// Package metrics is the pit wall's Prometheus series (paddock#126): request
// latency and counts by route, method and status, the size of trace answers, and
// failures reaching logbook and NATS. Every label has a short, fixed set of
// values; no session id, lap number or path ever becomes one.
package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The values of the route label that are not a Connect procedure.
const (
	RouteStatic = "static"
	RouteOther  = "other"
)

// The values of the upstream and reason labels of pitwall_upstream_errors_total.
const (
	UpstreamLogbook = "logbook"
	UpstreamNATS    = "nats"

	ReasonTimeout     = "timeout"
	ReasonUnavailable = "unavailable"
	ReasonOther       = "other"
)

// Metrics holds the series and the registry they are served from. A nil
// *Metrics records nothing, so a component built without one still works.
type Metrics struct {
	registry *prometheus.Registry
	latency  *prometheus.HistogramVec
	requests *prometheus.CounterVec
	traceB   prometheus.Histogram
	traceP   prometheus.Histogram
	upstream *prometheus.CounterVec
}

// New registers the series, and Go's runtime and process metrics, on a registry
// of its own, so two pit walls in one process (a test) never collide.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "pitwall_http_request_seconds",
			Help:    "How long the pit wall took to answer an HTTP request, by route and method.",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5},
		}, []string{"route", "method"}),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pitwall_http_requests_total",
			Help: "HTTP requests answered, by route, method and status; status 304 is an ETag saving a body.",
		}, []string{"route", "method", "status"}),
		traceB: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pitwall_trace_bytes",
			Help:    "Size in bytes of a trace answer (protobuf form, before compression), 200s only.",
			Buckets: prometheus.ExponentialBuckets(1024, 2, 10),
		}),
		traceP: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "pitwall_trace_points",
			Help:    "Points in a trace answer, 200s only.",
			Buckets: []float64{100, 250, 500, 1000, 1500, 2500, 5000, 10000},
		}),
		upstream: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "pitwall_upstream_errors_total",
			Help: "Failures reaching logbook or NATS, by upstream and reason.",
		}, []string{"upstream", "reason"}),
	}
	m.registry.MustRegister(m.latency, m.requests, m.traceB, m.traceP, m.upstream,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	// Each upstream error series starts at 0, so a rate() of it has a baseline.
	for _, up := range []string{UpstreamLogbook, UpstreamNATS} {
		for _, reason := range []string{ReasonTimeout, ReasonUnavailable, ReasonOther} {
			m.upstream.WithLabelValues(up, reason)
		}
	}
	return m
}

// Handler serves the registry in Prometheus's text format.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// TraceAnswered records a trace answer of bytes bytes and points points.
func (m *Metrics) TraceAnswered(bytes, points int) {
	if m == nil {
		return
	}
	m.traceB.Observe(float64(bytes))
	m.traceP.Observe(float64(points))
}

// UpstreamError counts one failure reaching upstream, for reason.
func (m *Metrics) UpstreamError(upstream, reason string) {
	if m == nil {
		return
	}
	m.upstream.WithLabelValues(upstream, reason).Inc()
}

// statusWriter keeps the status a handler wrote.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach the writer underneath.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Middleware times each request and counts it under route(r), its method and the
// status it was answered with. route must return one of a fixed set of values.
func (m *Metrics) Middleware(route func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		if sw.status == 0 {
			sw.status = http.StatusOK
		}
		rt, verb := route(r), method(r)
		m.latency.WithLabelValues(rt, verb).Observe(time.Since(start).Seconds())
		m.requests.WithLabelValues(rt, verb, strconv.Itoa(sw.status)).Inc()
	})
}

// method is the request's method if it is one of the standard ones, so a client
// can't make a label of its own.
func method(r *http.Request) string {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return r.Method
	}
	return "other"
}
