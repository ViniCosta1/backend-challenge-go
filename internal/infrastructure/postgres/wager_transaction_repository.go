package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

const selectWagerTransaction = `
SELECT
    id::text,
    provider_id,
    external_transaction_id,
    idempotency_key,
    payload_hash,
    wallet_id::text,
    player_id::text,
    round_id,
    game_id,
    kind,
    money_amount,
    currency,
    reference_external_transaction_id,
    reference_transaction_id::text,
    status,
    failure_code,
    result_balance_amount,
    result_balance_currency,
    reference_attempts,
    reference_next_attempt_at,
    created_at,
    updated_at
FROM wager_transactions`

type WagerTransactionRepository struct {
	db DBTX
}

func NewWagerTransactionRepository(db DBTX) *WagerTransactionRepository {
	return &WagerTransactionRepository{db: db}
}

func (r *WagerTransactionRepository) Create(
	ctx context.Context,
	wager *domain.WagerTransaction,
) error {
	if wager == nil {
		return fmt.Errorf("create wager transaction: transaction is required")
	}

	commandTag, err := r.insert(ctx, wager, "")
	if err != nil {
		return fmt.Errorf("create wager transaction %q: %w", wager.ID(), classifyDatabaseError(err))
	}
	if commandTag.RowsAffected() != 1 {
		return fmt.Errorf("create wager transaction %q: no row inserted", wager.ID())
	}

	return nil
}

func (r *WagerTransactionRepository) CreateIfAbsent(
	ctx context.Context,
	wager *domain.WagerTransaction,
) (bool, error) {
	if wager == nil {
		return false, fmt.Errorf("reserve wager transaction: transaction is required")
	}

	commandTag, err := r.insert(ctx, wager, " ON CONFLICT DO NOTHING")
	if err != nil {
		return false, fmt.Errorf("reserve wager transaction %q: %w", wager.ID(), classifyDatabaseError(err))
	}

	return commandTag.RowsAffected() == 1, nil
}

func (r *WagerTransactionRepository) insert(
	ctx context.Context,
	wager *domain.WagerTransaction,
	conflictClause string,
) (pgconn.CommandTag, error) {
	if wager == nil {
		return pgconn.CommandTag{}, fmt.Errorf("wager transaction is required")
	}

	query := `
INSERT INTO wager_transactions (
    id,
    provider_id,
    external_transaction_id,
    idempotency_key,
    payload_hash,
    wallet_id,
    player_id,
    round_id,
    game_id,
    kind,
    money_amount,
    currency,
    reference_external_transaction_id,
    reference_transaction_id,
    status,
    failure_code,
    result_balance_amount,
    result_balance_currency,
    reference_attempts,
    reference_next_attempt_at,
    created_at,
    updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
)` + conflictClause

	money := wager.Money()
	resultBalance, hasResultBalance := wager.ResultBalance()
	nextAttempt, hasNextAttempt := wager.ReferenceNextAttemptAt()

	var resultBalanceAmount any
	var resultBalanceCurrency any
	if hasResultBalance {
		resultBalanceAmount = resultBalance.Amount()
		resultBalanceCurrency = resultBalance.Currency()
	}

	return r.db.Exec(
		ctx,
		query,
		wager.ID(),
		nullableString(wager.ProviderID()),
		nullableString(wager.ExternalTransactionID()),
		nullableString(wager.IdempotencyKey()),
		nullableString(wager.PayloadHash()),
		wager.WalletID(),
		wager.PlayerID(),
		nullableString(wager.RoundID()),
		nullableString(wager.GameID()),
		wager.Kind(),
		money.Amount(),
		money.Currency(),
		nullableString(wager.ReferenceExternalTransactionID()),
		nullableString(wager.ReferenceTransactionID()),
		wager.Status(),
		nullableString(string(wager.FailureCode())),
		resultBalanceAmount,
		resultBalanceCurrency,
		wager.ReferenceAttempts(),
		nullableTime(nextAttempt, hasNextAttempt),
		wager.CreatedAt(),
		wager.UpdatedAt(),
	)
}

func (r *WagerTransactionRepository) FindByID(
	ctx context.Context,
	id string,
) (*domain.WagerTransaction, error) {
	return scanWagerTransaction(
		r.db.QueryRow(ctx, selectWagerTransaction+" WHERE id = $1", id),
		fmt.Sprintf("find wager transaction %q", id),
	)
}

func (r *WagerTransactionRepository) FindByProviderAndIdempotencyKey(
	ctx context.Context,
	providerID string,
	idempotencyKey string,
) (*domain.WagerTransaction, error) {
	return scanWagerTransaction(
		r.db.QueryRow(
			ctx,
			selectWagerTransaction+
				" WHERE provider_id = $1 AND idempotency_key = $2",
			providerID,
			idempotencyKey,
		),
		fmt.Sprintf(
			"find wager transaction by provider %q and idempotency key %q",
			providerID,
			idempotencyKey,
		),
	)
}

func (r *WagerTransactionRepository) FindByIDAndProvider(ctx context.Context, id, providerID string) (*domain.WagerTransaction, error) {
	return scanWagerTransaction(r.db.QueryRow(ctx, selectWagerTransaction+" WHERE id = $1 AND provider_id = $2", id, providerID), "find provider wager transaction")
}

func (r *WagerTransactionRepository) FindByProviderAndExternalTransactionID(
	ctx context.Context,
	providerID string,
	externalTransactionID string,
) (*domain.WagerTransaction, error) {
	return scanWagerTransaction(
		r.db.QueryRow(
			ctx,
			selectWagerTransaction+
				" WHERE provider_id = $1 AND external_transaction_id = $2",
			providerID,
			externalTransactionID,
		),
		fmt.Sprintf(
			"find wager transaction by provider %q and external id %q",
			providerID,
			externalTransactionID,
		),
	)
}

func (r *WagerTransactionRepository) Update(
	ctx context.Context,
	wager *domain.WagerTransaction,
) error {
	if wager == nil {
		return fmt.Errorf("update wager transaction: transaction is required")
	}

	// The unique index is the arbiter of financial reversals. A savepoint
	// contains a unique violation so application can persist a rejection in
	// this same transaction. No financial data has been updated yet.
	guarded := wager.Status() == domain.WagerStatusProcessed &&
		(wager.Kind() == domain.WagerKindRefund || wager.Kind() == domain.WagerKindRollback)
	if guarded {
		if _, ok := r.db.(pgx.Tx); !ok {
			return fmt.Errorf("processed reversal update requires a transaction-bound repository")
		}
		if _, err := r.db.Exec(ctx, "SAVEPOINT wager_reversal_update"); err != nil {
			return fmt.Errorf("savepoint reversal update: %w", err)
		}
	}

	const query = `
UPDATE wager_transactions
SET
    status = $2,
    failure_code = $3,
    result_balance_amount = $4,
    result_balance_currency = $5,
    updated_at = $6,
    reference_transaction_id = $7,
    reference_attempts = $8,
    reference_next_attempt_at = $9
WHERE id = $1`

	resultBalance, hasResultBalance := wager.ResultBalance()
	nextAttempt, hasNextAttempt := wager.ReferenceNextAttemptAt()
	var resultBalanceAmount any
	var resultBalanceCurrency any
	if hasResultBalance {
		resultBalanceAmount = resultBalance.Amount()
		resultBalanceCurrency = resultBalance.Currency()
	}

	commandTag, err := r.db.Exec(
		ctx,
		query,
		wager.ID(),
		wager.Status(),
		nullableString(string(wager.FailureCode())),
		resultBalanceAmount,
		resultBalanceCurrency,
		wager.UpdatedAt(),
		nullableString(wager.ReferenceTransactionID()),
		wager.ReferenceAttempts(),
		nullableTime(nextAttempt, hasNextAttempt),
	)
	if guarded {
		if err != nil {
			if _, rollbackErr := r.db.Exec(ctx, "ROLLBACK TO SAVEPOINT wager_reversal_update"); rollbackErr != nil {
				return fmt.Errorf("rollback reversal savepoint: %w (update error: %v)", rollbackErr, err)
			}
		}
		if _, releaseErr := r.db.Exec(ctx, "RELEASE SAVEPOINT wager_reversal_update"); releaseErr != nil {
			return fmt.Errorf("release reversal savepoint: %w", releaseErr)
		}
	}
	if err != nil {
		var pgError *pgconn.PgError
		if errors.As(err, &pgError) && pgError.Code == "23505" &&
			pgError.ConstraintName == "ux_wager_transactions_processed_reversal_reference" {
			return fmt.Errorf("update reversal %q: %w", wager.ID(), application.ErrReversalAlreadyProcessed)
		}
		return fmt.Errorf("update wager transaction %q: %w", wager.ID(), classifyDatabaseError(err))
	}
	if commandTag.RowsAffected() != 1 {
		return fmt.Errorf(
			"update wager transaction %q: %w",
			wager.ID(),
			application.ErrNotFound,
		)
	}

	return nil
}

func (r *WagerTransactionRepository) FindNextPendingReferenceForUpdate(
	ctx context.Context,
	dueAt time.Time,
) (*domain.WagerTransaction, error) {
	return scanWagerTransaction(
		r.db.QueryRow(ctx, selectWagerTransaction+`
WHERE status = 'PENDING_REFERENCE' AND reference_next_attempt_at <= $1
ORDER BY reference_next_attempt_at, id
LIMIT 1 FOR UPDATE SKIP LOCKED`, dueAt),
		"find due pending reference for update",
	)
}

func scanWagerTransaction(
	row rowScanner,
	resource string,
) (*domain.WagerTransaction, error) {
	var (
		id                             string
		providerID                     pgtype.Text
		externalTransactionID          pgtype.Text
		idempotencyKey                 pgtype.Text
		payloadHash                    pgtype.Text
		walletID                       string
		playerID                       string
		roundID                        pgtype.Text
		gameID                         pgtype.Text
		kind                           string
		moneyAmount                    int64
		currency                       string
		referenceExternalTransactionID pgtype.Text
		referenceTransactionID         pgtype.Text
		status                         string
		failureCode                    pgtype.Text
		resultBalanceAmount            pgtype.Int8
		resultBalanceCurrency          pgtype.Text
		referenceAttempts              int32
		referenceNextAttemptAt         pgtype.Timestamptz
		createdAt                      time.Time
		updatedAt                      time.Time
	)

	if err := row.Scan(
		&id,
		&providerID,
		&externalTransactionID,
		&idempotencyKey,
		&payloadHash,
		&walletID,
		&playerID,
		&roundID,
		&gameID,
		&kind,
		&moneyAmount,
		&currency,
		&referenceExternalTransactionID,
		&referenceTransactionID,
		&status,
		&failureCode,
		&resultBalanceAmount,
		&resultBalanceCurrency,
		&referenceAttempts,
		&referenceNextAttemptAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return nil, wrapQueryError(resource, err)
	}

	money, err := domain.NewMoney(moneyAmount, currency)
	if err != nil {
		return nil, fmt.Errorf("%s: map money: %w", resource, err)
	}

	resultBalance, err := mapWagerResultBalance(
		resultBalanceAmount,
		resultBalanceCurrency,
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", resource, err)
	}
	var nextAttempt *time.Time
	if referenceNextAttemptAt.Valid {
		utc := referenceNextAttemptAt.Time.UTC()
		nextAttempt = &utc
	}

	wager, err := domain.RehydrateWagerTransaction(
		domain.RehydrateWagerTransactionParams{
			ID:                             id,
			ProviderID:                     textValue(providerID),
			ExternalTransactionID:          textValue(externalTransactionID),
			IdempotencyKey:                 textValue(idempotencyKey),
			PayloadHash:                    textValue(payloadHash),
			WalletID:                       walletID,
			PlayerID:                       playerID,
			RoundID:                        textValue(roundID),
			GameID:                         textValue(gameID),
			Kind:                           domain.WagerKind(kind),
			Money:                          money,
			ReferenceExternalTransactionID: textValue(referenceExternalTransactionID),
			ReferenceTransactionID:         textValue(referenceTransactionID),
			Status:                         domain.WagerStatus(status),
			FailureCode:                    domain.FailureCode(textValue(failureCode)),
			ResultBalance:                  resultBalance,
			ReferenceAttempts:              referenceAttempts,
			ReferenceNextAttemptAt:         nextAttempt,
			CreatedAt:                      createdAt.UTC(),
			UpdatedAt:                      updatedAt.UTC(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%s: rehydrate: %w", resource, err)
	}

	return &wager, nil
}

func mapWagerResultBalance(
	amount pgtype.Int8,
	currency pgtype.Text,
) (*domain.Money, error) {
	if amount.Valid != currency.Valid {
		return nil, fmt.Errorf("result balance amount and currency must both be set")
	}
	if !amount.Valid {
		return nil, nil
	}

	resultBalance, err := domain.NewMoney(amount.Int64, currency.String)
	if err != nil {
		return nil, fmt.Errorf("map result balance: %w", err)
	}

	return &resultBalance, nil
}

var _ application.WagerTransactionRepository = (*WagerTransactionRepository)(nil)
