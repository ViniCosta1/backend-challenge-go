package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestNewIntegrationEvents(t *testing.T) {
	tests := []struct {
		name          string
		wantType      IntegrationEventType
		wantAggregate string
		create        func() (IntegrationEventEnvelope, error)
	}{
		{
			name:          "wager processed",
			wantType:      IntegrationEventTypeWagerTransactionProcessed,
			wantAggregate: "transaction-1",
			create: func() (IntegrationEventEnvelope, error) {
				event, err := NewWagerTransactionProcessed(
					NewWagerTransactionProcessedParams{
						EventID:               "event-1",
						CorrelationID:         "correlation-1",
						TransactionID:         "transaction-1",
						WalletID:              "wallet-1",
						ProviderID:            "provider-1",
						ExternalTransactionID: "external-1",
						Kind:                  WagerKindBet,
						ResultBalance:         ledgerMoney(7500),
					},
				)
				return event.Envelope(), err
			},
		},
		{
			name:          "wager rejected",
			wantType:      IntegrationEventTypeWagerTransactionRejected,
			wantAggregate: "transaction-1",
			create: func() (IntegrationEventEnvelope, error) {
				event, err := NewWagerTransactionRejected(
					NewWagerTransactionRejectedParams{
						EventID:               "event-1",
						CorrelationID:         "correlation-1",
						TransactionID:         "transaction-1",
						WalletID:              "wallet-1",
						ProviderID:            "provider-1",
						ExternalTransactionID: "external-1",
						Kind:                  WagerKindBet,
						FailureCode:           "INSUFFICIENT_BALANCE",
					},
				)
				return event.Envelope(), err
			},
		},
		{
			name:          "wager pending reference",
			wantType:      IntegrationEventTypeWagerTransactionPendingReference,
			wantAggregate: "transaction-1",
			create: func() (IntegrationEventEnvelope, error) {
				event, err := NewWagerTransactionPendingReference(
					NewWagerTransactionPendingReferenceParams{
						EventID:                        "event-1",
						CorrelationID:                  "correlation-1",
						TransactionID:                  "transaction-1",
						WalletID:                       "wallet-1",
						ProviderID:                     "provider-1",
						ExternalTransactionID:          "external-1",
						ReferenceExternalTransactionID: "external-bet-1",
						Kind:                           WagerKindRefund,
					},
				)
				return event.Envelope(), err
			},
		},
		{
			name:          "wallet balance changed",
			wantType:      IntegrationEventTypeWalletBalanceChanged,
			wantAggregate: "wallet-1",
			create: func() (IntegrationEventEnvelope, error) {
				event, err := NewWalletBalanceChanged(
					NewWalletBalanceChangedParams{
						EventID:       "event-1",
						CorrelationID: "correlation-1",
						WalletID:      "wallet-1",
						TransactionID: "transaction-1",
						Direction:     LedgerDirectionDebit,
						Money:         ledgerMoney(2500),
						BalanceBefore: ledgerMoney(10000),
						BalanceAfter:  ledgerMoney(7500),
						WalletVersion: 2,
					},
				)
				return event.Envelope(), err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope, err := tt.create()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if envelope.EventType() != tt.wantType {
				t.Errorf("expected event type %s, got %s", tt.wantType, envelope.EventType())
			}
			if envelope.Version() != integrationEventVersion {
				t.Errorf("expected version %d, got %d", integrationEventVersion, envelope.Version())
			}
			if envelope.AggregateID() != tt.wantAggregate {
				t.Errorf("expected aggregate %s, got %s", tt.wantAggregate, envelope.AggregateID())
			}
			if envelope.OccurredAt().IsZero() ||
				envelope.OccurredAt().Location() != time.UTC {
				t.Error("expected a non-zero UTC occurredAt")
			}
		})
	}
}

func TestWalletBalanceChangedPreservesFinancialSnapshot(t *testing.T) {
	before := ledgerMoney(10000)
	movement := ledgerMoney(2500)
	after := ledgerMoney(12500)

	event, err := NewWalletBalanceChanged(NewWalletBalanceChangedParams{
		EventID:       "event-1",
		CorrelationID: "correlation-1",
		CausationID:   "command-1",
		WalletID:      "wallet-1",
		TransactionID: "transaction-1",
		Direction:     LedgerDirectionCredit,
		Money:         movement,
		BalanceBefore: before,
		BalanceAfter:  after,
		WalletVersion: 2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if event.Money() != movement ||
		event.BalanceBefore() != before ||
		event.BalanceAfter() != after {
		t.Error("event did not preserve the financial snapshot")
	}

	if containsFloat(reflect.TypeOf(event)) {
		t.Error("integration event payload must not contain float values")
	}
}

func TestWagerTransactionProcessedAllowsOpeningMetadata(t *testing.T) {
	event, err := NewWagerTransactionProcessed(
		NewWagerTransactionProcessedParams{
			EventID:       "event-1",
			CorrelationID: "correlation-1",
			TransactionID: "transaction-1",
			WalletID:      "wallet-1",
			Kind:          WagerKindOpening,
			ResultBalance: ledgerMoney(10000),
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := event.ProviderID(); ok {
		t.Error("OPENING must not expose a provider id")
	}
	if _, ok := event.ExternalTransactionID(); ok {
		t.Error("OPENING must not expose an external transaction id")
	}
}

func TestIntegrationEventRejectsInvalidData(t *testing.T) {
	tests := []struct {
		name    string
		create  func() error
		wantErr error
	}{
		{
			name: "missing event id",
			create: func() error {
				_, err := NewWagerTransactionProcessed(
					NewWagerTransactionProcessedParams{
						CorrelationID:         "correlation-1",
						TransactionID:         "transaction-1",
						WalletID:              "wallet-1",
						ProviderID:            "provider-1",
						ExternalTransactionID: "external-1",
						Kind:                  WagerKindBet,
						ResultBalance:         ledgerMoney(7500),
					},
				)
				return err
			},
			wantErr: ErrIntegrationEventIDRequired,
		},
		{
			name: "external processed event without provider",
			create: func() error {
				_, err := NewWagerTransactionProcessed(
					NewWagerTransactionProcessedParams{
						EventID:               "event-1",
						CorrelationID:         "correlation-1",
						TransactionID:         "transaction-1",
						WalletID:              "wallet-1",
						ExternalTransactionID: "external-1",
						Kind:                  WagerKindBet,
						ResultBalance:         ledgerMoney(7500),
					},
				)
				return err
			},
			wantErr: ErrIntegrationProviderIDRequired,
		},
		{
			name: "inconsistent wallet snapshot",
			create: func() error {
				_, err := NewWalletBalanceChanged(
					NewWalletBalanceChangedParams{
						EventID:       "event-1",
						CorrelationID: "correlation-1",
						WalletID:      "wallet-1",
						TransactionID: "transaction-1",
						Direction:     LedgerDirectionDebit,
						Money:         ledgerMoney(2500),
						BalanceBefore: ledgerMoney(10000),
						BalanceAfter:  ledgerMoney(8000),
						WalletVersion: 2,
					},
				)
				return err
			},
			wantErr: ErrInvalidIntegrationEventPayload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.create(); !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func containsFloat(valueType reflect.Type) bool {
	switch valueType.Kind() {
	case reflect.Float32, reflect.Float64:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return containsFloat(valueType.Elem())
	case reflect.Struct:
		if valueType.PkgPath() == "time" {
			return false
		}
		for i := 0; i < valueType.NumField(); i++ {
			if containsFloat(valueType.Field(i).Type) {
				return true
			}
		}
	}

	return false
}
