package observability

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry                  *prometheus.Registry
	httpRequests              *prometheus.CounterVec
	httpDuration              *prometheus.HistogramVec
	wagerResults              *prometheus.CounterVec
	wagerDuration             *prometheus.HistogramVec
	idempotentReplays         prometheus.Counter
	duplicates                *prometheus.CounterVec
	retries                   *prometheus.CounterVec
	dlqCandidates             prometheus.Counter
	conflicts                 *prometheus.CounterVec
	reconciliationDivergences prometheus.Counter
	sqsMessages               *prometheus.CounterVec
	outboxDeliveries          *prometheus.CounterVec
}

func NewMetrics(pool *pgxpool.Pool) *Metrics {
	registry := prometheus.NewRegistry()
	metrics := &Metrics{
		registry: registry,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_http_requests_total", Help: "HTTP requests grouped by bounded route and status.",
		}, []string{"method", "route", "status"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wager_http_request_duration_seconds", Help: "HTTP adapter latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		wagerResults: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_results_total", Help: "Durable wager results by status and transport.",
		}, []string{"status", "transport"}),
		wagerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "wager_processing_duration_seconds", Help: "Wager processing latency by transport.",
			Buckets: prometheus.DefBuckets,
		}, []string{"transport"}),
		idempotentReplays: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_idempotent_replays_total", Help: "Financial replays that produced no second effect.",
		}),
		duplicates: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_duplicates_total", Help: "Duplicate deliveries classified without a second effect.",
		}, []string{"kind"}),
		retries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_retries_total", Help: "Durable or broker retries by component.",
		}, []string{"component"}),
		dlqCandidates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_sqs_dlq_candidates_total", Help: "Messages observed at the configured final receive before broker redrive.",
		}),
		conflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_conflicts_total", Help: "Identified idempotency, identity, or reversal conflicts.",
		}, []string{"kind"}),
		reconciliationDivergences: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_reconciliation_divergences_total", Help: "Wallet reconciliation checks that found a mismatch.",
		}),
		sqsMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_sqs_messages_total", Help: "Input messages by terminal adapter outcome.",
		}, []string{"result"}),
		outboxDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_outbox_deliveries_total", Help: "Outbox delivery attempts by outcome.",
		}, []string{"result"}),
	}
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		metrics.httpRequests, metrics.httpDuration, metrics.wagerResults, metrics.wagerDuration,
		metrics.idempotentReplays, metrics.duplicates, metrics.retries, metrics.dlqCandidates, metrics.conflicts,
		metrics.reconciliationDivergences, metrics.sqsMessages, metrics.outboxDeliveries)
	if pool != nil {
		registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "wager_outbox_oldest_pending_age_seconds",
			Help: "Age of the oldest unpublished Outbox event; zero when the Outbox is empty.",
		}, func() float64 {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var seconds float64
			err := pool.QueryRow(ctx, `SELECT COALESCE(
				GREATEST(EXTRACT(EPOCH FROM (CURRENT_TIMESTAMP - MIN(occurred_at))), 0), 0
			)::double precision FROM outbox_events WHERE published_at IS NULL`).Scan(&seconds)
			if err != nil {
				return math.NaN()
			}
			return seconds
		}))
	}
	return metrics
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{EnableOpenMetrics: true})
}

func (m *Metrics) ObserveHTTPRequest(method, route string, status int, duration time.Duration) {
	m.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(duration.Seconds())
}

func (m *Metrics) ObserveWager(status, transport string, replay bool, duration time.Duration) {
	m.wagerResults.WithLabelValues(status, transport).Inc()
	m.wagerDuration.WithLabelValues(transport).Observe(duration.Seconds())
	if replay {
		m.idempotentReplays.Inc()
	}
}

func (m *Metrics) RecordRetry(component string, count int) {
	if count > 0 {
		m.retries.WithLabelValues(component).Add(float64(count))
	}
}

func (m *Metrics) RecordDLQCandidate() { m.dlqCandidates.Inc() }

func (m *Metrics) RecordDuplicate(kind string) { m.duplicates.WithLabelValues(kind).Inc() }

func (m *Metrics) RecordConflict(kind string) { m.conflicts.WithLabelValues(kind).Inc() }

func (m *Metrics) RecordReconciliationDivergence() { m.reconciliationDivergences.Inc() }

func (m *Metrics) ObserveSQSMessage(result string) { m.sqsMessages.WithLabelValues(result).Inc() }

func (m *Metrics) ObserveOutbox(published, failed int) {
	if published > 0 {
		m.outboxDeliveries.WithLabelValues("published").Add(float64(published))
	}
	if failed > 0 {
		m.outboxDeliveries.WithLabelValues("failed").Add(float64(failed))
		m.RecordRetry("outbox", failed)
	}
}
