package sqs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type wagerRequest struct {
	MessageID  string    `json:"messageId"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurredAt"`
	Data       struct {
		ProviderID            string           `json:"providerId"`
		ExternalTransactionID string           `json:"externalTransactionId"`
		IdempotencyKey        string           `json:"idempotencyKey"`
		PlayerID              string           `json:"playerId"`
		WalletID              string           `json:"walletId"`
		RoundID               string           `json:"roundId"`
		GameID                string           `json:"gameId"`
		Kind                  domain.WagerKind `json:"kind"`
		Money                 struct {
			Amount   string `json:"amount"`
			Currency string `json:"currency"`
		} `json:"money"`
		ReferenceExternalTransactionID string `json:"referenceExternalTransactionId"`
	} `json:"data"`
}

func DecodeWagerMessage(body string) (application.ProcessWagerMessageInput, error) {
	var result application.ProcessWagerMessageInput
	invalid := func(reason string) (application.ProcessWagerMessageInput, error) {
		return result, fmt.Errorf("%w: %s", application.ErrInvalidMessage, reason)
	}
	if len(body) == 0 || len(body) > 1<<20 {
		return invalid("invalid body length")
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	var envelope wagerRequest
	if err := decoder.Decode(&envelope); err != nil {
		return invalid("invalid envelope JSON")
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return invalid("expected one envelope")
	}
	if strings.TrimSpace(envelope.MessageID) == "" || envelope.Type != "WagerTransactionRequested" || envelope.OccurredAt.IsZero() {
		return invalid("invalid messageId, type or occurredAt")
	}
	data := envelope.Data
	for _, id := range []string{data.PlayerID, data.WalletID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return invalid("invalid playerId/walletId UUID")
		}
	}
	for _, value := range []string{data.ProviderID, data.ExternalTransactionID, data.IdempotencyKey, data.RoundID, data.GameID} {
		if strings.TrimSpace(value) == "" {
			return invalid("missing business identity fields")
		}
	}
	if strings.TrimSpace(data.IdempotencyKey) != data.IdempotencyKey || strings.ContainsAny(data.IdempotencyKey, "\r\n\t") {
		return invalid("invalid idempotencyKey")
	}
	parts := strings.Split(data.Money.Amount, ".")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 2 {
		return invalid("money must have two decimal places")
	}
	for _, part := range parts {
		for _, char := range part {
			if char < '0' || char > '9' {
				return invalid("invalid decimal Money")
			}
		}
	}
	amount, err := domain.ParseAmount(data.Money.Amount)
	if err != nil {
		return invalid("invalid Money amount")
	}
	money, err := domain.NewMoney(amount, data.Money.Currency)
	if err != nil {
		return invalid("invalid Money")
	}
	// The Inbox hash is SHA-256 of the exact received UTF-8 body, including
	// envelope and idempotency key. Financial hashing remains in the shared core.
	digest := sha256.Sum256([]byte(body))
	return application.ProcessWagerMessageInput{MessageID: envelope.MessageID, PayloadHash: hex.EncodeToString(digest[:]), Wager: application.ProcessWagerTransactionInput{
		ProviderID: data.ProviderID, ExternalTransactionID: data.ExternalTransactionID, IdempotencyKey: data.IdempotencyKey,
		PlayerID: data.PlayerID, WalletID: data.WalletID, RoundID: data.RoundID, GameID: data.GameID, Kind: data.Kind,
		Money: money, ReferenceExternalTransactionID: data.ReferenceExternalTransactionID}}, nil
}
