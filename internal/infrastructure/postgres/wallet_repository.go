package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

const selectWallet = `
SELECT
    id::text,
    player_id::text,
    currency,
    balance_amount,
    version,
    created_at,
    updated_at
FROM wallets`

type WalletRepository struct {
	db DBTX
}

func NewWalletRepository(db DBTX) *WalletRepository {
	return &WalletRepository{db: db}
}

func (r *WalletRepository) Create(
	ctx context.Context,
	wallet *domain.Wallet,
) error {
	if wallet == nil {
		return fmt.Errorf("create wallet: wallet is required")
	}

	const query = `
INSERT INTO wallets (
    id,
    player_id,
    currency,
    balance_amount,
    version,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)`

	balance := wallet.Balance()
	if _, err := r.db.Exec(
		ctx,
		query,
		wallet.ID(),
		wallet.PlayerID(),
		wallet.Currency(),
		balance.Amount(),
		wallet.Version(),
		wallet.CreatedAt(),
		wallet.UpdatedAt(),
	); err != nil {
		return mapWalletCreateError(wallet.ID(), err)
	}

	return nil
}

func mapWalletCreateError(walletID string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		postgresError.Code == "23505" &&
		postgresError.ConstraintName == "uq_wallets_player_currency" {
		return fmt.Errorf(
			"create wallet %q: %w",
			walletID,
			application.ErrWalletAlreadyExists,
		)
	}

	return fmt.Errorf("create wallet %q: %w", walletID, classifyDatabaseError(err))
}

func (r *WalletRepository) FindByID(
	ctx context.Context,
	id string,
) (*domain.Wallet, error) {
	return scanWallet(
		r.db.QueryRow(ctx, selectWallet+" WHERE id = $1", id),
		fmt.Sprintf("find wallet %q", id),
	)
}

func (r *WalletRepository) FindByIDForUpdate(
	ctx context.Context,
	id string,
) (*domain.Wallet, error) {
	return scanWallet(
		r.db.QueryRow(ctx, selectWallet+" WHERE id = $1 FOR UPDATE", id),
		fmt.Sprintf("find wallet %q for update", id),
	)
}

func (r *WalletRepository) Update(
	ctx context.Context,
	wallet *domain.Wallet,
) error {
	if wallet == nil {
		return fmt.Errorf("update wallet: wallet is required")
	}

	const query = `
UPDATE wallets
SET
    balance_amount = $2,
    version = $3,
    updated_at = $4
WHERE id = $1`

	balance := wallet.Balance()
	commandTag, err := r.db.Exec(
		ctx,
		query,
		wallet.ID(),
		balance.Amount(),
		wallet.Version(),
		wallet.UpdatedAt(),
	)
	if err != nil {
		return fmt.Errorf("update wallet %q: %w", wallet.ID(), classifyDatabaseError(err))
	}
	if commandTag.RowsAffected() != 1 {
		return fmt.Errorf("update wallet %q: %w", wallet.ID(), application.ErrNotFound)
	}

	return nil
}

func scanWallet(row rowScanner, resource string) (*domain.Wallet, error) {
	var (
		id            string
		playerID      string
		currency      string
		balanceAmount int64
		version       int64
		createdAt     time.Time
		updatedAt     time.Time
	)

	if err := row.Scan(
		&id,
		&playerID,
		&currency,
		&balanceAmount,
		&version,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, wrapQueryError(resource, err)
	}

	balance, err := domain.NewMoney(balanceAmount, currency)
	if err != nil {
		return nil, fmt.Errorf("%s: map balance: %w", resource, err)
	}

	wallet, err := domain.RehydrateWallet(
		id,
		playerID,
		currency,
		balance,
		version,
		createdAt.UTC(),
		updatedAt.UTC(),
	)
	if err != nil {
		return nil, fmt.Errorf("%s: rehydrate: %w", resource, err)
	}

	return &wallet, nil
}

var _ application.WalletRepository = (*WalletRepository)(nil)

func (r *WalletRepository) ReconciliationSnapshot(ctx context.Context, id string) (application.ReconciliationSnapshot, error) {
	// NUMERIC prevents aggregate overflow. It is converted back to int64
	// explicitly; no floating-point value crosses the persistence boundary.
	const query = `
SELECT w.id::text, w.currency, w.balance_amount,
       COALESCE(SUM(CASE l.direction
           WHEN 'CREDIT' THEN l.money_amount::numeric
           ELSE -l.money_amount::numeric END), 0)::text,
       COUNT(l.id)
FROM wallets w
LEFT JOIN wallet_ledger_entries l ON l.wallet_id = w.id
WHERE w.id = $1
GROUP BY w.id`
	var result application.ReconciliationSnapshot
	var currency, calculatedText string
	var stored int64
	if err := r.db.QueryRow(ctx, query, id).Scan(&result.WalletID, &currency, &stored, &calculatedText, &result.CheckedEntries); err != nil {
		return result, wrapQueryError("reconcile wallet", err)
	}
	calculated, err := strconv.ParseInt(calculatedText, 10, 64)
	if err != nil {
		return result, fmt.Errorf("reconciliation amount exceeds int64: %w", err)
	}
	result.StoredBalance, err = domain.NewMoney(stored, currency)
	if err != nil {
		return result, err
	}
	result.CalculatedBalance, err = signedMoney(calculated, currency)
	return result, err
}

// Negative ledger totals are valid diagnostic results, even though a wallet
// balance cannot be negative. Handle MinInt64 without negating it directly.
func signedMoney(amount int64, currency string) (domain.Money, error) {
	if amount >= 0 {
		return domain.NewMoney(amount, currency)
	}
	positive, err := domain.NewMoney(-(amount + 1), currency)
	if err != nil {
		return domain.Money{}, err
	}
	negative, err := positive.Negate()
	if err != nil {
		return domain.Money{}, err
	}
	one, err := domain.NewMoney(1, currency)
	if err != nil {
		return domain.Money{}, err
	}
	return negative.Subtract(one)
}
