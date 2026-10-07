package domain

import (
	"errors"
	"fmt"
)

type WagerKind string

const (
	WagerKindOpening  WagerKind = "OPENING"
	WagerKindBet      WagerKind = "BET"
	WagerKindWin      WagerKind = "WIN"
	WagerKindLoss     WagerKind = "LOSS"
	WagerKindRefund   WagerKind = "REFUND"
	WagerKindRollback WagerKind = "ROLLBACK"
)

var (
	ErrInvalidWagerKind          = errors.New("invalid wager kind")
	ErrInvalidWagerAmount        = errors.New("invalid wager amount")
	ErrInvalidWagerCurrency      = errors.New("invalid wager currency")
	ErrInvalidWagerResultBalance = errors.New("invalid wager result balance")
)

func validateWagerMoney(kind WagerKind, money Money) error {
	if err := validateWagerCurrency(money); err != nil {
		return err
	}

	return validateWagerAmount(kind, money)
}

func validateWagerCurrency(money Money) error {
	if money.currency != "BRL" {
		return fmt.Errorf(
			"%w: expected BRL, got %q",
			ErrInvalidWagerCurrency,
			money.currency,
		)
	}

	return nil
}

func validateWagerResultBalance(
	transactionMoney Money,
	resultBalance Money,
) error {
	if resultBalance.amount < 0 {
		return fmt.Errorf(
			"%w: balance cannot be negative",
			ErrInvalidWagerResultBalance,
		)
	}

	if resultBalance.currency != transactionMoney.currency ||
		resultBalance.currency != "BRL" {
		return fmt.Errorf(
			"%w: currency must match transaction currency",
			ErrInvalidWagerResultBalance,
		)
	}

	return nil
}

func validateWagerAmount(kind WagerKind, money Money) error {
	switch kind {
	case WagerKindLoss:
		if money.amount != 0 {
			return fmt.Errorf(
				"%w: LOSS amount must be zero",
				ErrInvalidWagerAmount,
			)
		}

	case WagerKindBet,
		WagerKindWin,
		WagerKindRefund,
		WagerKindRollback:
		if money.amount <= 0 {
			return fmt.Errorf(
				"%w: %s amount must be greater than zero",
				ErrInvalidWagerAmount,
				kind,
			)
		}

	default:
		return ErrInvalidWagerKind
	}

	return nil
}

func isExternalWagerKind(kind WagerKind) bool {
	switch kind {
	case WagerKindBet,
		WagerKindWin,
		WagerKindLoss,
		WagerKindRefund,
		WagerKindRollback:
		return true

	default:
		return false
	}
}

func isValidWagerKind(kind WagerKind) bool {
	return kind == WagerKindOpening || isExternalWagerKind(kind)
}
