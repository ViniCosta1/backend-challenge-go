package domain

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func newOutboxEnvelopeForTest(t *testing.T) IntegrationEventEnvelope {
	t.Helper()

	event, err := NewWalletBalanceChanged(NewWalletBalanceChangedParams{
		EventID:       "event-1",
		CorrelationID: "correlation-1",
		CausationID:   "command-1",
		WalletID:      "wallet-1",
		TransactionID: "transaction-1",
		Direction:     LedgerDirectionCredit,
		Money:         ledgerMoney(2500),
		BalanceBefore: ledgerMoney(10000),
		BalanceAfter:  ledgerMoney(12500),
		WalletVersion: 2,
	})
	if err != nil {
		t.Fatalf("unexpected event error: %v", err)
	}

	return event.Envelope()
}

func TestNewOutboxEvent(t *testing.T) {
	envelope := newOutboxEnvelopeForTest(t)
	payload := []byte(`{"walletId":"wallet-1"}`)

	outbox, err := NewOutboxEvent(envelope, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload[0] = 'x'
	if outbox.Attempts() != 0 {
		t.Errorf("expected zero attempts, got %d", outbox.Attempts())
	}
	if outbox.IsPublished() {
		t.Error("new outbox event must not be published")
	}
	if outbox.EventID() != envelope.EventID() {
		t.Error("outbox must preserve the integration event id")
	}
	if !outbox.NextAttemptAt().Equal(envelope.OccurredAt()) {
		t.Error("new outbox event must be immediately eligible for publication")
	}
	if !bytes.Equal(outbox.Payload(), []byte(`{"walletId":"wallet-1"}`)) {
		t.Error("outbox payload must be an immutable snapshot")
	}

	returnedPayload := outbox.Payload()
	returnedPayload[0] = 'x'
	if bytes.Equal(returnedPayload, outbox.Payload()) {
		t.Error("payload getter must return a defensive copy")
	}
}

func TestOutboxEventAttemptAndRetry(t *testing.T) {
	outbox, err := NewOutboxEvent(
		newOutboxEnvelopeForTest(t),
		[]byte(`{"walletId":"wallet-1"}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	eventID := outbox.EventID()
	if err := outbox.RegisterAttempt(); err != nil {
		t.Fatalf("unexpected attempt error: %v", err)
	}

	nextAttemptAt := outbox.OccurredAt().Add(time.Minute)
	if err := outbox.ScheduleRetry(nextAttemptAt); err != nil {
		t.Fatalf("unexpected retry error: %v", err)
	}

	if outbox.Attempts() != 1 {
		t.Errorf("expected one attempt, got %d", outbox.Attempts())
	}
	if !outbox.NextAttemptAt().Equal(nextAttemptAt) {
		t.Error("outbox did not preserve the scheduled retry")
	}
	if outbox.EventID() != eventID {
		t.Error("event id must remain stable after an attempt")
	}
}

func TestOutboxEventMarkPublished(t *testing.T) {
	outbox, err := NewOutboxEvent(
		newOutboxEnvelopeForTest(t),
		[]byte(`{"walletId":"wallet-1"}`),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := outbox.MarkPublished(); err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}
	publishedAt, ok := outbox.PublishedAt()
	if !ok || publishedAt.IsZero() || publishedAt.Location() != time.UTC {
		t.Fatal("expected a non-zero UTC publishedAt")
	}

	if err := outbox.MarkPublished(); err != nil {
		t.Fatalf("idempotent publication returned an error: %v", err)
	}
	publishedAgain, _ := outbox.PublishedAt()
	if !publishedAgain.Equal(publishedAt) {
		t.Error("idempotent publication must preserve the original timestamp")
	}
	if err := outbox.RegisterAttempt(); !errors.Is(err, ErrOutboxAlreadyPublished) {
		t.Fatalf("expected ErrOutboxAlreadyPublished, got %v", err)
	}
}

func TestRehydrateOutboxEvent(t *testing.T) {
	occurredAt := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	nextAttemptAt := occurredAt.Add(time.Minute)
	publishedAt := occurredAt.Add(2 * time.Minute)
	payload := []byte(`{"walletId":"wallet-1"}`)

	outbox, err := RehydrateOutboxEvent(RehydrateOutboxEventParams{
		EventID:       "event-1",
		AggregateID:   "wallet-1",
		EventType:     IntegrationEventTypeWalletBalanceChanged,
		Version:       integrationEventVersion,
		Payload:       payload,
		CorrelationID: "correlation-1",
		CausationID:   "command-1",
		OccurredAt:    occurredAt,
		Attempts:      2,
		NextAttemptAt: nextAttemptAt,
		PublishedAt:   &publishedAt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	payload[0] = 'x'
	publishedAt = publishedAt.Add(time.Hour)
	storedPublishedAt, ok := outbox.PublishedAt()
	causationID, hasCausationID := outbox.CausationID()
	if outbox.EventID() != "event-1" ||
		outbox.AggregateID() != "wallet-1" ||
		outbox.EventType() != IntegrationEventTypeWalletBalanceChanged ||
		outbox.Version() != integrationEventVersion ||
		outbox.CorrelationID() != "correlation-1" ||
		!hasCausationID || causationID != "command-1" ||
		!outbox.OccurredAt().Equal(occurredAt) ||
		outbox.Attempts() != 2 ||
		!outbox.NextAttemptAt().Equal(nextAttemptAt) ||
		!bytes.Equal(outbox.Payload(), []byte(`{"walletId":"wallet-1"}`)) ||
		!ok ||
		!storedPublishedAt.Equal(occurredAt.Add(2*time.Minute)) {
		t.Error("rehydration did not preserve the persisted outbox state")
	}
}
