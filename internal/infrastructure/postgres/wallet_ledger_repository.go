package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type WalletLedgerRepository struct {
	db DBTX
}

func NewWalletLedgerRepository(db DBTX) *WalletLedgerRepository {
	return &WalletLedgerRepository{db: db}
}

func (r *WalletLedgerRepository) Create(
	ctx context.Context,
	entry *domain.WalletLedgerEntry,
) error {
	if entry == nil {
		return fmt.Errorf("create wallet ledger entry: entry is required")
	}

	const query = `
INSERT INTO wallet_ledger_entries (
    id,
    wallet_id,
    transaction_id,
    direction,
    money_amount,
    currency,
    balance_before,
    balance_after,
    created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	money := entry.Money()
	balanceBefore := entry.BalanceBefore()
	balanceAfter := entry.BalanceAfter()

	if _, err := r.db.Exec(
		ctx,
		query,
		entry.ID(),
		entry.WalletID(),
		entry.TransactionID(),
		entry.Direction(),
		money.Amount(),
		money.Currency(),
		balanceBefore.Amount(),
		balanceAfter.Amount(),
		entry.CreatedAt(),
	); err != nil {
		return fmt.Errorf("create wallet ledger entry %q: %w", entry.ID(), classifyDatabaseError(err))
	}

	return nil
}

var _ application.WalletLedgerRepository = (*WalletLedgerRepository)(nil)

func (r *WalletLedgerRepository) ListByWallet(ctx context.Context, walletID string, after *application.LedgerPosition, limit int) ([]domain.WalletLedgerEntry, error) {
	query := `SELECT id::text, wallet_id::text, transaction_id::text, direction,
                     money_amount, currency, balance_before, balance_after, created_at
              FROM wallet_ledger_entries WHERE wallet_id = $1`
	args := []any{walletID}
	if after != nil {
		query += " AND (created_at, id) > ($2, $3::uuid)"
		args = append(args, after.CreatedAt, after.ID)
	}
	query += fmt.Sprintf(" ORDER BY created_at, id LIMIT $%d", len(args)+1)
	args = append(args, limit)
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, wrapQueryError("list wallet ledger", err)
	}
	defer rows.Close()
	entries := make([]domain.WalletLedgerEntry, 0, limit)
	for rows.Next() {
		var id, wallet, transaction, direction, currency string
		var amount, before, after int64
		var created time.Time
		if err := rows.Scan(&id, &wallet, &transaction, &direction, &amount, &currency, &before, &after, &created); err != nil {
			return nil, fmt.Errorf("scan ledger entry: %w", err)
		}
		money, err := domain.NewMoney(amount, currency)
		if err != nil {
			return nil, err
		}
		balanceBefore, err := domain.NewMoney(before, currency)
		if err != nil {
			return nil, err
		}
		balanceAfter, err := domain.NewMoney(after, currency)
		if err != nil {
			return nil, err
		}
		entry, err := domain.RehydrateWalletLedgerEntry(domain.RehydrateWalletLedgerEntryParams{
			ID: id, WalletID: wallet, TransactionID: transaction, Direction: domain.LedgerDirection(direction),
			Money: money, BalanceBefore: balanceBefore, BalanceAfter: balanceAfter, CreatedAt: created.UTC(),
		})
		if err != nil {
			return nil, fmt.Errorf("rehydrate ledger entry: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapQueryError("iterate wallet ledger", err)
	}
	return entries, nil
}
