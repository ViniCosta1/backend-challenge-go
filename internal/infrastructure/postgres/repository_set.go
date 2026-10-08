package postgres

import "github.com/vinicosta1/backend-challenge-go/internal/application"

type RepositorySet struct {
	wallets           *WalletRepository
	wagerTransactions *WagerTransactionRepository
	ledger            *WalletLedgerRepository
	inbox             *InboxRepository
	outbox            *OutboxRepository
}

func NewRepositorySet(db DBTX) *RepositorySet {
	return &RepositorySet{
		wallets:           NewWalletRepository(db),
		wagerTransactions: NewWagerTransactionRepository(db),
		ledger:            NewWalletLedgerRepository(db),
		inbox:             NewInboxRepository(db),
		outbox:            NewOutboxRepository(db),
	}
}

func (r *RepositorySet) Wallets() application.WalletRepository {
	return r.wallets
}

func (r *RepositorySet) WagerTransactions() application.WagerTransactionRepository {
	return r.wagerTransactions
}

func (r *RepositorySet) Ledger() application.WalletLedgerRepository {
	return r.ledger
}

func (r *RepositorySet) Inbox() application.InboxRepository {
	return r.inbox
}

func (r *RepositorySet) Outbox() application.OutboxRepository {
	return r.outbox
}

var _ application.TransactionRepositories = (*RepositorySet)(nil)
