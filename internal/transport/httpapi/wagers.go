package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func (a *API) processTransaction(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || keys[0] == "" || strings.TrimSpace(keys[0]) != keys[0] || strings.ContainsAny(keys[0], "\r\n\t") {
		writeError(w, http.StatusBadRequest, "invalid_idempotency_key", "one non-empty Idempotency-Key header is required")
		return
	}
	var body struct {
		ProviderID                     *string          `json:"providerId"`
		ExternalTransactionID          string           `json:"externalTransactionId"`
		PlayerID                       string           `json:"playerId"`
		WalletID                       string           `json:"walletId"`
		RoundID                        string           `json:"roundId"`
		GameID                         string           `json:"gameId"`
		Kind                           domain.WagerKind `json:"kind"`
		Money                          moneyDTO         `json:"money"`
		ReferenceExternalTransactionID string           `json:"referenceExternalTransactionId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	provider := authenticatedIdentity(r).ClientID
	if body.ProviderID != nil && *body.ProviderID != provider {
		writeError(w, http.StatusForbidden, "forbidden", "providerId must match the authenticated provider")
		return
	}
	money, err := body.Money.money()
	if err != nil || !validUUID(body.PlayerID) || !validUUID(body.WalletID) {
		writeError(w, http.StatusBadRequest, "invalid_input", "invalid UUID or Money")
		return
	}
	if strings.TrimSpace(body.ExternalTransactionID) == "" || strings.TrimSpace(body.RoundID) == "" || strings.TrimSpace(body.GameID) == "" {
		writeError(w, http.StatusBadRequest, "invalid_input", "externalTransactionId, roundId and gameId are required")
		return
	}
	setRequestFields(r.Context(), requestFields{WalletID: body.WalletID, ProviderID: provider})
	output, err := a.processWager.Execute(r.Context(), application.ProcessWagerTransactionInput{
		ProviderID: provider, ExternalTransactionID: body.ExternalTransactionID, IdempotencyKey: keys[0],
		PlayerID: body.PlayerID, WalletID: body.WalletID, RoundID: body.RoundID, GameID: body.GameID,
		Kind: body.Kind, Money: money, ReferenceExternalTransactionID: body.ReferenceExternalTransactionID,
	})
	if err != nil {
		a.respondError(w, r, err)
		return
	}
	setRequestFields(r.Context(), requestFields{TransactionID: output.TransactionID})
	if a.metrics != nil {
		a.metrics.ObserveWager(string(output.Status), "http", output.IdempotentReplay, time.Since(started))
	}
	status := http.StatusOK
	if output.Status == domain.WagerStatusPendingReference {
		status = http.StatusAccepted
	}
	if output.Status == domain.WagerStatusRejected {
		status = http.StatusUnprocessableEntity
	}
	var balance *domain.Money
	if output.HasResultBalance {
		balance = &output.Balance
	}
	writeJSON(w, status, struct {
		TransactionID    string             `json:"transactionId"`
		Status           domain.WagerStatus `json:"status"`
		Balance          *domain.Money      `json:"balance,omitempty"`
		FailureCode      domain.FailureCode `json:"failureCode,omitempty"`
		IdempotentReplay bool               `json:"idempotentReplay"`
	}{output.TransactionID, output.Status, balance, output.FailureCode, output.IdempotentReplay})
}

func (a *API) getTransaction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("transactionId")
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid_input", "transactionId must be a canonical UUID")
		return
	}
	setRequestFields(r.Context(), requestFields{TransactionID: id})
	wager, err := a.wagers.FindByID(r.Context(), authenticatedIdentity(r).ClientID, id)
	if err != nil {
		a.respondError(w, r, err)
		return
	}
	writeWager(w, wager)
}

func (a *API) getExternalTransaction(w http.ResponseWriter, r *http.Request) {
	provider := authenticatedIdentity(r).ClientID
	if r.PathValue("providerId") != provider {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	wager, err := a.wagers.FindByExternalID(r.Context(), provider, r.PathValue("externalTransactionId"))
	if err != nil {
		a.respondError(w, r, err)
		return
	}
	setRequestFields(r.Context(), requestFields{TransactionID: wager.ID(), WalletID: wager.WalletID(), ProviderID: provider})
	writeWager(w, wager)
}

func writeWager(w http.ResponseWriter, wager *domain.WagerTransaction) {
	result, ok := wager.ResultBalance()
	var balance *domain.Money
	if ok {
		balance = &result
	}
	next, hasNext := wager.ReferenceNextAttemptAt()
	var nextAttempt *time.Time
	if hasNext {
		nextAttempt = &next
	}
	writeJSON(w, http.StatusOK, struct {
		TransactionID                  string             `json:"transactionId"`
		ProviderID                     string             `json:"providerId"`
		ExternalTransactionID          string             `json:"externalTransactionId"`
		PlayerID                       string             `json:"playerId"`
		WalletID                       string             `json:"walletId"`
		RoundID                        string             `json:"roundId"`
		GameID                         string             `json:"gameId"`
		Kind                           domain.WagerKind   `json:"kind"`
		Money                          domain.Money       `json:"money"`
		Status                         domain.WagerStatus `json:"status"`
		FailureCode                    domain.FailureCode `json:"failureCode,omitempty"`
		ResultBalance                  *domain.Money      `json:"resultBalance,omitempty"`
		ReferenceExternalTransactionID string             `json:"referenceExternalTransactionId,omitempty"`
		ReferenceTransactionID         string             `json:"referenceTransactionId,omitempty"`
		ReferenceAttempts              int32              `json:"referenceAttempts"`
		ReferenceNextAttemptAt         *time.Time         `json:"referenceNextAttemptAt,omitempty"`
		CreatedAt                      time.Time          `json:"createdAt"`
		UpdatedAt                      time.Time          `json:"updatedAt"`
	}{wager.ID(), wager.ProviderID(), wager.ExternalTransactionID(), wager.PlayerID(), wager.WalletID(), wager.RoundID(), wager.GameID(), wager.Kind(), wager.Money(), wager.Status(), wager.FailureCode(), balance, wager.ReferenceExternalTransactionID(), wager.ReferenceTransactionID(), wager.ReferenceAttempts(), nextAttempt, wager.CreatedAt(), wager.UpdatedAt()})
}
