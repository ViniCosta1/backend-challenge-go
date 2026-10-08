package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func TestPostgresRepositoriesShareTransaction(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	transactionManager := NewTransactionManager(pool)
	walletID := newTestUUID(t)
	playerID := newTestUUID(t)
	openingID := newTestUUID(t)
	ledgerID := newTestUUID(t)
	wagerID := newTestUUID(t)
	eventID := newTestUUID(t)
	providerID := "provider-" + wagerID
	externalTransactionID := "external-" + wagerID
	idempotencyKey := "idempotency-" + wagerID
	messageID := "message-" + wagerID

	err = transactionManager.WithinTransaction(
		ctx,
		func(repositories application.TransactionRepositories) error {
			assertRepositoriesShareDBTX(t, repositories)

			balance := mustMoney(t, 10000)
			wallet, err := domain.NewWallet(walletID, playerID, balance)
			if err != nil {
				return err
			}
			if err := repositories.Wallets().Create(ctx, &wallet); err != nil {
				return err
			}

			opening, err := domain.NewOpeningWagerTransaction(
				openingID,
				walletID,
				playerID,
				balance,
			)
			if err != nil {
				return err
			}
			if err := repositories.WagerTransactions().Create(ctx, &opening); err != nil {
				return err
			}

			zero := mustMoney(t, 0)
			entry, err := domain.NewWalletLedgerEntry(
				domain.NewWalletLedgerEntryParams{
					ID:            ledgerID,
					WalletID:      walletID,
					TransactionID: openingID,
					Direction:     domain.LedgerDirectionCredit,
					Money:         balance,
					BalanceBefore: zero,
					BalanceAfter:  balance,
				},
			)
			if err != nil {
				return err
			}
			if err := repositories.Ledger().Create(ctx, &entry); err != nil {
				return err
			}

			wager, err := domain.NewWagerTransaction(
				domain.NewWagerTransactionParams{
					ID:                    wagerID,
					ProviderID:            providerID,
					ExternalTransactionID: externalTransactionID,
					IdempotencyKey:        idempotencyKey,
					PayloadHash:           "payload-hash",
					WalletID:              walletID,
					PlayerID:              playerID,
					RoundID:               "round-1",
					GameID:                "game-1",
					Kind:                  domain.WagerKindBet,
					Money:                 mustMoney(t, 2500),
				},
			)
			if err != nil {
				return err
			}
			resultBalance := mustMoney(t, 7500)
			if err := wager.MarkProcessed(resultBalance); err != nil {
				return err
			}
			if err := repositories.WagerTransactions().Create(ctx, &wager); err != nil {
				return err
			}

			byKey, err := repositories.WagerTransactions().
				FindByProviderAndIdempotencyKey(ctx, providerID, idempotencyKey)
			if err != nil {
				return err
			}
			byExternalID, err := repositories.WagerTransactions().
				FindByProviderAndExternalTransactionID(
					ctx,
					providerID,
					externalTransactionID,
				)
			if err != nil {
				return err
			}
			if byKey.ID() != wagerID || byExternalID.ID() != wagerID {
				t.Fatal("wager identity queries returned another transaction")
			}
			persistedResult, ok := byKey.ResultBalance()
			if !ok || persistedResult.Amount() != resultBalance.Amount() {
				t.Fatal("wager result balance was not preserved")
			}

			inbox, err := domain.NewInboxMessage(
				"wager-consumer",
				messageID,
				"payload-hash",
			)
			if err != nil {
				return err
			}
			if err := inbox.MarkCompleted(); err != nil {
				return err
			}
			if err := repositories.Inbox().Create(ctx, &inbox); err != nil {
				return err
			}

			integrationEvent, err := domain.NewWalletBalanceChanged(
				domain.NewWalletBalanceChangedParams{
					EventID:       eventID,
					CorrelationID: "correlation-1",
					WalletID:      walletID,
					TransactionID: openingID,
					Direction:     domain.LedgerDirectionCredit,
					Money:         balance,
					BalanceBefore: zero,
					BalanceAfter:  balance,
					WalletVersion: 1,
				},
			)
			if err != nil {
				return err
			}
			outbox, err := domain.NewOutboxEvent(
				integrationEvent.Envelope(),
				[]byte(fmt.Sprintf(`{"walletId":%q}`, walletID)),
			)
			if err != nil {
				return err
			}

			return repositories.Outbox().Create(ctx, &outbox)
		},
	)
	if err != nil {
		t.Fatalf("commit repository transaction: %v", err)
	}

	repositories := NewRepositorySet(pool)
	wallet, err := repositories.Wallets().FindByID(ctx, walletID)
	if err != nil {
		t.Fatalf("find committed wallet: %v", err)
	}
	if wallet.ID() != walletID || wallet.Balance().Amount() != 10000 {
		t.Fatal("wallet did not preserve its persisted state")
	}

	inbox, err := repositories.Inbox().FindByConsumerAndMessageID(
		ctx,
		"wager-consumer",
		messageID,
	)
	if err != nil {
		t.Fatalf("find committed inbox message: %v", err)
	}
	if !inbox.IsCompleted() {
		t.Fatal("inbox completion was not persisted")
	}

	var outboxCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM outbox_events WHERE event_id = $1",
		eventID,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("query committed outbox event: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("expected one outbox event, got %d", outboxCount)
	}
}

func TestPostgresTransactionManagerRollsBackCallbackError(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	walletID := newTestUUID(t)
	callbackErr := errors.New("abort transaction")
	err = NewTransactionManager(pool).WithinTransaction(
		ctx,
		func(repositories application.TransactionRepositories) error {
			wallet, err := domain.NewWallet(
				walletID,
				newTestUUID(t),
				mustMoney(t, 0),
			)
			if err != nil {
				return err
			}
			if err := repositories.Wallets().Create(ctx, &wallet); err != nil {
				return err
			}

			return callbackErr
		},
	)
	if !errors.Is(err, callbackErr) {
		t.Fatalf("expected callback error, got %v", err)
	}

	_, err = NewRepositorySet(pool).Wallets().FindByID(ctx, walletID)
	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("expected rolled back wallet to be absent, got %v", err)
	}
}

func TestWalletRepositoryReadsBIGINTVersion(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(pool.Close)

	walletID := newTestUUID(t)
	version := int64(1) << 40
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO wallets (
             id, player_id, currency, balance_amount, version, created_at, updated_at
         ) VALUES ($1, $2, 'BRL', 0, $3, now(), now())`,
		walletID,
		newTestUUID(t),
		version,
	); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	wallet, err := NewWalletRepository(pool).FindByID(ctx, walletID)
	if err != nil {
		t.Fatalf("find wallet: %v", err)
	}
	if wallet.Version() != version {
		t.Fatalf("expected version %d, got %d", version, wallet.Version())
	}
}

func assertRepositoriesShareDBTX(
	t *testing.T,
	repositories application.TransactionRepositories,
) {
	t.Helper()

	walletDB := repositories.Wallets().(*WalletRepository).db
	if repositories.WagerTransactions().(*WagerTransactionRepository).db != walletDB ||
		repositories.Ledger().(*WalletLedgerRepository).db != walletDB ||
		repositories.Inbox().(*InboxRepository).db != walletDB ||
		repositories.Outbox().(*OutboxRepository).db != walletDB {
		t.Fatal("transaction repositories do not share the same DBTX")
	}
}

func mustMoney(t *testing.T, amount int64) domain.Money {
	t.Helper()

	money, err := domain.NewMoney(amount, "BRL")
	if err != nil {
		t.Fatalf("create money: %v", err)
	}

	return money
}

func newTestUUID(t *testing.T) string {
	t.Helper()

	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatalf("generate UUID: %v", err)
	}

	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80

	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	)
}
