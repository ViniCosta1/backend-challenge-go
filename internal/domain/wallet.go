package domain

import (
	"errors"
	"time"
)

var ErrInsufficientBalance = errors.New("insufficient balance")

type Wallet struct {
	id        string // wallet-uuid....
	playerID  string // player-uuid....
	currency  string
	balance   Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

func NewWallet(
	id string,
	playerID string,
	initialBalance Money,
) (Wallet, error) {
	if id == "" {
		return Wallet{}, errors.New("wallet id is required")
	}

	if playerID == "" {
		return Wallet{}, errors.New("player id is required")
	}

	if initialBalance.currency != "BRL" || initialBalance.amount < 0 {
		return Wallet{}, errors.New("initial balance must be valid non-negative BRL Money")
	}

	now := time.Now().UTC()

	return Wallet{
		id:        id,
		playerID:  playerID,
		currency:  initialBalance.currency,
		balance:   initialBalance,
		version:   1,
		createdAt: now,
		updatedAt: now,
	}, nil
}

func RehydrateWallet(
	id string,
	playerID string,
	currency string,
	balance Money,
	version int64,
	createdAt time.Time,
	updatedAt time.Time,
) (Wallet, error) {
	if id == "" {
		return Wallet{}, errors.New("wallet id is required")
	}

	if playerID == "" {
		return Wallet{}, errors.New("player id is required")
	}

	if version < 1 {
		return Wallet{}, errors.New("wallet version must be at least 1")
	}

	if currency != "BRL" || balance.currency != currency || balance.amount < 0 {
		return Wallet{}, errors.New("wallet balance must be valid non-negative BRL Money")
	}

	if createdAt.IsZero() || updatedAt.IsZero() ||
		createdAt.Location() != time.UTC || updatedAt.Location() != time.UTC ||
		updatedAt.Before(createdAt) {
		return Wallet{}, errors.New("wallet timestamps must be valid UTC values in chronological order")
	}

	return Wallet{
		id:        id,
		playerID:  playerID,
		currency:  currency,
		balance:   balance,
		version:   version,
		createdAt: createdAt,
		updatedAt: updatedAt,
	}, nil
}

func (w *Wallet) Debit(amount Money) error {
	if amount.amount <= 0 {
		return errors.New("debit amount must be positive")
	}

	if w.currency != amount.currency {
		return errors.New("currency mismatch")
	}

	comparison, err := w.balance.Compare(amount)
	if err != nil {
		return err
	}

	if comparison < 0 { // w.balance.amount is less than amount?
		return ErrInsufficientBalance
	}

	newBalance, err := w.balance.Subtract(amount)
	if err != nil {
		return err
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = time.Now().UTC()

	return nil
}

func (w *Wallet) Credit(amount Money) error {
	if amount.amount <= 0 {
		return errors.New("credit amount must be positive")
	}

	if w.currency != amount.currency {
		return errors.New("currency mismatch")
	}

	newBalance, err := w.balance.Add(amount)
	if err != nil {
		return err
	}

	w.balance = newBalance
	w.version++
	w.updatedAt = time.Now().UTC()

	return nil
}

// Getters
func (w *Wallet) ID() string {
	return w.id
}

func (w *Wallet) PlayerID() string {
	return w.playerID
}

func (w *Wallet) Currency() string {
	return w.currency
}

func (w *Wallet) Balance() Money {
	return w.balance
}

func (w *Wallet) Version() int64 {
	return w.version
}

func (w *Wallet) CreatedAt() time.Time {
	return w.createdAt
}

func (w *Wallet) UpdatedAt() time.Time {
	return w.updatedAt
}
