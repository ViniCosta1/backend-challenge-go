package application

import (
	"encoding/json"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type integrationEventPayload[T any] struct {
	EventID       string                      `json:"eventId"`
	EventType     domain.IntegrationEventType `json:"eventType"`
	AggregateID   string                      `json:"aggregateId"`
	CorrelationID string                      `json:"correlationId"`
	CausationID   string                      `json:"causationId,omitempty"`
	OccurredAt    time.Time                   `json:"occurredAt"`
	Version       int32                       `json:"version"`
	Data          T                           `json:"data"`
}

type wagerTransactionProcessedData struct {
	TransactionID         string           `json:"transactionId"`
	WalletID              string           `json:"walletId"`
	ProviderID            string           `json:"providerId,omitempty"`
	ExternalTransactionID string           `json:"externalTransactionId,omitempty"`
	Kind                  domain.WagerKind `json:"kind"`
	ResultBalance         domain.Money     `json:"resultBalance"`
}

func marshalWagerTransactionProcessed(
	event domain.WagerTransactionProcessed,
) ([]byte, error) {
	providerID, _ := event.ProviderID()
	externalTransactionID, _ := event.ExternalTransactionID()

	return marshalIntegrationEvent(
		event.Envelope(),
		wagerTransactionProcessedData{
			TransactionID:         event.TransactionID(),
			WalletID:              event.WalletID(),
			ProviderID:            providerID,
			ExternalTransactionID: externalTransactionID,
			Kind:                  event.Kind(),
			ResultBalance:         event.ResultBalance(),
		},
	)
}

type wagerTransactionRejectedData struct {
	TransactionID         string             `json:"transactionId"`
	WalletID              string             `json:"walletId"`
	ProviderID            string             `json:"providerId"`
	ExternalTransactionID string             `json:"externalTransactionId"`
	Kind                  domain.WagerKind   `json:"kind"`
	FailureCode           domain.FailureCode `json:"failureCode"`
}

func marshalWagerTransactionRejected(
	event domain.WagerTransactionRejected,
) ([]byte, error) {
	return marshalIntegrationEvent(
		event.Envelope(),
		wagerTransactionRejectedData{
			TransactionID:         event.TransactionID(),
			WalletID:              event.WalletID(),
			ProviderID:            event.ProviderID(),
			ExternalTransactionID: event.ExternalTransactionID(),
			Kind:                  event.Kind(),
			FailureCode:           event.FailureCode(),
		},
	)
}

type wagerTransactionPendingReferenceData struct {
	TransactionID                  string           `json:"transactionId"`
	WalletID                       string           `json:"walletId"`
	ProviderID                     string           `json:"providerId"`
	ExternalTransactionID          string           `json:"externalTransactionId"`
	ReferenceExternalTransactionID string           `json:"referenceExternalTransactionId"`
	Kind                           domain.WagerKind `json:"kind"`
}

func marshalWagerTransactionPendingReference(event domain.WagerTransactionPendingReference) ([]byte, error) {
	return marshalIntegrationEvent(event.Envelope(), wagerTransactionPendingReferenceData{
		TransactionID:                  event.TransactionID(),
		WalletID:                       event.WalletID(),
		ProviderID:                     event.ProviderID(),
		ExternalTransactionID:          event.ExternalTransactionID(),
		ReferenceExternalTransactionID: event.ReferenceExternalTransactionID(),
		Kind:                           event.Kind(),
	})
}

type walletBalanceChangedData struct {
	WalletID      string                 `json:"walletId"`
	TransactionID string                 `json:"transactionId"`
	Direction     domain.LedgerDirection `json:"direction"`
	Money         domain.Money           `json:"money"`
	BalanceBefore domain.Money           `json:"balanceBefore"`
	BalanceAfter  domain.Money           `json:"balanceAfter"`
	WalletVersion int64                  `json:"walletVersion"`
}

func marshalWalletBalanceChanged(
	event domain.WalletBalanceChanged,
) ([]byte, error) {
	return marshalIntegrationEvent(
		event.Envelope(),
		walletBalanceChangedData{
			WalletID:      event.WalletID(),
			TransactionID: event.TransactionID(),
			Direction:     event.Direction(),
			Money:         event.Money(),
			BalanceBefore: event.BalanceBefore(),
			BalanceAfter:  event.BalanceAfter(),
			WalletVersion: event.WalletVersion(),
		},
	)
}

func marshalIntegrationEvent[T any](
	envelope domain.IntegrationEventEnvelope,
	data T,
) ([]byte, error) {
	causationID, _ := envelope.CausationID()

	return json.Marshal(integrationEventPayload[T]{
		EventID:       envelope.EventID(),
		EventType:     envelope.EventType(),
		AggregateID:   envelope.AggregateID(),
		CorrelationID: envelope.CorrelationID(),
		CausationID:   causationID,
		OccurredAt:    envelope.OccurredAt(),
		Version:       envelope.Version(),
		Data:          data,
	})
}
