package application

import "errors"

var (
	ErrNotFound                     = errors.New("not found")
	ErrWalletAlreadyExists          = errors.New("wallet already exists")
	ErrIdempotencyConflict          = errors.New("idempotency key reused with different payload")
	ErrExternalTransactionConflict  = errors.New("external transaction id already exists")
	ErrUnsupportedWagerKind         = errors.New("unsupported wager kind")
	ErrWagerWalletRelationship      = errors.New("wager does not belong to wallet player")
	ErrWagerReplayResultUnavailable = errors.New("persisted wager result is unavailable")
	ErrReversalAlreadyProcessed     = errors.New("reference already has a processed reversal")
	ErrInvalidCursor                = errors.New("invalid ledger cursor")
	ErrInvalidLimit                 = errors.New("ledger limit must be between 1 and 100")
	ErrUnavailable                  = errors.New("dependency unavailable")
	ErrInvalidMessage               = errors.New("invalid wager message")
	ErrInboxPayloadConflict         = errors.New("inbox message id reused with different payload")
)
