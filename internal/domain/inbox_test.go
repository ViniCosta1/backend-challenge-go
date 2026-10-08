package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewInboxMessage(t *testing.T) {
	message, err := NewInboxMessage(
		"wager-consumer",
		"message-1",
		"payload-hash",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if message.IsCompleted() {
		t.Error("new inbox message must not be completed")
	}
	if message.receivedAt.IsZero() || message.receivedAt.Location() != time.UTC {
		t.Error("expected a non-zero UTC receivedAt")
	}
}

func TestNewInboxMessageRejectsMissingFields(t *testing.T) {
	tests := []struct {
		name         string
		consumerName string
		messageID    string
		payloadHash  string
		wantErr      error
	}{
		{
			name: "missing consumer", messageID: "message-1",
			payloadHash: "payload-hash", wantErr: ErrInboxConsumerNameRequired,
		},
		{
			name: "missing message id", consumerName: "wager-consumer",
			payloadHash: "payload-hash", wantErr: ErrInboxMessageIDRequired,
		},
		{
			name: "missing payload hash", consumerName: "wager-consumer",
			messageID: "message-1", wantErr: ErrInboxPayloadHashRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewInboxMessage(
				tt.consumerName,
				tt.messageID,
				tt.payloadHash,
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestInboxMessageMarkCompleted(t *testing.T) {
	message, err := NewInboxMessage(
		"wager-consumer",
		"message-1",
		"payload-hash",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := message.MarkCompleted(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	completedAt, ok := message.CompletedAt()
	if !ok || completedAt.IsZero() || completedAt.Location() != time.UTC {
		t.Fatal("expected a non-zero UTC completedAt")
	}

	if err := message.MarkCompleted(); err != nil {
		t.Fatalf("idempotent completion returned an error: %v", err)
	}
	completedAgain, _ := message.CompletedAt()
	if !completedAgain.Equal(completedAt) {
		t.Error("idempotent completion must preserve the original timestamp")
	}
}

func TestRehydrateInboxMessage(t *testing.T) {
	receivedAt := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	completedAt := receivedAt.Add(time.Minute)

	message, err := RehydrateInboxMessage(RehydrateInboxMessageParams{
		ConsumerName: "wager-consumer",
		MessageID:    "message-1",
		PayloadHash:  "payload-hash",
		ReceivedAt:   receivedAt,
		CompletedAt:  &completedAt,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	completedAt = completedAt.Add(time.Hour)
	storedCompletedAt, ok := message.CompletedAt()
	if message.consumerName != "wager-consumer" ||
		message.messageID != "message-1" ||
		message.payloadHash != "payload-hash" ||
		!message.receivedAt.Equal(receivedAt) ||
		!ok ||
		!storedCompletedAt.Equal(receivedAt.Add(time.Minute)) {
		t.Error("rehydration did not preserve the persisted inbox state")
	}
}
