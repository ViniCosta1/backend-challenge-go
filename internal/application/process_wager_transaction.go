package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type ProcessWagerTransaction struct {
	transactions TransactionManager
	generateID   IDGenerator
}

type ProcessWagerTransactionInput struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       string
	WalletID                       string
	RoundID                        string
	GameID                         string
	Kind                           domain.WagerKind
	Money                          domain.Money
	ReferenceExternalTransactionID string
}

type ProcessWagerTransactionOutput struct {
	TransactionID    string
	Status           domain.WagerStatus
	Balance          domain.Money
	HasResultBalance bool
	FailureCode      domain.FailureCode
	IdempotentReplay bool
}

func NewProcessWagerTransaction(
	transactions TransactionManager,
	generateID IDGenerator,
) *ProcessWagerTransaction {
	if generateID == nil {
		generateID = uuid.NewString
	}

	return &ProcessWagerTransaction{
		transactions: transactions,
		generateID:   generateID,
	}
}

func (u *ProcessWagerTransaction) Execute(
	ctx context.Context,
	input ProcessWagerTransactionInput,
) (ProcessWagerTransactionOutput, error) {
	wager, payloadHash, err := u.prepareWager(input)
	if err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	var output ProcessWagerTransactionOutput
	err = u.transactions.WithinTransaction(ctx, func(repositories TransactionRepositories) error {
		output, err = u.executePrepared(ctx, repositories, input, &wager, payloadHash)
		return err
	})
	if err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("process wager transaction: %w", err)
	}
	return output, nil
}

// executeWithRepositories reuses the financial core inside the caller's
// transaction. It must not start another transaction (Inbox uses this path).
func (u *ProcessWagerTransaction) executeWithRepositories(ctx context.Context, repositories TransactionRepositories, input ProcessWagerTransactionInput) (ProcessWagerTransactionOutput, error) {
	wager, payloadHash, err := u.prepareWager(input)
	if err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	return u.executePrepared(ctx, repositories, input, &wager, payloadHash)
}

func (u *ProcessWagerTransaction) prepareWager(input ProcessWagerTransactionInput) (domain.WagerTransaction, string, error) {
	if !isSupportedFinancialWager(input.Kind) {
		return domain.WagerTransaction{}, "", fmt.Errorf(
			"process wager kind %q: %w",
			input.Kind,
			ErrUnsupportedWagerKind,
		)
	}
	if input.ReferenceExternalTransactionID != "" && input.Kind != domain.WagerKindWin && !isReversalKind(input.Kind) {
		return domain.WagerTransaction{}, "", fmt.Errorf("references are supported only for WIN and reversals: %w", domain.ErrInvalidWagerState)
	}

	payloadHash, err := calculateWagerPayloadHash(input)
	if err != nil {
		return domain.WagerTransaction{}, "", err
	}

	wager, err := domain.NewWagerTransaction(domain.NewWagerTransactionParams{
		ID:                             u.generateID(),
		ProviderID:                     input.ProviderID,
		ExternalTransactionID:          input.ExternalTransactionID,
		IdempotencyKey:                 input.IdempotencyKey,
		PayloadHash:                    payloadHash,
		WalletID:                       input.WalletID,
		PlayerID:                       input.PlayerID,
		RoundID:                        input.RoundID,
		GameID:                         input.GameID,
		Kind:                           input.Kind,
		Money:                          input.Money,
		ReferenceExternalTransactionID: input.ReferenceExternalTransactionID,
	})
	if err != nil {
		return domain.WagerTransaction{}, "", fmt.Errorf("create wager transaction: %w", err)
	}
	return wager, payloadHash, nil
}

func (u *ProcessWagerTransaction) executePrepared(ctx context.Context, repositories TransactionRepositories, input ProcessWagerTransactionInput, wager *domain.WagerTransaction, payloadHash string) (ProcessWagerTransactionOutput, error) {
	created, err := repositories.WagerTransactions().CreateIfAbsent(ctx, wager)
	if err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if !created {
		return resolveExistingWager(ctx, repositories.WagerTransactions(), input, payloadHash)
	}
	wallet, err := repositories.Wallets().FindByIDForUpdate(ctx, input.WalletID)
	if err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if wallet.PlayerID() != input.PlayerID {
		if wager.ReferenceExternalTransactionID() != "" {
			return u.rejectWager(ctx, repositories, wager, wallet.Balance(), domain.FailureCodeReferenceMismatch)
		}
		return ProcessWagerTransactionOutput{}, fmt.Errorf("wallet %q player %q differs from wager player %q: %w", wallet.ID(), wallet.PlayerID(), input.PlayerID, ErrWagerWalletRelationship)
	}
	if wallet.Currency() != input.Money.Currency() {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("wallet %q currency %q differs from wager currency %q: %w", wallet.ID(), wallet.Currency(), input.Money.Currency(), domain.ErrInvalidWagerCurrency)
	}
	return u.processNewWager(ctx, repositories, wallet, wager)
}

func isSupportedFinancialWager(kind domain.WagerKind) bool {
	return kind == domain.WagerKindBet ||
		kind == domain.WagerKindWin ||
		kind == domain.WagerKindLoss || isReversalKind(kind)
}

func resolveExistingWager(
	ctx context.Context,
	repository WagerTransactionRepository,
	input ProcessWagerTransactionInput,
	payloadHash string,
) (ProcessWagerTransactionOutput, error) {
	existing, err := repository.FindByProviderAndIdempotencyKey(
		ctx,
		input.ProviderID,
		input.IdempotencyKey,
	)
	if err == nil {
		if existing.PayloadHash() != payloadHash {
			return ProcessWagerTransactionOutput{}, ErrIdempotencyConflict
		}

		balance, hasBalance := existing.ResultBalance()
		if existing.Status() == domain.WagerStatusPendingReference {
			return ProcessWagerTransactionOutput{
				TransactionID:    existing.ID(),
				Status:           existing.Status(),
				IdempotentReplay: true,
			}, nil
		}
		if !hasBalance {
			return ProcessWagerTransactionOutput{}, fmt.Errorf(
				"replay wager transaction %q: %w",
				existing.ID(),
				ErrWagerReplayResultUnavailable,
			)
		}

		return ProcessWagerTransactionOutput{
			TransactionID:    existing.ID(),
			Status:           existing.Status(),
			Balance:          balance,
			HasResultBalance: true,
			FailureCode:      existing.FailureCode(),
			IdempotentReplay: true,
		}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ProcessWagerTransactionOutput{}, err
	}

	_, err = repository.FindByProviderAndExternalTransactionID(
		ctx,
		input.ProviderID,
		input.ExternalTransactionID,
	)
	if err == nil {
		return ProcessWagerTransactionOutput{}, ErrExternalTransactionConflict
	}
	if !errors.Is(err, ErrNotFound) {
		return ProcessWagerTransactionOutput{}, err
	}

	return ProcessWagerTransactionOutput{}, fmt.Errorf(
		"wager reservation conflicted but no matching business identity was found",
	)
}

func (u *ProcessWagerTransaction) processNewWager(
	ctx context.Context,
	repositories TransactionRepositories,
	wallet *domain.Wallet,
	wager *domain.WagerTransaction,
) (ProcessWagerTransactionOutput, error) {
	balanceBefore := wallet.Balance()

	switch wager.Kind() {
	case domain.WagerKindBet:
		if err := wallet.Debit(wager.Money()); err != nil {
			if errors.Is(err, domain.ErrInsufficientBalance) {
				return u.rejectWager(ctx, repositories, wager, balanceBefore, domain.FailureCodeInsufficientBalance)
			}
			return ProcessWagerTransactionOutput{}, fmt.Errorf("debit wallet: %w", err)
		}

		return u.persistProcessedMovement(
			ctx,
			repositories,
			wallet,
			wager,
			balanceBefore,
			domain.LedgerDirectionDebit,
		)

	case domain.WagerKindWin:
		if wager.ReferenceExternalTransactionID() != "" {
			return u.processReferencedWager(ctx, repositories, wallet, wager, time.Now().UTC())
		}
		if err := wallet.Credit(wager.Money()); err != nil {
			return ProcessWagerTransactionOutput{}, fmt.Errorf("credit wallet: %w", err)
		}

		return u.persistProcessedMovement(
			ctx,
			repositories,
			wallet,
			wager,
			balanceBefore,
			domain.LedgerDirectionCredit,
		)

	case domain.WagerKindLoss:
		if err := wager.MarkProcessed(balanceBefore); err != nil {
			return ProcessWagerTransactionOutput{}, fmt.Errorf("mark LOSS processed: %w", err)
		}
		if err := repositories.WagerTransactions().Update(ctx, wager); err != nil {
			return ProcessWagerTransactionOutput{}, err
		}
		if err := u.persistProcessedOutbox(ctx, repositories.Outbox(), wager, balanceBefore); err != nil {
			return ProcessWagerTransactionOutput{}, err
		}

		return processedWagerOutput(wager, balanceBefore), nil

	case domain.WagerKindRefund, domain.WagerKindRollback:
		return u.processReferencedWager(ctx, repositories, wallet, wager, time.Now().UTC())

	default:
		return ProcessWagerTransactionOutput{}, ErrUnsupportedWagerKind
	}
}

func (u *ProcessWagerTransaction) rejectWager(
	ctx context.Context,
	repositories TransactionRepositories,
	wager *domain.WagerTransaction,
	observedBalance domain.Money,
	code domain.FailureCode,
) (ProcessWagerTransactionOutput, error) {
	if err := wager.MarkRejectedWithBalance(code, observedBalance); err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("mark wager rejected: %w", err)
	}
	if err := repositories.WagerTransactions().Update(ctx, wager); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}

	rejected, err := domain.NewWagerTransactionRejected(
		domain.NewWagerTransactionRejectedParams{
			EventID:               u.generateID(),
			CorrelationID:         wager.ID(),
			TransactionID:         wager.ID(),
			WalletID:              wager.WalletID(),
			ProviderID:            wager.ProviderID(),
			ExternalTransactionID: wager.ExternalTransactionID(),
			Kind:                  wager.Kind(),
			FailureCode:           wager.FailureCode(),
		},
	)
	if err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("create rejected event: %w", err)
	}
	payload, err := marshalWagerTransactionRejected(rejected)
	if err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("serialize rejected event: %w", err)
	}
	outbox, err := domain.NewOutboxEvent(rejected.Envelope(), payload)
	if err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("create rejected outbox: %w", err)
	}
	if err := repositories.Outbox().Create(ctx, &outbox); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}

	return ProcessWagerTransactionOutput{
		TransactionID:    wager.ID(),
		Status:           wager.Status(),
		Balance:          observedBalance,
		HasResultBalance: true,
		FailureCode:      wager.FailureCode(),
	}, nil
}

func (u *ProcessWagerTransaction) persistProcessedMovement(
	ctx context.Context,
	repositories TransactionRepositories,
	wallet *domain.Wallet,
	wager *domain.WagerTransaction,
	balanceBefore domain.Money,
	direction domain.LedgerDirection,
) (ProcessWagerTransactionOutput, error) {
	balanceAfter := wallet.Balance()
	processed := *wager
	if err := processed.MarkProcessed(balanceAfter); err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("mark wager processed: %w", err)
	}
	if err := repositories.WagerTransactions().Update(ctx, &processed); err != nil {
		if errors.Is(err, ErrReversalAlreadyProcessed) {
			return u.rejectWager(ctx, repositories, wager, balanceBefore, domain.FailureCodeReferenceAlreadyReversed)
		}
		return ProcessWagerTransactionOutput{}, err
	}
	*wager = processed
	if err := repositories.Wallets().Update(ctx, wallet); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}

	ledger, err := domain.NewWalletLedgerEntry(domain.NewWalletLedgerEntryParams{
		ID:            u.generateID(),
		WalletID:      wallet.ID(),
		TransactionID: wager.ID(),
		Direction:     direction,
		Money:         wager.Money(),
		BalanceBefore: balanceBefore,
		BalanceAfter:  balanceAfter,
	})
	if err != nil {
		return ProcessWagerTransactionOutput{}, fmt.Errorf("create ledger entry: %w", err)
	}
	if err := repositories.Ledger().Create(ctx, &ledger); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if err := u.persistProcessedOutbox(ctx, repositories.Outbox(), wager, balanceAfter); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}
	if err := u.persistBalanceChangedOutbox(
		ctx,
		repositories.Outbox(),
		wallet,
		wager,
		direction,
		balanceBefore,
		balanceAfter,
	); err != nil {
		return ProcessWagerTransactionOutput{}, err
	}

	return processedWagerOutput(wager, balanceAfter), nil
}

func (u *ProcessWagerTransaction) persistProcessedOutbox(
	ctx context.Context,
	repository OutboxRepository,
	wager *domain.WagerTransaction,
	balance domain.Money,
) error {
	event, err := domain.NewWagerTransactionProcessed(
		domain.NewWagerTransactionProcessedParams{
			EventID:               u.generateID(),
			CorrelationID:         wager.ID(),
			TransactionID:         wager.ID(),
			WalletID:              wager.WalletID(),
			ProviderID:            wager.ProviderID(),
			ExternalTransactionID: wager.ExternalTransactionID(),
			Kind:                  wager.Kind(),
			ResultBalance:         balance,
		},
	)
	if err != nil {
		return fmt.Errorf("create processed event: %w", err)
	}
	payload, err := marshalWagerTransactionProcessed(event)
	if err != nil {
		return fmt.Errorf("serialize processed event: %w", err)
	}
	outbox, err := domain.NewOutboxEvent(event.Envelope(), payload)
	if err != nil {
		return fmt.Errorf("create processed outbox: %w", err)
	}
	if err := repository.Create(ctx, &outbox); err != nil {
		return err
	}

	return nil
}

func (u *ProcessWagerTransaction) persistBalanceChangedOutbox(
	ctx context.Context,
	repository OutboxRepository,
	wallet *domain.Wallet,
	wager *domain.WagerTransaction,
	direction domain.LedgerDirection,
	balanceBefore domain.Money,
	balanceAfter domain.Money,
) error {
	event, err := domain.NewWalletBalanceChanged(domain.NewWalletBalanceChangedParams{
		EventID:       u.generateID(),
		CorrelationID: wager.ID(),
		WalletID:      wallet.ID(),
		TransactionID: wager.ID(),
		Direction:     direction,
		Money:         wager.Money(),
		BalanceBefore: balanceBefore,
		BalanceAfter:  balanceAfter,
		WalletVersion: wallet.Version(),
	})
	if err != nil {
		return fmt.Errorf("create balance changed event: %w", err)
	}
	payload, err := marshalWalletBalanceChanged(event)
	if err != nil {
		return fmt.Errorf("serialize balance changed event: %w", err)
	}
	outbox, err := domain.NewOutboxEvent(event.Envelope(), payload)
	if err != nil {
		return fmt.Errorf("create balance changed outbox: %w", err)
	}
	if err := repository.Create(ctx, &outbox); err != nil {
		return err
	}

	return nil
}

func processedWagerOutput(
	wager *domain.WagerTransaction,
	balance domain.Money,
) ProcessWagerTransactionOutput {
	return ProcessWagerTransactionOutput{
		TransactionID:    wager.ID(),
		Status:           wager.Status(),
		Balance:          balance,
		HasResultBalance: true,
	}
}
