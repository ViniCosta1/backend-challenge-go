package application

import "context"

type TransactionRepositories interface {
	Wallets() WalletRepository
	WagerTransactions() WagerTransactionRepository
	Ledger() WalletLedgerRepository
	Inbox() InboxRepository
	Outbox() OutboxRepository
}

type TransactionManager interface {
	WithinTransaction(
		ctx context.Context,
		fn func(repositories TransactionRepositories) error,
	) error
}
