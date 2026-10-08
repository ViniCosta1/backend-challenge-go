package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

// RetryPendingReferences performs one bounded polling pass. Scheduling calls
// belongs to the runtime; all progress, attempts and deadlines live in PostgreSQL.
type RetryPendingReferences struct {
	processor *ProcessWagerTransaction
}

func NewRetryPendingReferences(transactions TransactionManager, generateID IDGenerator) *RetryPendingReferences {
	return &RetryPendingReferences{processor: NewProcessWagerTransaction(transactions, generateID)}
}

// Execute claims at most limit due operations, one short SQL transaction each.
// The supplied cutoff allows deterministic polling and backoff tests.
func (u *RetryPendingReferences) Execute(ctx context.Context, now time.Time, limit int) (int, error) {
	if now.IsZero() || limit <= 0 {
		return 0, fmt.Errorf("pending reference cutoff and positive limit are required")
	}
	now = now.UTC()
	attempted := 0
	for attempted < limit {
		found := false
		err := u.processor.transactions.WithinTransaction(ctx, func(repositories TransactionRepositories) error {
			wager, err := repositories.WagerTransactions().FindNextPendingReferenceForUpdate(ctx, now)
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			found = true
			if err := wager.RegisterReferenceAttempt(); err != nil {
				return err
			}
			wallet, err := repositories.Wallets().FindByIDForUpdate(ctx, wager.WalletID())
			if err != nil {
				return err
			}
			if wallet.PlayerID() != wager.PlayerID() || wallet.Currency() != wager.Money().Currency() {
				_, err = u.processor.rejectWager(ctx, repositories, wager, wallet.Balance(), domain.FailureCodeReferenceMismatch)
				return err
			}
			_, err = u.processor.processReferencedWager(ctx, repositories, wallet, wager, now)
			return err
		})
		if err != nil {
			return attempted, fmt.Errorf("retry pending reference: %w", err)
		}
		if !found {
			break
		}
		attempted++
	}
	return attempted, nil
}
