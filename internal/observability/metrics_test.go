package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsExposeBoundedOperationalSignals(t *testing.T) {
	metrics := NewMetrics(nil)
	metrics.ObserveHTTPRequest("POST", "/wagering/transactions", 200, time.Millisecond)
	metrics.ObserveWager("PROCESSED", "http", true, 2*time.Millisecond)
	metrics.RecordRetry("outbox", 1)
	metrics.RecordConflict("idempotency")
	metrics.RecordDuplicate("inbox")

	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/metrics", nil))
	body := recorder.Body.String()
	for _, name := range []string{"wager_http_requests_total", "wager_results_total", "wager_idempotent_replays_total", "wager_duplicates_total", "wager_retries_total", "wager_conflicts_total"} {
		if !strings.Contains(body, name) {
			t.Fatalf("metric %q is missing", name)
		}
	}
}
