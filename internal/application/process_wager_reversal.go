package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

const (
	MaxReferenceAttempts    int32 = 5
	ReferenceRetryBaseDelay       = time.Second
)

func isReversalKind(kind domain.WagerKind) bool {
	return kind == domain.WagerKindRefund || kind == domain.WagerKindRollback
}

func (u *ProcessWagerTransaction) processReferencedWager(
	ctx context.Context,
	repositories TransactionRepositories,
	wallet *domain.Wallet,
	wager *domain.WagerTransaction,
	now time.Time,
) (ProcessWagerTransactionOutput, error) {
	if wager.Kind() == domain.WagerKindWin {
		return u.processReferencedWin(ctx, repositories, wallet, wager, now)
	}
	return u.processReversal(ctx, repositories, wallet, wager, now)
}

func (u *ProcessWagerTransaction) processReferencedWin(
	ctx context.Context,
	repositories TransactionRepositories,
	wallet *domain.Wallet,
	wager *domain.WagerTransaction,
	now time.Time,
) (ProcessWagerTransactionOutput, error) {
	before := wallet.Balance()
	reference, err := repositories.WagerTransactions().FindByProviderAndExternalTransactionID(
		ctx, wager.ProviderID(), wager.ReferenceExternalTransactionID(),
	)
	if errors.Is(err, ErrNotFound) {
		return u.deferReference(ctx, repositories, wallet, wager, now)
	}
	if err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if err := wager.ValidateWinReference(reference); err != nil {
		switch {
		case errors.Is(err, domain.ErrReferencePending):
			return u.deferReference(ctx, repositories, wallet, wager, now)
		case errors.Is(err, domain.ErrReferenceMismatch):
			return u.rejectWager(ctx, repositories, wager, before, domain.FailureCodeReferenceMismatch)
		case errors.Is(err, domain.ErrReferenceIncompatible):
			return u.rejectWager(ctx, repositories, wager, before, domain.FailureCodeReferenceIncompatible)
		default:
			return ProcessWagerTransactionOutput{}, err
		}
	}
	if err := wager.ResolveReference(reference.ID()); err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("resolve WIN reference: %w", err)
	}
	if err := wallet.Credit(wager.Money()); err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("credit referenced WIN: %w", err)
	}
	return u.persistProcessedMovement(ctx, repositories, wallet, wager, before, domain.LedgerDirectionCredit)
}

func (u *ProcessWagerTransaction) processReversal(
	ctx context.Context,
	repositories TransactionRepositories,
	wallet *domain.Wallet,
	wager *domain.WagerTransaction,
	now time.Time,
) (ProcessWagerTransactionOutput, error) {
	before := wallet.Balance()
	reference, err := repositories.WagerTransactions().FindByProviderAndExternalTransactionID(
		ctx, wager.ProviderID(), wager.ReferenceExternalTransactionID(),
	)
	if errors.Is(err, ErrNotFound) {
		return u.deferReference(ctx, repositories, wallet, wager, now)
	}
	if err != nil {
		return ProcessWagerTransactionOutput{}, err
	}

	direction, err := wager.ReversalDirection(reference)
	if errors.Is(err, domain.ErrReferencePending) {
		return u.deferReference(ctx, repositories, wallet, wager, now)
	}
	if errors.Is(err, domain.ErrReferenceMismatch) {
		return u.rejectWager(ctx, repositories, wager, before, domain.FailureCodeReferenceMismatch)
	}
	if errors.Is(err, domain.ErrReferenceIncompatible) {
		return u.rejectWager(ctx, repositories, wager, before, domain.FailureCodeReferenceIncompatible)
	}
	if err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if err := wager.ResolveReference(reference.ID()); err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("resolve reversal reference: %w", err)
	}

	if direction == domain.LedgerDirectionCredit {
		err = wallet.Credit(wager.Money())
	} else {
		err = wallet.Debit(wager.Money())
	}
	if errors.Is(err, domain.ErrInsufficientBalance) {
		return u.rejectWager(ctx, repositories, wager, before, domain.FailureCodeRollbackInsufficientBalance)
	}
	if err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("apply reversal movement: %w", err)
	}
	return u.persistProcessedMovement(ctx, repositories, wallet, wager, before, direction)
}

func (u *ProcessWagerTransaction) deferReference(
	ctx context.Context,
	repositories TransactionRepositories,
	wallet *domain.Wallet,
	wager *domain.WagerTransaction,
	now time.Time,
) (ProcessWagerTransactionOutput, error) {
	if wager.ReferenceAttempts() >= MaxReferenceAttempts {
		return u.rejectWager(ctx, repositories, wager, wallet.Balance(), domain.FailureCodeReferenceNotFound)
	}
	initial := wager.Status() == domain.WagerStatusPending
	if initial {
		if err := wager.MarkPendingReference(); err != nil {
			return ProcessWagerTransactionOutput{}, err
		}
	}
	delay := ReferenceRetryBaseDelay * time.Duration(1<<uint(wager.ReferenceAttempts()))
	if err := wager.ScheduleReferenceRetry(now.UTC().Add(delay)); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if err := repositories.WagerTransactions().Update(ctx, wager); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if initial {
		if err := u.persistPendingReferenceOutbox(ctx, repositories.Outbox(), wager); err != nil {
			return ProcessWagerTransactionOutput{}, err
		}
	}
	return ProcessWagerTransactionOutput{TransactionID: wager.ID(), Status: wager.Status()}, nil
}

func (u *ProcessWagerTransaction) persistPendingReferenceOutbox(
	ctx context.Context,
	repository OutboxRepository,
	wager *domain.WagerTransaction,
) error {
	event, err := domain.NewWagerTransactionPendingReference(domain.NewWagerTransactionPendingReferenceParams{
		EventID:                        u.generateID(),
		CorrelationID:                  wager.ID(),
		TransactionID:                  wager.ID(),
		WalletID:                       wager.WalletID(),
		ProviderID:                     wager.ProviderID(),
		ExternalTransactionID:          wager.ExternalTransactionID(),
		ReferenceExternalTransactionID: wager.ReferenceExternalTransactionID(),
		Kind:                           wager.Kind(),
	})
	if err != nil {
		return fmt.Errorf("create pending reference event: %w", err)
	}
	payload, err := marshalWagerTransactionPendingReference(event)
	if err != nil {
		return fmt.Errorf("serialize pending reference event: %w", err)
	}
	outbox, err := domain.NewOutboxEvent(event.Envelope(), payload)
	if err != nil {
		return fmt.Errorf("create pending reference outbox: %w", err)
	}
	return repository.Create(ctx, &outbox)
}
