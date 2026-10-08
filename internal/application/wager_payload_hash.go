package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

// wagerPayloadHashInput is the canonical representation of the business
// payload. Field order is fixed by this struct, strings are preserved exactly,
// Money uses its canonical JSON representation, and an absent reference is an
// empty string. Idempotency keys and transport metadata are intentionally not
// included.
type wagerPayloadHashInput struct {
	ProviderID                     string           `json:"providerId"`
	ExternalTransactionID          string           `json:"externalTransactionId"`
	PlayerID                       string           `json:"playerId"`
	WalletID                       string           `json:"walletId"`
	RoundID                        string           `json:"roundId"`
	GameID                         string           `json:"gameId"`
	Kind                           domain.WagerKind `json:"kind"`
	Money                          domain.Money     `json:"money"`
	ReferenceExternalTransactionID string           `json:"referenceExternalTransactionId"`
}

func calculateWagerPayloadHash(input ProcessWagerTransactionInput) (string, error) {
	payload, err := json.Marshal(wagerPayloadHashInput{
		ProviderID:                     input.ProviderID,
		ExternalTransactionID:          input.ExternalTransactionID,
		PlayerID:                       input.PlayerID,
		WalletID:                       input.WalletID,
		RoundID:                        input.RoundID,
		GameID:                         input.GameID,
		Kind:                           input.Kind,
		Money:                          input.Money,
		ReferenceExternalTransactionID: input.ReferenceExternalTransactionID,
	})
	if err != nil {
		return "", fmt.Errorf("serialize wager payload for hashing: %w", err)
	}

	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
