package domain

import (
	"errors"
	"fmt"
	"time"
)

var ErrMissingReference = errors.New(
	"reference external transaction id is required",
)

var (
	ErrWagerTransactionIDRequired = errors.New("transaction id is required")
	ErrWagerProviderIDRequired    = errors.New("provider id is required")
	ErrWagerExternalIDRequired    = errors.New(
		"external transaction id is required",
	)
	ErrWagerIdempotencyKeyRequired    = errors.New("idempotency key is required")
	ErrWagerPayloadHashRequired       = errors.New("payload hash is required")
	ErrWagerWalletIDRequired          = errors.New("wallet id is required")
	ErrWagerPlayerIDRequired          = errors.New("player id is required")
	ErrWagerRoundIDRequired           = errors.New("round id is required")
	ErrWagerGameIDRequired            = errors.New("game id is required")
	ErrReferenceTransactionIDRequired = errors.New(
		"reference transaction id is required",
	)
	ErrInvalidWagerState      = errors.New("invalid wager transaction state")
	ErrInvalidWagerTimestamps = errors.New("invalid wager transaction timestamps")
)

type WagerTransaction struct {
	id string

	// External-operation metadata.
	providerID            string
	externalTransactionID string
	idempotencyKey        string
	payloadHash           string

	walletID string
	playerID string
	roundID  string
	gameID   string

	kind  WagerKind
	money Money

	// Reference supplied by the provider.
	referenceExternalTransactionID string

	// Internal transaction resolved from referenceExternalTransactionID.
	referenceTransactionID string

	status      WagerStatus
	failureCode FailureCode

	// Persisted so an idempotent replay returns the balance observed
	// when the original operation was processed.
	resultBalance *Money

	createdAt time.Time
	updatedAt time.Time
}

type NewWagerTransactionParams struct {
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
}

func NewWagerTransaction(
	p NewWagerTransactionParams,
) (WagerTransaction, error) {
	if err := validateNewWagerTransactionParams(p); err != nil {
		return WagerTransaction{}, err
	}

	now := time.Now().UTC()

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
		status:                         WagerStatusPending,
		createdAt:                      now,
		updatedAt:                      now,
	}, nil
}

func validateNewWagerTransactionParams(p NewWagerTransactionParams) error {
	if p.ID == "" {
		return ErrWagerTransactionIDRequired
	}
	if p.ProviderID == "" {
		return ErrWagerProviderIDRequired
	}
	if p.ExternalTransactionID == "" {
		return ErrWagerExternalIDRequired
	}
	if p.IdempotencyKey == "" {
		return ErrWagerIdempotencyKeyRequired
	}
	if p.PayloadHash == "" {
		return ErrWagerPayloadHashRequired
	}
	if p.WalletID == "" {
		return ErrWagerWalletIDRequired
	}
	if p.PlayerID == "" {
		return ErrWagerPlayerIDRequired
	}
	if p.RoundID == "" {
		return ErrWagerRoundIDRequired
	}
	if p.GameID == "" {
		return ErrWagerGameIDRequired
	}
	if p.Kind == WagerKindOpening {
		return fmt.Errorf(
			"%w: OPENING cannot be submitted externally",
			ErrInvalidWagerKind,
		)
	}
	if !isExternalWagerKind(p.Kind) {
		return ErrInvalidWagerKind
	}
	if err := validateWagerMoney(p.Kind, p.Money); err != nil {
		return err
	}
	if (p.Kind == WagerKindRefund || p.Kind == WagerKindRollback) &&
		p.ReferenceExternalTransactionID == "" {
		return ErrMissingReference
	}

	return nil
}

func NewOpeningWagerTransaction(
	id string,
	walletID string,
	playerID string,
	money Money,
) (WagerTransaction, error) {
	if id == "" {
		return WagerTransaction{}, ErrWagerTransactionIDRequired
	}
	if walletID == "" {
		return WagerTransaction{}, ErrWagerWalletIDRequired
	}
	if playerID == "" {
		return WagerTransaction{}, ErrWagerPlayerIDRequired
	}
	if err := validateOpeningWagerMoney(money); err != nil {
		return WagerTransaction{}, err
	}

	now := time.Now().UTC()

	return WagerTransaction{
		id:        id,
		walletID:  walletID,
		playerID:  playerID,
		kind:      WagerKindOpening,
		money:     money,
		status:    WagerStatusProcessed,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func validateOpeningWagerMoney(money Money) error {
	if err := validateWagerCurrency(money); err != nil {
		return err
	}
	if money.amount <= 0 {
		return fmt.Errorf(
			"%w: OPENING amount must be greater than zero",
			ErrInvalidWagerAmount,
		)
	}

	return nil
}

func (w *WagerTransaction) ResolveReference(transactionID string) error {
	if err := w.ensureNotTerminal(); err != nil {
		return err
	}
	if w.referenceExternalTransactionID == "" {
		return ErrMissingReference
	}
	if transactionID == "" {
		return ErrReferenceTransactionIDRequired
	}

	w.referenceTransactionID = transactionID
	w.updatedAt = time.Now().UTC()

	return nil
}

func (w *WagerTransaction) ID() string {
	return w.id
}

func (w *WagerTransaction) ProviderID() string {
	return w.providerID
}

func (w *WagerTransaction) ExternalTransactionID() string {
	return w.externalTransactionID
}

func (w *WagerTransaction) IdempotencyKey() string {
	return w.idempotencyKey
}

func (w *WagerTransaction) PayloadHash() string {
	return w.payloadHash
}

func (w *WagerTransaction) WalletID() string {
	return w.walletID
}

func (w *WagerTransaction) PlayerID() string {
	return w.playerID
}

func (w *WagerTransaction) RoundID() string {
	return w.roundID
}

func (w *WagerTransaction) GameID() string {
	return w.gameID
}

func (w *WagerTransaction) Kind() WagerKind {
	return w.kind
}

func (w *WagerTransaction) Money() Money {
	return w.money
}

func (w *WagerTransaction) Status() WagerStatus {
	return w.status
}

func (w *WagerTransaction) ReferenceExternalTransactionID() string {
	return w.referenceExternalTransactionID
}

func (w *WagerTransaction) ReferenceTransactionID() string {
	return w.referenceTransactionID
}

func (w *WagerTransaction) FailureCode() FailureCode {
	return w.failureCode
}

func (w *WagerTransaction) ResultBalance() (Money, bool) {
	if w.resultBalance == nil {
		return Money{}, false
	}

	return *w.resultBalance, true
}

func (w *WagerTransaction) CreatedAt() time.Time {
	return w.createdAt
}

func (w *WagerTransaction) UpdatedAt() time.Time {
	return w.updatedAt
}
