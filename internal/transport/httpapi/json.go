package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{code, message},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON request")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "request must contain one JSON object")
		return false
	}
	return true
}

type moneyDTO struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (m moneyDTO) money() (domain.Money, error) {
	// The adapter validates decimal syntax before invoking the existing domain
	// parser, which currently accepts signed decimal components via ParseInt.
	parts := strings.Split(m.Amount, ".")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 2 {
		return domain.Money{}, errors.New("money amount must have exactly two decimal places")
	}
	for _, part := range parts {
		for _, char := range part {
			if char < '0' || char > '9' {
				return domain.Money{}, errors.New("money amount must contain only decimal digits")
			}
		}
	}
	amount, err := domain.ParseAmount(m.Amount)
	if err != nil {
		return domain.Money{}, err
	}
	return domain.NewMoney(amount, m.Currency)
}

func validUUID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (a *API) respondError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, application.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, application.ErrWalletAlreadyExists):
		writeError(w, http.StatusConflict, "wallet_already_exists", "wallet already exists for this player and currency")
	case errors.Is(err, application.ErrIdempotencyConflict):
		if a.metrics != nil {
			a.metrics.RecordConflict("idempotency")
		}
		writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was reused with different content")
	case errors.Is(err, application.ErrExternalTransactionConflict):
		if a.metrics != nil {
			a.metrics.RecordConflict("external_transaction")
		}
		writeError(w, http.StatusConflict, "external_transaction_conflict", "external transaction already exists")
	case isInputError(err):
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid request fields")
	case errors.Is(err, application.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "dependency is temporarily unavailable")
	default:
		a.logger.ErrorContext(r.Context(), "HTTP operation failed", "error", err, "method", r.Method, "path", r.URL.Path)
		writeError(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
	}
}

func isInputError(err error) bool {
	for _, sentinel := range []error{
		application.ErrInvalidCursor, application.ErrInvalidLimit, application.ErrUnsupportedWagerKind,
		application.ErrWagerWalletRelationship, domain.ErrInvalidWagerKind, domain.ErrInvalidWagerAmount,
		domain.ErrInvalidWagerCurrency, domain.ErrInvalidWagerState, domain.ErrMissingReference,
		domain.ErrWagerProviderIDRequired, domain.ErrWagerExternalIDRequired, domain.ErrWagerIdempotencyKeyRequired,
		domain.ErrWagerWalletIDRequired, domain.ErrWagerPlayerIDRequired, domain.ErrWagerRoundIDRequired, domain.ErrWagerGameIDRequired,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}
