package service

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/clickstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds the Prometheus metrics for requests, caches, the database pool and clicks.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	m := &Metrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests by route, method and status.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "Time to serve HTTP requests, by route and method.",
			Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		}, []string{"route", "method"}),
	}
	registry.MustRegister(m.requests, m.duration)
	return m
}

func (m *Metrics) ObserveRequest(route, method string, status int, took time.Duration) {
	m.requests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(route, method).Observe(took.Seconds())
}

// Exporter serves the metrics in Prometheus's text format.
func (m *Metrics) Exporter() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Database adds the connection pool's statistics, including waits for a free connection.
func (m *Metrics) Database(conn *sql.DB) {
	m.registry.MustRegister(collectors.NewDBStatsCollector(conn, "api"))
}

func (m *Metrics) Counter(name, help string, value func() float64) {
	m.registry.MustRegister(prometheus.NewCounterFunc(prometheus.CounterOpts{Name: name, Help: help}, value))
}

func (m *Metrics) Gauge(name, help string, value func() float64) {
	m.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: name, Help: help}, value))
}

// Track exports the database pool, redirect cache and click pipeline counters. stream is nil
// when clicks don't go through Redis.
func (m *Metrics) Track(conn *sql.DB, redirects *RedirectService, recorder *analytics.Recorder, stream *clickstream.Queue) {
	m.Database(conn)

	cache := func(pick func(RedirectStats) uint64) func() float64 {
		return func() float64 { return float64(pick(redirects.Stats())) }
	}
	m.Counter("link_cache_hits_total", "Redirect lookups answered by the in-process cache.",
		cache(func(s RedirectStats) uint64 { return s.Local.Hits }))
	m.Counter("link_cache_misses_total", "Redirect lookups the in-process cache could not answer.",
		cache(func(s RedirectStats) uint64 { return s.Local.Misses }))
	m.Counter("link_cache_coalesced_total", "Misses that waited for another request's lookup of the same slug.",
		cache(func(s RedirectStats) uint64 { return s.Local.Coalesced }))
	m.Counter("link_cache_stale_total", "Expired links served because the database lookup failed.",
		cache(func(s RedirectStats) uint64 { return s.Local.Stale }))
	m.Counter("link_cache_shared_hits_total", "Misses answered by Redis.",
		cache(func(s RedirectStats) uint64 { return s.L2Hits }))
	m.Counter("link_cache_shared_misses_total", "Misses Redis could not answer.",
		cache(func(s RedirectStats) uint64 { return s.L2Misses }))
	m.Counter("link_cache_shared_errors_total", "Failed Redis reads and writes.",
		cache(func(s RedirectStats) uint64 { return s.L2Errors }))
	m.Counter("link_cache_db_loads_total", "Redirect lookups that reached PostgreSQL.",
		cache(func(s RedirectStats) uint64 { return s.DBLoads }))

	direct := func(pick func(analytics.RecorderStats) uint64) func() float64 {
		return func() float64 { return float64(pick(recorder.Stats())) }
	}
	m.Gauge("click_queue_depth", "Clicks waiting in memory, to be appended to the stream or written to PostgreSQL.",
		func() float64 {
			depth := recorder.QueueDepth()
			if stream != nil {
				depth += stream.QueueDepth()
			}
			return float64(depth)
		})
	m.Counter("clicks_written_total", "Clicks written to PostgreSQL straight from memory.",
		direct(func(s analytics.RecorderStats) uint64 { return s.Written }))
	m.Counter("clicks_duplicate_total", "Clicks written straight from memory that were already stored, from retried batches.",
		direct(func(s analytics.RecorderStats) uint64 { return s.Duplicates }))
	m.Counter("clicks_dropped_total", "Clicks dropped because the in-memory buffer was full.",
		direct(func(s analytics.RecorderStats) uint64 { return s.Dropped }))
	m.Counter("click_write_failures_total", "Failed click batch writes from memory; each one is retried.",
		direct(func(s analytics.RecorderStats) uint64 { return s.FailedWrites }))
	m.Counter("click_batches_total", "Click batches written straight from memory.",
		direct(func(s analytics.RecorderStats) uint64 { return s.Batches }))
	if stream == nil {
		return
	}

	streamStat := func(pick func(clickstream.Stats) uint64) func() float64 {
		return func() float64 { return float64(pick(stream.Stats())) }
	}
	m.Counter("click_stream_appended_total", "Clicks appended to the Redis stream.",
		streamStat(func(s clickstream.Stats) uint64 { return s.Appended }))
	m.Counter("click_stream_fallback_total", "Clicks sent straight to PostgreSQL because Redis failed or the stream was over budget.",
		streamStat(func(s clickstream.Stats) uint64 { return s.Fallback }))
	m.Counter("click_stream_dropped_total", "Clicks dropped because the stream's in-memory buffer was full.",
		streamStat(func(s clickstream.Stats) uint64 { return s.Dropped }))
	m.Counter("click_stream_written_total", "Clicks this instance wrote from the stream to PostgreSQL.",
		streamStat(func(s clickstream.Stats) uint64 { return s.Written }))
	m.Counter("click_stream_duplicate_total", "Stream clicks that were already stored: entries taken over after a crash, or appends that timed out but landed.",
		streamStat(func(s clickstream.Stats) uint64 { return s.Duplicates }))
	m.Counter("click_stream_write_failures_total", "Failed writes from the stream; each one is retried.",
		streamStat(func(s clickstream.Stats) uint64 { return s.FailedWrites }))
	m.Counter("click_stream_claimed_total", "Stream entries taken over from silent consumers.",
		streamStat(func(s clickstream.Stats) uint64 { return s.Claimed }))
	m.Counter("click_stream_dead_letters_total", "Stream entries moved to sh:clicks:dead because PostgreSQL rejected them.",
		streamStat(func(s clickstream.Stats) uint64 { return s.DeadLetters }))
	m.Gauge("click_stream_backlog_entries", "Stream entries not yet written to PostgreSQL, at the last check.",
		func() float64 { return float64(stream.Stats().BacklogEntries) })
	m.Gauge("click_stream_backlog_bytes", "Estimated stream memory in Redis, at the last check.",
		func() float64 { return float64(stream.Stats().BacklogBytes) })
}
