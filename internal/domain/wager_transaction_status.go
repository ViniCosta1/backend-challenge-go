package domain

import (
	"errors"
	"fmt"
	"time"
)

type WagerStatus string

const (
	WagerStatusPending          WagerStatus = "PENDING"
	WagerStatusPendingReference WagerStatus = "PENDING_REFERENCE"
	WagerStatusProcessed        WagerStatus = "PROCESSED"
	WagerStatusRejected         WagerStatus = "REJECTED"
	WagerStatusFailed           WagerStatus = "FAILED"
)

type FailureCode string

const (
	FailureCodeInsufficientBalance         FailureCode = "INSUFFICIENT_BALANCE"
	FailureCodeRollbackInsufficientBalance FailureCode = "ROLLBACK_INSUFFICIENT_BALANCE"
	FailureCodeReferenceNotFound           FailureCode = "REFERENCE_NOT_FOUND"
	FailureCodeReferenceIncompatible       FailureCode = "REFERENCE_INCOMPATIBLE"
	FailureCodeReferenceMismatch           FailureCode = "REFERENCE_MISMATCH"
	FailureCodeReferenceAlreadyReversed    FailureCode = "REFERENCE_ALREADY_REVERSED"
)

var (
	ErrInvalidWagerStatus  = errors.New("invalid wager status")
	ErrFailureCodeRequired = errors.New("failure code is required")
	ErrInvalidTransition   = errors.New(
		"invalid wager transaction transition",
	)
	ErrTerminalWager = errors.New(
		"wager transaction is already terminal",
	)
)

func (w *WagerTransaction) IsTerminal() bool {
	switch w.status {
	case WagerStatusProcessed,
		WagerStatusRejected,
		WagerStatusFailed:
		return true

	default:
		return false
	}
}

func (w *WagerTransaction) ensureNotTerminal() error {
	if w.IsTerminal() {
		return ErrTerminalWager
	}

	return nil
}

func (w *WagerTransaction) MarkPendingReference() error {
	if err := w.ensureNotTerminal(); err != nil {
		return err
	}

	if w.status != WagerStatusPending {
		return fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition,
			w.status,
			WagerStatusPendingReference,
		)
	}

	if w.referenceExternalTransactionID == "" {
		return ErrMissingReference
	}

	w.status = WagerStatusPendingReference
	w.updatedAt = time.Now().UTC()

	return nil
}

func (w *WagerTransaction) MarkProcessed(resultBalance Money) error {
	if err := w.ensureNotTerminal(); err != nil {
		return err
	}

	if w.status != WagerStatusPending &&
		w.status != WagerStatusPendingReference {
		return fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition,
			w.status,
			WagerStatusProcessed,
		)
	}

	if w.referenceExternalTransactionID != "" &&
		w.referenceTransactionID == "" {
		return fmt.Errorf(
			"%w: reference must be resolved before processing",
			ErrInvalidWagerState,
		)
	}

	if err := validateWagerResultBalance(w.money, resultBalance); err != nil {
		return err
	}

	w.status = WagerStatusProcessed

	balance := resultBalance
	w.resultBalance = &balance

	w.failureCode = ""
	w.referenceNextAttemptAt = nil
	w.updatedAt = time.Now().UTC()

	return nil
}

func (w *WagerTransaction) MarkRejected(failureCode FailureCode) error {
	if err := w.ensureNotTerminal(); err != nil {
		return err
	}

	if failureCode == "" {
		return ErrFailureCodeRequired
	}

	if w.status != WagerStatusPending &&
		w.status != WagerStatusPendingReference {
		return fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition,
			w.status,
			WagerStatusRejected,
		)
	}

	w.status = WagerStatusRejected
	w.failureCode = failureCode
	w.referenceNextAttemptAt = nil
	w.updatedAt = time.Now().UTC()

	return nil
}

// MarkRejectedWithBalance preserves the observed financial result without
// applying a movement. Legacy rejections may still have no observed balance.
func (w *WagerTransaction) MarkRejectedWithBalance(code FailureCode, balance Money) error {
	if err := validateWagerResultBalance(w.money, balance); err != nil {
		return err
	}
	if err := w.MarkRejected(code); err != nil {
		return err
	}
	w.resultBalance = &balance
	return nil
}

func (w *WagerTransaction) MarkFailed(failureCode FailureCode) error {
	if err := w.ensureNotTerminal(); err != nil {
		return err
	}

	if failureCode == "" {
		return ErrFailureCodeRequired
	}

	if w.status != WagerStatusPending &&
		w.status != WagerStatusPendingReference {
		return fmt.Errorf(
			"%w: %s -> %s",
			ErrInvalidTransition,
			w.status,
			WagerStatusFailed,
		)
	}

	w.status = WagerStatusFailed
	w.failureCode = failureCode
	w.referenceNextAttemptAt = nil
	w.updatedAt = time.Now().UTC()

	return nil
}

func isValidWagerStatus(status WagerStatus) bool {
	switch status {
	case WagerStatusPending,
		WagerStatusPendingReference,
		WagerStatusProcessed,
		WagerStatusRejected,
		WagerStatusFailed:
		return true

	default:
		return false
	}
}
