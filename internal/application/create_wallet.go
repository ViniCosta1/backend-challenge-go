package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type IDGenerator func() string

type CreateWallet struct {
	transactions TransactionManager
	generateID   IDGenerator
}

type CreateWalletInput struct {
	PlayerID       string
	InitialBalance domain.Money
}

type CreateWalletOutput struct {
	WalletID string
	PlayerID string
	Balance  domain.Money
	Version  int64
}

func NewCreateWallet(
	transactions TransactionManager,
	generateID IDGenerator,
) *CreateWallet {
	if generateID == nil {
		generateID = uuid.NewString
	}

	return &CreateWallet{
		transactions: transactions,
		generateID:   generateID,
	}
}

func (u *CreateWallet) Execute(
	ctx context.Context,
	input CreateWalletInput,
) (CreateWalletOutput, error) {
	wallet, err := domain.NewWallet(
		u.generateID(),
		input.PlayerID,
		input.InitialBalance,
	)
	if err != nil {
		return CreateWalletOutput{}, fmt.Errorf("create wallet entity: %w", err)
	}

	var opening *walletOpening
	if input.InitialBalance.Amount() > 0 {
		opening, err = u.newWalletOpening(wallet)
		if err != nil {
			return CreateWalletOutput{}, err
		}
	}

	if err := u.transactions.WithinTransaction(
		ctx,
		func(repositories TransactionRepositories) error {
			if err := repositories.Wallets().Create(ctx, &wallet); err != nil {
				return err
			}
			if opening == nil {
				return nil
			}

			if err := repositories.WagerTransactions().Create(
				ctx,
				&opening.transaction,
			); err != nil {
				return err
			}
			if err := repositories.Ledger().Create(ctx, &opening.ledger); err != nil {
				return err
			}
			if err := repositories.Outbox().Create(
				ctx,
				&opening.processedOutbox,
			); err != nil {
				return err
			}
			if err := repositories.Outbox().Create(
				ctx,
				&opening.balanceChangedOutbox,
			); err != nil {
				return err
			}

			return nil
		},
	); err != nil {
		return CreateWalletOutput{}, fmt.Errorf("persist wallet: %w", err)
	}

	return CreateWalletOutput{
		WalletID: wallet.ID(),
		PlayerID: wallet.PlayerID(),
		Balance:  wallet.Balance(),
		Version:  wallet.Version(),
	}, nil
}

type walletOpening struct {
	transaction          domain.WagerTransaction
	ledger               domain.WalletLedgerEntry
	processedOutbox      domain.OutboxEvent
	balanceChangedOutbox domain.OutboxEvent
}

func (u *CreateWallet) newWalletOpening(
	wallet domain.Wallet,
) (*walletOpening, error) {
	transaction, err := domain.NewOpeningWagerTransaction(
		u.generateID(),
		wallet.ID(),
		wallet.PlayerID(),
		wallet.Balance(),
	)
	if err != nil {
		return nil, fmt.Errorf("create opening transaction: %w", err)
	}

	zero, err := domain.ZeroMoney(wallet.Currency())
	if err != nil {
		return nil, fmt.Errorf("create opening zero balance: %w", err)
	}

	ledger, err := domain.NewWalletLedgerEntry(
		domain.NewWalletLedgerEntryParams{
			ID:            u.generateID(),
			WalletID:      wallet.ID(),
			TransactionID: transaction.ID(),
			Direction:     domain.LedgerDirectionCredit,
			Money:         wallet.Balance(),
			BalanceBefore: zero,
			BalanceAfter:  wallet.Balance(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create opening ledger entry: %w", err)
	}

	processed, err := domain.NewWagerTransactionProcessed(
		domain.NewWagerTransactionProcessedParams{
			EventID:       u.generateID(),
			CorrelationID: wallet.ID(),
			TransactionID: transaction.ID(),
			WalletID:      wallet.ID(),
			Kind:          domain.WagerKindOpening,
			ResultBalance: wallet.Balance(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create opening processed event: %w", err)
	}

	processedPayload, err := marshalWagerTransactionProcessed(processed)
	if err != nil {
		return nil, fmt.Errorf("serialize opening processed event: %w", err)
	}
	processedOutbox, err := domain.NewOutboxEvent(
		processed.Envelope(),
		processedPayload,
	)
	if err != nil {
		return nil, fmt.Errorf("create opening processed outbox: %w", err)
	}

	balanceChanged, err := domain.NewWalletBalanceChanged(
		domain.NewWalletBalanceChangedParams{
			EventID:       u.generateID(),
			CorrelationID: wallet.ID(),
			WalletID:      wallet.ID(),
			TransactionID: transaction.ID(),
			Direction:     domain.LedgerDirectionCredit,
			Money:         wallet.Balance(),
			BalanceBefore: zero,
			BalanceAfter:  wallet.Balance(),
			WalletVersion: wallet.Version(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create opening balance event: %w", err)
	}

	balanceChangedPayload, err := marshalWalletBalanceChanged(balanceChanged)
	if err != nil {
		return nil, fmt.Errorf("serialize opening balance event: %w", err)
	}
	balanceChangedOutbox, err := domain.NewOutboxEvent(
		balanceChanged.Envelope(),
		balanceChangedPayload,
	)
	if err != nil {
		return nil, fmt.Errorf("create opening balance outbox: %w", err)
	}

	return &walletOpening{
		transaction:          transaction,
		ledger:               ledger,
		processedOutbox:      processedOutbox,
		balanceChangedOutbox: balanceChangedOutbox,
	}, nil
}
