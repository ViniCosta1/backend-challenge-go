package application

import (
	"testing"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func TestWagerPayloadHashExcludesIdempotencyKeyAndIncludesBusinessFields(t *testing.T) {
	money, err := domain.NewMoney(2500, "BRL")
	if err != nil {
		t.Fatalf("create money: %v", err)
	}
	input := ProcessWagerTransactionInput{
		ProviderID:            "provider-1",
		ExternalTransactionID: "external-1",
		IdempotencyKey:        "key-1",
		PlayerID:              "player-1",
		WalletID:              "wallet-1",
		RoundID:               "round-1",
		GameID:                "game-1",
		Kind:                  domain.WagerKindBet,
		Money:                 money,
	}

	first, err := calculateWagerPayloadHash(input)
	if err != nil {
		t.Fatalf("calculate hash: %v", err)
	}
	input.IdempotencyKey = "another-key"
	second, err := calculateWagerPayloadHash(input)
	if err != nil {
		t.Fatalf("calculate hash with another key: %v", err)
	}
	if first != second {
		t.Fatal("idempotency key must not affect the business payload hash")
	}

	input.RoundID = "another-round"
	third, err := calculateWagerPayloadHash(input)
	if err != nil {
		t.Fatalf("calculate changed hash: %v", err)
	}
	if third == first {
		t.Fatal("changing a business field must change the payload hash")
	}
	if len(first) != 64 {
		t.Fatalf("expected SHA-256 hex with 64 characters, got %q", first)
	}
}
