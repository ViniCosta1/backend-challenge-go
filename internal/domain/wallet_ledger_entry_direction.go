package domain

import "errors"

type LedgerDirection string

const (
	LedgerDirectionDebit  LedgerDirection = "DEBIT"
	LedgerDirectionCredit LedgerDirection = "CREDIT"
)

var ErrInvalidLedgerDirection = errors.New("invalid ledger direction")

func isValidLedgerDirection(direction LedgerDirection) bool {
	switch direction {
	case LedgerDirectionDebit, LedgerDirectionCredit:
		return true

	default:
		return false
	}
}
