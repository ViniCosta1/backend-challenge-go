package application

import (
	"context"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type WalletRepository interface {
	Create(ctx context.Context, wallet *domain.Wallet) error
	FindByID(ctx context.Context, id string) (*domain.Wallet, error)
	FindByIDForUpdate(ctx context.Context, id string) (*domain.Wallet, error)
	Update(ctx context.Context, wallet *domain.Wallet) error
	ReconciliationSnapshot(ctx context.Context, id string) (ReconciliationSnapshot, error)
}

type WagerTransactionRepository interface {
	Create(ctx context.Context, wager *domain.WagerTransaction) error
	CreateIfAbsent(ctx context.Context, wager *domain.WagerTransaction) (bool, error)
	FindByID(ctx context.Context, id string) (*domain.WagerTransaction, error)
	FindByIDAndProvider(ctx context.Context, id, providerID string) (*domain.WagerTransaction, error)
	FindByProviderAndIdempotencyKey(
		ctx context.Context,
		providerID string,
		idempotencyKey string,
	) (*domain.WagerTransaction, error)
	FindByProviderAndExternalTransactionID(
		ctx context.Context,
		providerID string,
		externalTransactionID string,
	) (*domain.WagerTransaction, error)
	Update(ctx context.Context, wager *domain.WagerTransaction) error
	FindNextPendingReferenceForUpdate(ctx context.Context, dueAt time.Time) (*domain.WagerTransaction, error)
}

type WalletLedgerRepository interface {
	Create(ctx context.Context, entry *domain.WalletLedgerEntry) error
	ListByWallet(ctx context.Context, walletID string, after *LedgerPosition, limit int) ([]domain.WalletLedgerEntry, error)
}

type InboxRepository interface {
	Create(ctx context.Context, message *domain.InboxMessage) error
	CreateIfAbsent(ctx context.Context, message *domain.InboxMessage) (bool, error)
	FindByConsumerAndMessageIDForUpdate(ctx context.Context, consumerName, messageID string) (*domain.InboxMessage, error)
	Complete(ctx context.Context, message *domain.InboxMessage) error
	FindByConsumerAndMessageID(
		ctx context.Context,
		consumerName string,
		messageID string,
	) (*domain.InboxMessage, error)
}

type OutboxRepository interface {
	Create(ctx context.Context, event *domain.OutboxEvent) error
	FindNextDueForUpdate(ctx context.Context, dueAt time.Time) (*domain.OutboxEvent, error)
	UpdateDelivery(ctx context.Context, event *domain.OutboxEvent) error
}
