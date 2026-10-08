package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
)

type TransactionManager struct {
	pool *pgxpool.Pool
}

func NewTransactionManager(pool *pgxpool.Pool) *TransactionManager {
	return &TransactionManager{pool: pool}
}

func (m *TransactionManager) WithinTransaction(
	ctx context.Context,
	fn func(application.TransactionRepositories) error,
) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", classifyDatabaseError(err))
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(NewRepositorySet(tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", classifyDatabaseError(err))
	}

	return nil
}

var _ application.TransactionManager = (*TransactionManager)(nil)
