package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const correlationIDHeader = "X-Correlation-ID"

type requestContextKey struct{}

type requestFields struct {
	CorrelationID string
	MessageID     string
	TransactionID string
	WalletID      string
	ProviderID    string
}

func (a *API) observeRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		correlationID := canonicalCorrelationID(r.Header.Values(correlationIDHeader))
		fields := &requestFields{CorrelationID: correlationID}
		ctx := context.WithValue(r.Context(), requestContextKey{}, fields)
		r = r.WithContext(ctx)
		w.Header().Set(correlationIDHeader, correlationID)
		status := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(status, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		duration := time.Since(started)
		if a.metrics != nil {
			a.metrics.ObserveHTTPRequest(r.Method, route, status.status, duration)
		}
		attributes := []any{"correlation_id", fields.CorrelationID, "method", r.Method, "route", route,
			"status", status.status, "duration_ms", duration.Milliseconds()}
		attributes = appendNonEmpty(attributes, "message_id", fields.MessageID)
		attributes = appendNonEmpty(attributes, "transaction_id", fields.TransactionID)
		attributes = appendNonEmpty(attributes, "wallet_id", fields.WalletID)
		attributes = appendNonEmpty(attributes, "provider_id", fields.ProviderID)
		a.logger.LogAttrs(ctx, slog.LevelInfo, "HTTP request completed", anyToAttrs(attributes)...)
	})
}

func canonicalCorrelationID(values []string) string {
	if len(values) == 1 {
		if parsed, err := uuid.Parse(values[0]); err == nil && parsed.String() == values[0] {
			return values[0]
		}
	}
	return uuid.NewString()
}

func setRequestFields(ctx context.Context, update requestFields) {
	fields, _ := ctx.Value(requestContextKey{}).(*requestFields)
	if fields == nil {
		return
	}
	if update.MessageID != "" {
		fields.MessageID = update.MessageID
	}
	if update.TransactionID != "" {
		fields.TransactionID = update.TransactionID
	}
	if update.WalletID != "" {
		fields.WalletID = update.WalletID
	}
	if update.ProviderID != "" {
		fields.ProviderID = update.ProviderID
	}
}

func appendNonEmpty(values []any, name, value string) []any {
	if value != "" {
		return append(values, name, value)
	}
	return values
}

func anyToAttrs(values []any) []slog.Attr {
	attributes := make([]slog.Attr, 0, len(values)/2)
	for index := 0; index+1 < len(values); index += 2 {
		attributes = append(attributes, slog.Any(values[index].(string), values[index+1]))
	}
	return attributes
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(payload []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(payload)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
