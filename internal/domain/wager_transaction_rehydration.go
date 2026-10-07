package domain

import (
	"fmt"
	"time"
)

type RehydrateWagerTransactionParams struct {
	ID string

	ProviderID            string
	ExternalTransactionID string
	IdempotencyKey        string
	PayloadHash           string

	WalletID string
	PlayerID string
	RoundID  string
	GameID   string

	Kind  WagerKind
	Money Money

	ReferenceExternalTransactionID string
	ReferenceTransactionID         string

	Status      WagerStatus
	FailureCode FailureCode

	ResultBalance *Money

	CreatedAt time.Time
	UpdatedAt time.Time
}

func RehydrateWagerTransaction(
	p RehydrateWagerTransactionParams,
) (WagerTransaction, error) {
	if err := validateRehydratedWagerTransaction(p); err != nil {
		return WagerTransaction{}, err
	}

	return WagerTransaction{
		id:                             p.ID,
		providerID:                     p.ProviderID,
		externalTransactionID:          p.ExternalTransactionID,
		idempotencyKey:                 p.IdempotencyKey,
		payloadHash:                    p.PayloadHash,
		walletID:                       p.WalletID,
		playerID:                       p.PlayerID,
		roundID:                        p.RoundID,
		gameID:                         p.GameID,
		kind:                           p.Kind,
		money:                          p.Money,
		referenceExternalTransactionID: p.ReferenceExternalTransactionID,
		referenceTransactionID:         p.ReferenceTransactionID,
		status:                         p.Status,
		failureCode:                    p.FailureCode,
		resultBalance:                  p.ResultBalance,
		createdAt:                      p.CreatedAt,
		updatedAt:                      p.UpdatedAt,
	}, nil
}

func validateRehydratedWagerTransaction(
	p RehydrateWagerTransactionParams,
) error {
	if p.ID == "" {
		return ErrWagerTransactionIDRequired
	}
	if !isValidWagerKind(p.Kind) {
		return ErrInvalidWagerKind
	}
	if !isValidWagerStatus(p.Status) {
		return ErrInvalidWagerStatus
	}
	if p.CreatedAt.IsZero() ||
		p.UpdatedAt.IsZero() ||
		p.UpdatedAt.Before(p.CreatedAt) {
		return ErrInvalidWagerTimestamps
	}
	if p.Kind == WagerKindOpening {
		return validateRehydratedOpeningWagerTransaction(p)
	}

	if err := validateNewWagerTransactionParams(NewWagerTransactionParams{
		ID:                             p.ID,
		ProviderID:                     p.ProviderID,
		ExternalTransactionID:          p.ExternalTransactionID,
		IdempotencyKey:                 p.IdempotencyKey,
		PayloadHash:                    p.PayloadHash,
		WalletID:                       p.WalletID,
		PlayerID:                       p.PlayerID,
		RoundID:                        p.RoundID,
		GameID:                         p.GameID,
		Kind:                           p.Kind,
		Money:                          p.Money,
		ReferenceExternalTransactionID: p.ReferenceExternalTransactionID,
	}); err != nil {
		return err
	}

	if p.ReferenceTransactionID != "" &&
		p.ReferenceExternalTransactionID == "" {
		return fmt.Errorf(
			"%w: internal reference requires an external reference",
			ErrInvalidWagerState,
		)
	}

	return validateRehydratedExternalWagerStatus(p)
}

func validateRehydratedOpeningWagerTransaction(
	p RehydrateWagerTransactionParams,
) error {
	if p.WalletID == "" {
		return ErrWagerWalletIDRequired
	}
	if p.PlayerID == "" {
		return ErrWagerPlayerIDRequired
	}
	if err := validateOpeningWagerMoney(p.Money); err != nil {
		return err
	}
	if p.Status != WagerStatusProcessed {
		return fmt.Errorf(
			"%w: OPENING must be PROCESSED",
			ErrInvalidWagerState,
		)
	}
	if p.ProviderID != "" ||
		p.ExternalTransactionID != "" ||
		p.IdempotencyKey != "" ||
		p.PayloadHash != "" ||
		p.RoundID != "" ||
		p.GameID != "" ||
		p.ReferenceExternalTransactionID != "" ||
		p.ReferenceTransactionID != "" ||
		p.FailureCode != "" ||
		p.ResultBalance != nil {
		return fmt.Errorf(
			"%w: OPENING contains external-operation metadata",
			ErrInvalidWagerState,
		)
	}

	return nil
}

func validateRehydratedExternalWagerStatus(
	p RehydrateWagerTransactionParams,
) error {
	switch p.Status {
	case WagerStatusPending:
		if p.FailureCode != "" || p.ResultBalance != nil {
			return fmt.Errorf(
				"%w: PENDING cannot contain a result",
				ErrInvalidWagerState,
			)
		}

	case WagerStatusPendingReference:
		if p.ReferenceExternalTransactionID == "" {
			return ErrMissingReference
		}
		if p.FailureCode != "" || p.ResultBalance != nil {
			return fmt.Errorf(
				"%w: PENDING_REFERENCE cannot contain a result",
				ErrInvalidWagerState,
			)
		}

	case WagerStatusProcessed:
		if p.FailureCode != "" {
			return fmt.Errorf(
				"%w: PROCESSED cannot contain a failure code",
				ErrInvalidWagerState,
			)
		}
		if p.ReferenceExternalTransactionID != "" &&
			p.ReferenceTransactionID == "" {
			return fmt.Errorf(
				"%w: reference must be resolved before processing",
				ErrInvalidWagerState,
			)
		}
		if p.ResultBalance == nil {
			return fmt.Errorf(
				"%w: PROCESSED requires a result balance",
				ErrInvalidWagerState,
			)
		}
		if err := validateWagerResultBalance(p.Money, *p.ResultBalance); err != nil {
			return err
		}

	case WagerStatusRejected, WagerStatusFailed:
		if p.FailureCode == "" {
			return ErrFailureCodeRequired
		}
		if p.ResultBalance != nil {
			return fmt.Errorf(
				"%w: unsuccessful transaction cannot contain a result balance",
				ErrInvalidWagerState,
			)
		}
	}

	return nil
}
