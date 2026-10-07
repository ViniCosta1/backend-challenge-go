package domain

import (
	"errors"
	"time"
)

type Wallet struct {
	id        string // wallet-uuid....
	playerID  string // player-uuid....
	currency  string
	balance   Money
	version   int32
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

	if initialBalance.amount < 0 {
		return Wallet{}, errors.New("initial balance must be non-negative")
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
	version int32,
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

	if balance.amount < 0 {
		return Wallet{}, errors.New("wallet balance cannot be negative")
	}

	if balance.currency != currency {
		return Wallet{}, errors.New("wallet currency does not match balance currency")
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
		return errors.New("insufficient balance")
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

func (w *Wallet) Version() int32 {
	return w.version
}

func (w *Wallet) CreatedAt() time.Time {
	return w.createdAt
}

func (w *Wallet) UpdatedAt() time.Time {
	return w.updatedAt
}
