package postgres

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func TestCreateWalletWithZeroBalancePersistsOnlyWallet(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID := newTestUUID(t)
	playerID := newTestUUID(t)

	useCase := application.NewCreateWallet(
		NewTransactionManager(pool),
		sequenceIDGenerator(t, walletID),
	)
	output, err := useCase.Execute(ctx, application.CreateWalletInput{
		PlayerID:       playerID,
		InitialBalance: mustMoney(t, 0),
	})
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}

	if output.WalletID != walletID ||
		output.PlayerID != playerID ||
		output.Balance.Amount() != 0 ||
		output.Version != 1 {
		t.Fatalf("unexpected output: %+v", output)
	}

	assertCount(t, ctx, pool, 1,
		"SELECT count(*) FROM wallets WHERE id = $1", walletID)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM wager_transactions WHERE wallet_id = $1", walletID)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id = $1", walletID)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM outbox_events WHERE correlation_id = $1", walletID)
}

func TestCreateWalletWithPositiveBalancePersistsOpeningAtomically(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID := newTestUUID(t)
	playerID := newTestUUID(t)
	openingID := newTestUUID(t)
	ledgerID := newTestUUID(t)
	processedEventID := newTestUUID(t)
	balanceChangedEventID := newTestUUID(t)

	useCase := application.NewCreateWallet(
		NewTransactionManager(pool),
		sequenceIDGenerator(
			t,
			walletID,
			openingID,
			ledgerID,
			processedEventID,
			balanceChangedEventID,
		),
	)
	output, err := useCase.Execute(ctx, application.CreateWalletInput{
		PlayerID:       playerID,
		InitialBalance: mustMoney(t, 10000),
	})
	if err != nil {
		t.Fatalf("create wallet: %v", err)
	}
	if output.WalletID != walletID ||
		output.Balance.Amount() != 10000 ||
		output.Version != 1 {
		t.Fatalf("unexpected output: %+v", output)
	}
	assertCount(t, ctx, pool, 1,
		`SELECT count(*) FROM wallets
         WHERE id = $1 AND balance_amount = 10000 AND version = 1`,
		walletID,
	)

	var (
		kind   string
		status string
		amount int64
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT kind, status, money_amount
         FROM wager_transactions
         WHERE id = $1 AND wallet_id = $2`,
		openingID,
		walletID,
	).Scan(&kind, &status, &amount); err != nil {
		t.Fatalf("query opening: %v", err)
	}
	if kind != string(domain.WagerKindOpening) ||
		status != string(domain.WagerStatusProcessed) ||
		amount != 10000 {
		t.Fatalf("unexpected opening: kind=%s status=%s amount=%d", kind, status, amount)
	}

	var (
		direction     string
		moneyAmount   int64
		balanceBefore int64
		balanceAfter  int64
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT direction, money_amount, balance_before, balance_after
         FROM wallet_ledger_entries
         WHERE id = $1 AND transaction_id = $2`,
		ledgerID,
		openingID,
	).Scan(
		&direction,
		&moneyAmount,
		&balanceBefore,
		&balanceAfter,
	); err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	if direction != string(domain.LedgerDirectionCredit) ||
		moneyAmount != 10000 ||
		balanceBefore != 0 ||
		balanceAfter != 10000 {
		t.Fatalf(
			"unexpected ledger: direction=%s money=%d before=%d after=%d",
			direction,
			moneyAmount,
			balanceBefore,
			balanceAfter,
		)
	}

	assertOutboxMoneyIsString(
		t,
		ctx,
		pool,
		processedEventID,
		"WagerTransactionProcessed",
		[]string{"data", "resultBalance", "amount"},
		"100.00",
	)
	assertOutboxMoneyIsString(
		t,
		ctx,
		pool,
		balanceChangedEventID,
		"WalletBalanceChanged",
		[]string{"data", "money", "amount"},
		"100.00",
	)
	assertCount(t, ctx, pool, 2,
		"SELECT count(*) FROM outbox_events WHERE correlation_id = $1", walletID)
}

func TestCreateWalletRollsBackWhenLatePersistenceFails(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID := newTestUUID(t)
	duplicateEventID := newTestUUID(t)

	useCase := application.NewCreateWallet(
		NewTransactionManager(pool),
		sequenceIDGenerator(
			t,
			walletID,
			newTestUUID(t),
			newTestUUID(t),
			duplicateEventID,
			duplicateEventID,
		),
	)
	_, err := useCase.Execute(ctx, application.CreateWalletInput{
		PlayerID:       newTestUUID(t),
		InitialBalance: mustMoney(t, 10000),
	})
	if err == nil {
		t.Fatal("expected persistence error")
	}

	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM wallets WHERE id = $1", walletID)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM wager_transactions WHERE wallet_id = $1", walletID)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id = $1", walletID)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM outbox_events WHERE correlation_id = $1", walletID)
}

func TestCreateWalletMapsPlayerCurrencyConflictWithoutPartialRecords(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	playerID := newTestUUID(t)
	firstWalletID := newTestUUID(t)
	secondWalletID := newTestUUID(t)

	firstUseCase := application.NewCreateWallet(
		NewTransactionManager(pool),
		sequenceIDGenerator(
			t,
			firstWalletID,
			newTestUUID(t),
			newTestUUID(t),
			newTestUUID(t),
			newTestUUID(t),
		),
	)
	if _, err := firstUseCase.Execute(ctx, application.CreateWalletInput{
		PlayerID:       playerID,
		InitialBalance: mustMoney(t, 10000),
	}); err != nil {
		t.Fatalf("create first wallet: %v", err)
	}

	secondUseCase := application.NewCreateWallet(
		NewTransactionManager(pool),
		sequenceIDGenerator(
			t,
			secondWalletID,
			newTestUUID(t),
			newTestUUID(t),
			newTestUUID(t),
			newTestUUID(t),
		),
	)
	_, err := secondUseCase.Execute(ctx, application.CreateWalletInput{
		PlayerID:       playerID,
		InitialBalance: mustMoney(t, 10000),
	})
	if !errors.Is(err, application.ErrWalletAlreadyExists) {
		t.Fatalf("expected ErrWalletAlreadyExists, got %v", err)
	}

	assertCount(t, ctx, pool, 1,
		"SELECT count(*) FROM wallets WHERE player_id = $1 AND currency = 'BRL'",
		playerID,
	)
	assertCount(t, ctx, pool, 1,
		"SELECT count(*) FROM wager_transactions WHERE player_id = $1 AND kind = 'OPENING'",
		playerID,
	)
	assertCount(t, ctx, pool, 1,
		`SELECT count(*)
         FROM wallet_ledger_entries ledger
         JOIN wallets wallet ON wallet.id = ledger.wallet_id
         WHERE wallet.player_id = $1`,
		playerID,
	)
	assertCount(t, ctx, pool, 2,
		"SELECT count(*) FROM outbox_events WHERE correlation_id = $1", firstWalletID)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM outbox_events WHERE correlation_id = $1", secondWalletID)
}

func createWalletTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()

	databaseURL := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	config.MaxConns = 64
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	return ctx, pool
}

func sequenceIDGenerator(t *testing.T, ids ...string) application.IDGenerator {
	t.Helper()

	next := 0
	return func() string {
		if next >= len(ids) {
			t.Fatalf("ID generator called more than %d times", len(ids))
		}

		id := ids[next]
		next++
		return id
	}
}

func assertCount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	want int,
	query string,
	args ...any,
) {
	t.Helper()

	var got int
	if err := pool.QueryRow(ctx, query, args...).Scan(&got); err != nil {
		t.Fatalf("query count: %v", err)
	}
	if got != want {
		t.Fatalf("expected count %d, got %d", want, got)
	}
}

func assertOutboxMoneyIsString(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	eventID string,
	wantEventType string,
	jsonPath []string,
	wantAmount string,
) {
	t.Helper()

	var (
		eventType string
		amount    string
		jsonType  string
	)
	if err := pool.QueryRow(
		ctx,
		`SELECT event_type, payload #>> $2, jsonb_typeof(payload #> $2)
         FROM outbox_events
         WHERE event_id = $1`,
		eventID,
		jsonPath,
	).Scan(&eventType, &amount, &jsonType); err != nil {
		t.Fatalf("query outbox payload: %v", err)
	}
	if eventType != wantEventType || amount != wantAmount || jsonType != "string" {
		t.Fatalf(
			"unexpected outbox payload: type=%s amount=%s jsonType=%s",
			eventType,
			amount,
			jsonType,
		)
	}
}
