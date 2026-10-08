package domain

import (
	"errors"
	"fmt"
)

var ErrInvalidWalletVersion = errors.New("invalid wallet version")

type WalletBalanceChanged struct {
	envelope      IntegrationEventEnvelope
	walletID      string
	transactionID string
	direction     LedgerDirection
	money         Money
	balanceBefore Money
	balanceAfter  Money
	walletVersion int64
}

type NewWalletBalanceChangedParams struct {
	EventID       string
	CorrelationID string
	CausationID   string

	WalletID      string
	TransactionID string
	Direction     LedgerDirection
	Money         Money
	BalanceBefore Money
	BalanceAfter  Money
	WalletVersion int64
}

func NewWalletBalanceChanged(
	p NewWalletBalanceChangedParams,
) (WalletBalanceChanged, error) {
	if p.WalletID == "" {
		return WalletBalanceChanged{}, ErrIntegrationWalletIDRequired
	}

	if p.TransactionID == "" {
		return WalletBalanceChanged{}, ErrIntegrationTransactionIDRequired
	}

	if p.WalletVersion < 1 {
		return WalletBalanceChanged{}, ErrInvalidWalletVersion
	}

	if err := validateWalletBalanceChangedMovement(p); err != nil {
		return WalletBalanceChanged{}, err
	}

	envelope, err := newIntegrationEventEnvelope(
		p.EventID,
		IntegrationEventTypeWalletBalanceChanged,
		p.WalletID,
		p.CorrelationID,
		p.CausationID,
	)
	if err != nil {
		return WalletBalanceChanged{}, err
	}

	return WalletBalanceChanged{
		envelope:      envelope,
		walletID:      p.WalletID,
		transactionID: p.TransactionID,
		direction:     p.Direction,
		money:         p.Money,
		balanceBefore: p.BalanceBefore,
		balanceAfter:  p.BalanceAfter,
		walletVersion: p.WalletVersion,
	}, nil
}

func validateWalletBalanceChangedMovement(
	p NewWalletBalanceChangedParams,
) error {
	if !isValidLedgerDirection(p.Direction) ||
		p.Money.amount <= 0 ||
		p.Money.currency != "BRL" ||
		p.BalanceBefore.amount < 0 ||
		p.BalanceAfter.amount < 0 {
		return ErrInvalidIntegrationEventPayload
	}

	expectedBalance, err := calculateLedgerBalance(
		p.Direction,
		p.BalanceBefore,
		p.Money,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidIntegrationEventPayload, err)
	}

	comparison, err := expectedBalance.Compare(p.BalanceAfter)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidIntegrationEventPayload, err)
	}

	if comparison != 0 {
		return ErrInvalidIntegrationEventPayload
	}

	return nil
}

func (e WalletBalanceChanged) Envelope() IntegrationEventEnvelope {
	return e.envelope
}

func (e WalletBalanceChanged) WalletID() string {
	return e.walletID
}

func (e WalletBalanceChanged) TransactionID() string {
	return e.transactionID
}

func (e WalletBalanceChanged) Direction() LedgerDirection {
	return e.direction
}

func (e WalletBalanceChanged) Money() Money {
	return e.money
}

func (e WalletBalanceChanged) BalanceBefore() Money {
	return e.balanceBefore
}

func (e WalletBalanceChanged) BalanceAfter() Money {
	return e.balanceAfter
}

func (e WalletBalanceChanged) WalletVersion() int64 {
	return e.walletVersion
}
