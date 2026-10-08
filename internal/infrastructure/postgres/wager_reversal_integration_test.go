package postgres

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

// A schema per test keeps polling deterministic without deleting any records
// belonging to another test or the application. Each test applies real migrations.
func reversalTestPool(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	schema := "reversal_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean temporary test schema: %v", err)
		}
		admin.Close()
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 64
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, migration := range []string{
		"000001_init.up.sql", "000002_wager_result_balance.up.sql", "000003_pending_reference_state.up.sql",
	} {
		applyTestMigration(t, ctx, pool, migration)
	}
	return ctx, pool
}

func applyTestMigration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) {
	t.Helper()
	sql, err := os.ReadFile("../../database/migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(sql)); err != nil {
		t.Fatalf("apply migration %s: %v", name, err)
	}
}

func executeWager(t *testing.T, ctx context.Context, pool *pgxpool.Pool, input application.ProcessWagerTransactionInput) application.ProcessWagerTransactionOutput {
	t.Helper()
	output, err := newProcessWagerUseCase(pool).Execute(ctx, input)
	if err != nil {
		t.Fatalf("execute %s: %v", input.Kind, err)
	}
	return output
}

func reversalInput(t *testing.T, original application.ProcessWagerTransactionInput, kind domain.WagerKind) application.ProcessWagerTransactionInput {
	t.Helper()
	input := newWagerInput(t, original.WalletID, original.PlayerID, kind, original.Money.Amount())
	input.ProviderID = original.ProviderID
	input.RoundID = original.RoundID
	input.ReferenceExternalTransactionID = original.ExternalTransactionID
	return input
}

func assertRejection(t *testing.T, ctx context.Context, pool *pgxpool.Pool, output application.ProcessWagerTransactionOutput, code domain.FailureCode, balance int64) {
	t.Helper()
	assertWagerOutput(t, output, domain.WagerStatusRejected, balance, false)
	if !output.HasResultBalance || output.FailureCode != code {
		t.Fatalf("unexpected rejected result: %+v", output)
	}
	wager, err := NewWagerTransactionRepository(pool).FindByID(ctx, output.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, ok := wager.ResultBalance()
	if !ok || snapshot.Amount() != balance || wager.FailureCode() != code {
		t.Fatalf("rejection snapshot/code not persisted: balance=%v code=%s", snapshot, wager.FailureCode())
	}
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", output.TransactionID)
	assertOutboxTypes(t, ctx, pool, output.TransactionID, "WagerTransactionRejected")
}

func TestRejectedBETReplayPreservesOriginalBalance(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 2000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	first := executeWager(t, ctx, pool, bet)
	assertRejection(t, ctx, pool, first, domain.FailureCodeInsufficientBalance, 2000)
	executeWager(t, ctx, pool, newWagerInput(t, wallet, player, domain.WagerKindWin, 1000))
	replay := executeWager(t, ctx, pool, bet)
	assertWagerOutput(t, replay, domain.WagerStatusRejected, 2000, true)
	if replay.TransactionID != first.TransactionID || replay.FailureCode != first.FailureCode {
		t.Fatal("replay did not preserve the rejected result")
	}
	assertWalletState(t, ctx, pool, wallet, 3000, 2)
	assertOutboxTypes(t, ctx, pool, first.TransactionID, "WagerTransactionRejected")
}

func TestWINWithProcessedBETReferenceAllowsIndependentAmountAndReplay(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	executeWager(t, ctx, pool, bet)
	win := newWagerInput(t, wallet, player, domain.WagerKindWin, 1000)
	win.ProviderID = bet.ProviderID
	win.RoundID = bet.RoundID
	win.ReferenceExternalTransactionID = bet.ExternalTransactionID

	output := executeWager(t, ctx, pool, win)
	assertWagerOutput(t, output, domain.WagerStatusProcessed, 8500, false)
	assertLedger(t, ctx, pool, output.TransactionID, domain.LedgerDirectionCredit, 1000, 7500, 8500)
	assertWalletState(t, ctx, pool, wallet, 8500, 3)
	persisted, err := NewWagerTransactionRepository(pool).FindByID(ctx, output.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ReferenceTransactionID() == "" {
		t.Fatal("processed referenced WIN did not persist its internal reference")
	}
	replay := executeWager(t, ctx, pool, win)
	assertWagerOutput(t, replay, domain.WagerStatusProcessed, 8500, true)
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id=$1", output.TransactionID)
	refund := executeWager(t, ctx, pool, reversalInput(t, bet, domain.WagerKindRefund))
	assertWagerOutput(t, refund, domain.WagerStatusProcessed, 11000, false)
	assertWalletState(t, ctx, pool, wallet, 11000, 4)
}

func TestWINReferencePendingThenResolves(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	win := newWagerInput(t, wallet, player, domain.WagerKindWin, 1000)
	win.ProviderID = bet.ProviderID
	win.RoundID = bet.RoundID
	win.ReferenceExternalTransactionID = bet.ExternalTransactionID
	pending := executeWager(t, ctx, pool, win)
	if pending.Status != domain.WagerStatusPendingReference {
		t.Fatalf("expected pending WIN, got %+v", pending)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 1)
	executeWager(t, ctx, pool, bet)
	persisted, err := NewWagerTransactionRepository(pool).FindByID(ctx, pending.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := persisted.ReferenceNextAttemptAt()
	if count, err := application.NewRetryPendingReferences(NewTransactionManager(pool), nil).Execute(ctx, next, 1); err != nil || count != 1 {
		t.Fatalf("resolve pending WIN: count=%d err=%v", count, err)
	}
	assertWalletState(t, ctx, pool, wallet, 8500, 3)
	assertLedger(t, ctx, pool, pending.TransactionID, domain.LedgerDirectionCredit, 1000, 7500, 8500)
}

func TestWINReferenceRejectsIncompatibleOrMismatchedBET(t *testing.T) {
	for _, scenario := range []string{"wrong kind", "rejected BET", "player wallet", "round"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, pool := reversalTestPool(t)
			wallet, player := createFundedWallet(t, ctx, pool, 10000)
			reference := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
			if scenario == "wrong kind" {
				reference.Kind = domain.WagerKindWin
			}
			if scenario == "rejected BET" {
				reference.Money = mustMoney(t, 11000)
			}
			executeWager(t, ctx, pool, reference)

			winWallet, winPlayer := wallet, player
			if scenario == "player wallet" {
				winWallet, winPlayer = createFundedWallet(t, ctx, pool, 5000)
			}
			win := newWagerInput(t, winWallet, winPlayer, domain.WagerKindWin, 1000)
			win.ProviderID = reference.ProviderID
			win.RoundID = reference.RoundID
			if scenario == "round" {
				win.RoundID = "another-round"
			}
			win.ReferenceExternalTransactionID = reference.ExternalTransactionID
			output := executeWager(t, ctx, pool, win)
			code := domain.FailureCodeReferenceMismatch
			if scenario == "wrong kind" || scenario == "rejected BET" {
				code = domain.FailureCodeReferenceIncompatible
			}
			wantBalance := int64(7500)
			switch scenario {
			case "wrong kind":
				wantBalance = 12500
			case "rejected BET":
				wantBalance = 10000
			case "player wallet":
				wantBalance = 5000
			}
			assertRejection(t, ctx, pool, output, code, wantBalance)
		})
	}
}

func TestREFUNDAndReplayRestoreOriginalBalance(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	betResult := executeWager(t, ctx, pool, bet)
	refund := reversalInput(t, bet, domain.WagerKindRefund)
	output := executeWager(t, ctx, pool, refund)
	assertWagerOutput(t, output, domain.WagerStatusProcessed, 10000, false)
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
	assertLedger(t, ctx, pool, betResult.TransactionID, domain.LedgerDirectionDebit, 2500, 10000, 7500)
	assertLedger(t, ctx, pool, output.TransactionID, domain.LedgerDirectionCredit, 2500, 7500, 10000)
	assertOutboxTypes(t, ctx, pool, output.TransactionID, "WagerTransactionProcessed", "WalletBalanceChanged")
	executeWager(t, ctx, pool, newWagerInput(t, wallet, player, domain.WagerKindWin, 1000))
	replay := executeWager(t, ctx, pool, refund)
	assertWagerOutput(t, replay, domain.WagerStatusProcessed, 10000, true)
	if replay.TransactionID != output.TransactionID {
		t.Fatal("REFUND replay changed identity")
	}
	assertWalletState(t, ctx, pool, wallet, 11000, 4)
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", output.TransactionID)
}

func TestSecondREFUNDAndROLLBACKOfSameBETAreRejected(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	executeWager(t, ctx, pool, bet)
	executeWager(t, ctx, pool, reversalInput(t, bet, domain.WagerKindRefund))
	for _, kind := range []domain.WagerKind{domain.WagerKindRefund, domain.WagerKindRollback} {
		result := executeWager(t, ctx, pool, reversalInput(t, bet, kind))
		assertRejection(t, ctx, pool, result, domain.FailureCodeReferenceAlreadyReversed, 10000)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
}

func TestREFUNDRejectsMismatchedBusinessFields(t *testing.T) {
	for _, field := range []string{"value", "player", "wallet", "round"} {
		t.Run(field, func(t *testing.T) {
			ctx, pool := reversalTestPool(t)
			wallet, player := createFundedWallet(t, ctx, pool, 10000)
			bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
			executeWager(t, ctx, pool, bet)
			refund := reversalInput(t, bet, domain.WagerKindRefund)
			wantBalance := int64(7500)
			switch field {
			case "value":
				refund.Money = mustMoney(t, 2400)
			case "player":
				refund.PlayerID = newTestUUID(t)
			case "wallet":
				refund.WalletID, refund.PlayerID = createFundedWallet(t, ctx, pool, 5000)
				wantBalance = 5000
			case "round":
				refund.RoundID = "different-round"
			}
			result := executeWager(t, ctx, pool, refund)
			assertRejection(t, ctx, pool, result, domain.FailureCodeReferenceMismatch, wantBalance)
			assertWalletState(t, ctx, pool, wallet, 7500, 2)
		})
	}
}

func TestROLLBACKMovementsAndReplay(t *testing.T) {
	for _, kind := range []domain.WagerKind{domain.WagerKindBet, domain.WagerKindWin, domain.WagerKindRefund} {
		t.Run(string(kind), func(t *testing.T) {
			ctx, pool := reversalTestPool(t)
			wallet, player := createFundedWallet(t, ctx, pool, 10000)
			original := newWagerInput(t, wallet, player, kind, 2500)
			if kind == domain.WagerKindRefund {
				bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
				executeWager(t, ctx, pool, bet)
				original = reversalInput(t, bet, domain.WagerKindRefund)
			}
			executeWager(t, ctx, pool, original)
			rollback := reversalInput(t, original, domain.WagerKindRollback)
			output := executeWager(t, ctx, pool, rollback)
			before, after, direction, version := int64(12500), int64(10000), domain.LedgerDirectionDebit, int64(3)
			if kind == domain.WagerKindBet {
				before, direction = 7500, domain.LedgerDirectionCredit
			}
			if kind == domain.WagerKindRefund {
				before, after, version = 10000, 7500, 4
			}
			assertWagerOutput(t, output, domain.WagerStatusProcessed, after, false)
			assertLedger(t, ctx, pool, output.TransactionID, direction, 2500, before, after)
			assertOutboxTypes(t, ctx, pool, output.TransactionID, "WagerTransactionProcessed", "WalletBalanceChanged")
			replay := executeWager(t, ctx, pool, rollback)
			assertWagerOutput(t, replay, domain.WagerStatusProcessed, after, true)
			assertWalletState(t, ctx, pool, wallet, after, version)
			assertCount(t, ctx, pool, 1, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", output.TransactionID)
		})
	}
}

func TestROLLBACKRejectsInsufficientBalanceWithDistinctCode(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 0)
	win := newWagerInput(t, wallet, player, domain.WagerKindWin, 2500)
	executeWager(t, ctx, pool, win)
	executeWager(t, ctx, pool, newWagerInput(t, wallet, player, domain.WagerKindBet, 2500))
	rollback := reversalInput(t, win, domain.WagerKindRollback)
	output := executeWager(t, ctx, pool, rollback)
	assertRejection(t, ctx, pool, output, domain.FailureCodeRollbackInsufficientBalance, 0)
	assertWalletState(t, ctx, pool, wallet, 0, 3)
	if output.FailureCode == domain.FailureCodeInsufficientBalance {
		t.Fatal("ROLLBACK and BET insufficient balance codes must differ")
	}
	executeWager(t, ctx, pool, newWagerInput(t, wallet, player, domain.WagerKindWin, 1000))
	replay := executeWager(t, ctx, pool, rollback)
	assertWagerOutput(t, replay, domain.WagerStatusRejected, 0, true)
	assertWalletState(t, ctx, pool, wallet, 1000, 4)
}

func TestREFUNDBeforeBETIsDurableAndResolvedAfterRestart(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	refund := reversalInput(t, bet, domain.WagerKindRefund)
	output := executeWager(t, ctx, pool, refund)
	if output.Status != domain.WagerStatusPendingReference || output.HasResultBalance {
		t.Fatalf("expected a pending result without a financial snapshot: %+v", output)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 1)
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", output.TransactionID)
	assertOutboxTypes(t, ctx, pool, output.TransactionID, "WagerTransactionPendingReference")
	replay := executeWager(t, ctx, pool, refund)
	if !replay.IdempotentReplay || replay.Status != output.Status || replay.TransactionID != output.TransactionID {
		t.Fatalf("pending replay did not preserve state: %+v", replay)
	}
	assertOutboxTypes(t, ctx, pool, output.TransactionID, "WagerTransactionPendingReference")

	wager, err := NewWagerTransactionRepository(pool).FindByID(ctx, output.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	next, ok := wager.ReferenceNextAttemptAt()
	if !ok || wager.ReferenceAttempts() != 0 {
		t.Fatal("pending schedule not persisted")
	}
	worker := application.NewRetryPendingReferences(NewTransactionManager(pool), nil)
	if count, err := worker.Execute(ctx, next.Add(-time.Nanosecond), 10); err != nil || count != 0 {
		t.Fatalf("worker ran before due date: count=%d err=%v", count, err)
	}
	executeWager(t, ctx, pool, bet)

	// Independent pool and a newly constructed worker emulate losing all
	// application memory and resuming solely from persisted state.
	restartedPool, err := pgxpool.NewWithConfig(ctx, pool.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer restartedPool.Close()
	restartedWorker := application.NewRetryPendingReferences(NewTransactionManager(restartedPool), nil)
	if count, err := restartedWorker.Execute(ctx, next, 10); err != nil || count != 1 {
		t.Fatalf("retry did not process persisted pending: count=%d err=%v", count, err)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
	assertLedger(t, ctx, pool, output.TransactionID, domain.LedgerDirectionCredit, 2500, 7500, 10000)
	assertOutboxTypes(t, ctx, pool, output.TransactionID,
		"WagerTransactionPendingReference", "WagerTransactionProcessed", "WalletBalanceChanged")
	replay = executeWager(t, ctx, pool, refund)
	assertWagerOutput(t, replay, domain.WagerStatusProcessed, 10000, true)
}

func TestPendingReferenceExpiresAfterFiveAttempts(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	missing := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	output := executeWager(t, ctx, pool, reversalInput(t, missing, domain.WagerKindRefund))
	worker := application.NewRetryPendingReferences(NewTransactionManager(pool), nil)
	repository := NewWagerTransactionRepository(pool)
	for attempt := int32(1); attempt <= application.MaxReferenceAttempts; attempt++ {
		wager, err := repository.FindByID(ctx, output.TransactionID)
		if err != nil {
			t.Fatal(err)
		}
		next, _ := wager.ReferenceNextAttemptAt()
		if count, err := worker.Execute(ctx, next, 10); err != nil || count != 1 {
			t.Fatalf("retry %d: count=%d err=%v", attempt, count, err)
		}
		persisted, err := repository.FindByID(ctx, output.TransactionID)
		if err != nil {
			t.Fatal(err)
		}
		if persisted.ReferenceAttempts() != attempt {
			t.Fatalf("attempt count not preserved: %d", persisted.ReferenceAttempts())
		}
		if attempt < application.MaxReferenceAttempts {
			future, ok := persisted.ReferenceNextAttemptAt()
			want := next.Add(application.ReferenceRetryBaseDelay * time.Duration(1<<uint(attempt)))
			if !ok || !future.Equal(want) || persisted.Status() != domain.WagerStatusPendingReference {
				t.Fatalf("backoff %d: got=%v want=%v", attempt, future, want)
			}
		} else if persisted.Status() != domain.WagerStatusRejected ||
			persisted.FailureCode() != domain.FailureCodeReferenceNotFound {
			t.Fatal("missing reference did not become a definitive rejection")
		}
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 1)
	assertOutboxTypes(t, ctx, pool, output.TransactionID,
		"WagerTransactionPendingReference", "WagerTransactionRejected")
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", output.TransactionID)
}

func TestREFUNDRejectsTerminalUnusableReference(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 2000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	executeWager(t, ctx, pool, bet)
	output := executeWager(t, ctx, pool, reversalInput(t, bet, domain.WagerKindRefund))
	assertRejection(t, ctx, pool, output, domain.FailureCodeReferenceIncompatible, 2000)
	assertWalletState(t, ctx, pool, wallet, 2000, 1)
}

func TestREFUNDWaitsForNonTerminalReference(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	reference, err := domain.NewWagerTransaction(domain.NewWagerTransactionParams{
		ID: uuid.NewString(), ProviderID: bet.ProviderID, ExternalTransactionID: bet.ExternalTransactionID,
		IdempotencyKey: bet.IdempotencyKey, PayloadHash: strings.Repeat("a", 64),
		WalletID: wallet, PlayerID: player, RoundID: bet.RoundID, GameID: bet.GameID,
		Kind: bet.Kind, Money: bet.Money,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewWagerTransactionRepository(pool).Create(ctx, &reference); err != nil {
		t.Fatal(err)
	}
	output := executeWager(t, ctx, pool, reversalInput(t, bet, domain.WagerKindRefund))
	if output.Status != domain.WagerStatusPendingReference {
		t.Fatalf("nonterminal reference was not deferred: %+v", output)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 1)
}

func TestConcurrentREFUNDAndROLLBACKHaveOnlyOneFinancialEffect(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	executeWager(t, ctx, pool, bet)
	results := runConcurrentWagers(t, ctx, pool, []application.ProcessWagerTransactionInput{
		reversalInput(t, bet, domain.WagerKindRefund), reversalInput(t, bet, domain.WagerKindRollback),
	})
	processed, rejected := 0, 0
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("concurrent reversal: %v", result.err)
		}
		if result.output.Status == domain.WagerStatusProcessed {
			processed++
		} else {
			rejected++
			assertRejection(t, ctx, pool, result.output, domain.FailureCodeReferenceAlreadyReversed, 10000)
		}
	}
	if processed != 1 || rejected != 1 {
		t.Fatalf("expected one successful reversal, got processed=%d rejected=%d", processed, rejected)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
	assertCount(t, ctx, pool, 1,
		`SELECT count(*) FROM wallet_ledger_entries l JOIN wager_transactions w ON w.id = l.transaction_id
         WHERE l.wallet_id = $1 AND w.kind IN ('REFUND', 'ROLLBACK')`, wallet)
}

func TestConcurrentPendingReferenceWorkersDoNotDuplicateFinancialEffect(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	refund := reversalInput(t, bet, domain.WagerKindRefund)
	pending := executeWager(t, ctx, pool, refund)
	executeWager(t, ctx, pool, bet)
	wager, err := NewWagerTransactionRepository(pool).FindByID(ctx, pending.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := wager.ReferenceNextAttemptAt()
	start := make(chan struct{})
	var group sync.WaitGroup
	counts, errs := make([]int, 3), make([]error, 3)
	for index := range counts {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			counts[i], errs[i] = application.NewRetryPendingReferences(NewTransactionManager(pool), nil).Execute(ctx, next, 10)
		}(index)
	}
	close(start)
	group.Wait()
	total := 0
	for i := range counts {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		total += counts[i]
	}
	if total != 1 {
		t.Fatalf("expected one claimed retry, got %d", total)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", pending.TransactionID)
}

func TestReversalLatePersistenceFailureRollsBackEverything(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	executeWager(t, ctx, pool, bet)
	wagerID, duplicateEventID := uuid.NewString(), uuid.NewString()
	useCase := application.NewProcessWagerTransaction(NewTransactionManager(pool), sequenceIDGenerator(t,
		wagerID, uuid.NewString(), duplicateEventID, duplicateEventID))
	if _, err := useCase.Execute(ctx, reversalInput(t, bet, domain.WagerKindRefund)); err == nil {
		t.Fatal("expected a late outbox persistence failure")
	}
	assertWalletState(t, ctx, pool, wallet, 7500, 2)
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wager_transactions WHERE id = $1", wagerID)
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", wagerID)
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM outbox_events WHERE correlation_id = $1", wagerID)
	// The failed attempt must not consume the unique reversal slot.
	output := executeWager(t, ctx, pool, reversalInput(t, bet, domain.WagerKindRefund))
	assertWagerOutput(t, output, domain.WagerStatusProcessed, 10000, false)
}

func TestNewFinancialMigrationsDownUp(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	applyTestMigration(t, ctx, pool, "000003_pending_reference_state.down.sql")
	applyTestMigration(t, ctx, pool, "000002_wager_result_balance.down.sql")
	applyTestMigration(t, ctx, pool, "000002_wager_result_balance.up.sql")
	applyTestMigration(t, ctx, pool, "000003_pending_reference_state.up.sql")
	assertCount(t, ctx, pool, 3,
		`SELECT count(*) FROM pg_constraint
         WHERE conrelid = 'wager_transactions'::regclass
         AND conname IN ('chk_wager_transactions_result_by_status',
                         'chk_wager_transactions_reference_schedule', 'fk_wager_transactions_wallet')`)
}

func TestFiftyConcurrentREFUNDReplaysHaveOneFinancialEffect(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	executeWager(t, ctx, pool, bet)
	refund := reversalInput(t, bet, domain.WagerKindRefund)
	inputs := make([]application.ProcessWagerTransactionInput, 50)
	for index := range inputs {
		inputs[index] = refund
	}
	results := runConcurrentWagers(t, ctx, pool, inputs)
	originals, replays := 0, 0
	id := ""
	for _, result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		assertWagerOutput(t, result.output, domain.WagerStatusProcessed, 10000, result.output.IdempotentReplay)
		if result.output.IdempotentReplay {
			replays++
		} else {
			originals++
		}
		if id == "" {
			id = result.output.TransactionID
		} else if id != result.output.TransactionID {
			t.Fatal("concurrent REFUND replay changed identity")
		}
	}
	if originals != 1 || replays != 49 {
		t.Fatalf("expected one REFUND and 49 replays, got %d/%d", originals, replays)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
	assertCount(t, ctx, pool, 1, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", id)
	assertOutboxTypes(t, ctx, pool, id, "WagerTransactionProcessed", "WalletBalanceChanged")
}

func TestPendingRetryFailureRollsBackAttemptAndSchedule(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	pending := executeWager(t, ctx, pool, reversalInput(t, bet, domain.WagerKindRefund))
	executeWager(t, ctx, pool, bet)
	wager, err := NewWagerTransactionRepository(pool).FindByID(ctx, pending.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := wager.ReferenceNextAttemptAt()
	duplicateEvent := uuid.NewString()
	worker := application.NewRetryPendingReferences(NewTransactionManager(pool), sequenceIDGenerator(t,
		uuid.NewString(), duplicateEvent, duplicateEvent))
	if _, err := worker.Execute(ctx, next, 1); err == nil {
		t.Fatal("expected duplicate outbox event to abort the retry")
	}
	persisted, err := NewWagerTransactionRepository(pool).FindByID(ctx, pending.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	persistedNext, _ := persisted.ReferenceNextAttemptAt()
	if persisted.Status() != domain.WagerStatusPendingReference ||
		persisted.ReferenceAttempts() != 0 || !persistedNext.Equal(next) {
		t.Fatal("failed retry must not consume an attempt or change the persisted schedule")
	}
	assertWalletState(t, ctx, pool, wallet, 7500, 2)
	assertCount(t, ctx, pool, 0, "SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1", pending.TransactionID)
	assertOutboxTypes(t, ctx, pool, pending.TransactionID, "WagerTransactionPendingReference")
	if _, err := application.NewRetryPendingReferences(NewTransactionManager(pool), nil).Execute(ctx, next, 1); err != nil {
		t.Fatal(err)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
}

func TestResultBalanceDowngradeDoesNotDiscardRejectionSnapshot(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 2000)
	output := executeWager(t, ctx, pool, newWagerInput(t, wallet, player, domain.WagerKindBet, 2500))
	sql, err := os.ReadFile("../../database/migrations/000002_wager_result_balance.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, string(sql)); err == nil {
		t.Fatal("downgrade should reject a constraint that would invalidate financial snapshots")
	}
	if _, err := connection.Exec(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	assertRejection(t, ctx, pool, output, domain.FailureCodeInsufficientBalance, 2000)
}
