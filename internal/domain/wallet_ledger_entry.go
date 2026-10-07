package domain

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrLedgerEntryIDRequired       = errors.New("ledger entry id is required")
	ErrLedgerWalletIDRequired      = errors.New("ledger wallet id is required")
	ErrLedgerTransactionIDRequired = errors.New(
		"ledger transaction id is required",
	)
	ErrInvalidLedgerMoney      = errors.New("invalid ledger money")
	ErrInvalidLedgerBalance    = errors.New("invalid ledger balance")
	ErrInconsistentLedgerEntry = errors.New("inconsistent ledger entry")
	ErrInvalidLedgerCreatedAt  = errors.New("invalid ledger created at")
)

type WalletLedgerEntry struct {
	id            string
	walletID      string
	transactionID string
	direction     LedgerDirection
	money         Money
	balanceBefore Money
	balanceAfter  Money
	createdAt     time.Time
}

type NewWalletLedgerEntryParams struct {
	ID            string
	WalletID      string
	TransactionID string
	Direction     LedgerDirection
	Money         Money
	BalanceBefore Money
	BalanceAfter  Money
}

func NewWalletLedgerEntry(
	p NewWalletLedgerEntryParams,
) (WalletLedgerEntry, error) {
	if err := validateWalletLedgerEntry(
		p.ID,
		p.WalletID,
		p.TransactionID,
		p.Direction,
		p.Money,
		p.BalanceBefore,
		p.BalanceAfter,
	); err != nil {
		return WalletLedgerEntry{}, err
	}

	return WalletLedgerEntry{
		id:            p.ID,
		walletID:      p.WalletID,
		transactionID: p.TransactionID,
		direction:     p.Direction,
		money:         p.Money,
		balanceBefore: p.BalanceBefore,
		balanceAfter:  p.BalanceAfter,
		createdAt:     time.Now().UTC(),
	}, nil
}

type RehydrateWalletLedgerEntryParams struct {
	ID            string
	WalletID      string
	TransactionID string
	Direction     LedgerDirection
	Money         Money
	BalanceBefore Money
	BalanceAfter  Money
	CreatedAt     time.Time
}

func RehydrateWalletLedgerEntry(
	p RehydrateWalletLedgerEntryParams,
) (WalletLedgerEntry, error) {
	if err := validateWalletLedgerEntry(
		p.ID,
		p.WalletID,
		p.TransactionID,
		p.Direction,
		p.Money,
		p.BalanceBefore,
		p.BalanceAfter,
	); err != nil {
		return WalletLedgerEntry{}, err
	}

	if p.CreatedAt.IsZero() || p.CreatedAt.Location() != time.UTC {
		return WalletLedgerEntry{}, ErrInvalidLedgerCreatedAt
	}

	return WalletLedgerEntry{
		id:            p.ID,
		walletID:      p.WalletID,
		transactionID: p.TransactionID,
		direction:     p.Direction,
		money:         p.Money,
		balanceBefore: p.BalanceBefore,
		balanceAfter:  p.BalanceAfter,
		createdAt:     p.CreatedAt,
	}, nil
}

func validateWalletLedgerEntry(
	id string,
	walletID string,
	transactionID string,
	direction LedgerDirection,
	money Money,
	balanceBefore Money,
	balanceAfter Money,
) error {
	if id == "" {
		return ErrLedgerEntryIDRequired
	}

	if walletID == "" {
		return ErrLedgerWalletIDRequired
	}

	if transactionID == "" {
		return ErrLedgerTransactionIDRequired
	}

	if !isValidLedgerDirection(direction) {
		return ErrInvalidLedgerDirection
	}

	if money.amount <= 0 || money.currency != "BRL" {
		return ErrInvalidLedgerMoney
	}

	if balanceBefore.amount < 0 || balanceAfter.amount < 0 {
		return ErrInvalidLedgerBalance
	}

	expectedBalance, err := calculateLedgerBalance(
		direction,
		balanceBefore,
		money,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLedgerBalance, err)
	}

	comparison, err := expectedBalance.Compare(balanceAfter)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLedgerBalance, err)
	}

	if comparison != 0 {
		return ErrInconsistentLedgerEntry
	}

	return nil
}

func calculateLedgerBalance(
	direction LedgerDirection,
	balanceBefore Money,
	money Money,
) (Money, error) {
	if direction == LedgerDirectionDebit {
		return balanceBefore.Subtract(money)
	}

	return balanceBefore.Add(money)
}

// Getters
func (e *WalletLedgerEntry) ID() string {
	return e.id
}

func (e *WalletLedgerEntry) WalletID() string {
	return e.walletID
}

func (e *WalletLedgerEntry) TransactionID() string {
	return e.transactionID
}

func (e *WalletLedgerEntry) Direction() LedgerDirection {
	return e.direction
}

func (e *WalletLedgerEntry) Money() Money {
	return e.money
}

func (e *WalletLedgerEntry) BalanceBefore() Money {
	return e.balanceBefore
}

func (e *WalletLedgerEntry) BalanceAfter() Money {
	return e.balanceAfter
}

func (e *WalletLedgerEntry) CreatedAt() time.Time {
	return e.createdAt
}
