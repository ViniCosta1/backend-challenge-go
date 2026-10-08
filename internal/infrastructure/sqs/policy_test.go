package sqs

import (
	"errors"
	"testing"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
)

func TestBackoffBoundsAndInvalidEnvelope(t *testing.T) {
	for _, test := range []struct {
		attempts int32
		input    int32
		outbox   time.Duration
	}{
		{1, 1, time.Second}, {2, 2, 2 * time.Second}, {5, 16, 16 * time.Second},
		{10, 30, 5 * time.Minute}, {1<<31 - 1, 30, 5 * time.Minute},
	} {
		if InputBackoff(test.attempts) != test.input || application.OutboxBackoff(test.attempts) != test.outbox {
			t.Fatalf("invalid backoff for %d", test.attempts)
		}
	}
	for _, payload := range []string{`null`, `{}`, `{"type":"other"}`, `{"type":"WagerTransactionRequested","money":1.01}`} {
		if _, err := DecodeWagerMessage(payload); !errors.Is(err, application.ErrInvalidMessage) {
			t.Fatalf("invalid envelope accepted: %s", payload)
		}
	}
}
