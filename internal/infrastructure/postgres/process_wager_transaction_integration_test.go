package postgres

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func TestProcessWagerTransactionBET(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	input := newWagerInput(t, walletID, playerID, domain.WagerKindBet, 2500)

	output, err := newProcessWagerUseCase(pool).Execute(ctx, input)
	if err != nil {
		t.Fatalf("process BET: %v", err)
	}
	assertWagerOutput(t, output, domain.WagerStatusProcessed, 7500, false)
	assertWalletState(t, ctx, pool, walletID, 7500, 2)
	assertLedger(t, ctx, pool, output.TransactionID, domain.LedgerDirectionDebit, 2500, 10000, 7500)
	assertOutboxTypes(t, ctx, pool, output.TransactionID,
		"WagerTransactionProcessed", "WalletBalanceChanged")
}

func TestProcessWagerTransactionRejectsInsufficientBET(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 2000)
	input := newWagerInput(t, walletID, playerID, domain.WagerKindBet, 2500)

	output, err := newProcessWagerUseCase(pool).Execute(ctx, input)
	if err != nil {
		t.Fatalf("process insufficient BET: %v", err)
	}
	assertWagerOutput(t, output, domain.WagerStatusRejected, 2000, false)
	assertWalletState(t, ctx, pool, walletID, 2000, 1)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1",
		output.TransactionID,
	)
	assertCount(t, ctx, pool, 1,
		`SELECT count(*) FROM wager_transactions
         WHERE id = $1 AND failure_code = 'INSUFFICIENT_BALANCE'`,
		output.TransactionID,
	)
	assertOutboxTypes(t, ctx, pool, output.TransactionID, "WagerTransactionRejected")
}

func TestProcessWagerTransactionWIN(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	input := newWagerInput(t, walletID, playerID, domain.WagerKindWin, 2500)

	output, err := newProcessWagerUseCase(pool).Execute(ctx, input)
	if err != nil {
		t.Fatalf("process WIN: %v", err)
	}
	assertWagerOutput(t, output, domain.WagerStatusProcessed, 12500, false)
	assertWalletState(t, ctx, pool, walletID, 12500, 2)
	assertLedger(t, ctx, pool, output.TransactionID, domain.LedgerDirectionCredit, 2500, 10000, 12500)
	assertOutboxTypes(t, ctx, pool, output.TransactionID,
		"WagerTransactionProcessed", "WalletBalanceChanged")
}

func TestProcessWagerTransactionLOSS(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	input := newWagerInput(t, walletID, playerID, domain.WagerKindLoss, 0)

	output, err := newProcessWagerUseCase(pool).Execute(ctx, input)
	if err != nil {
		t.Fatalf("process LOSS: %v", err)
	}
	assertWagerOutput(t, output, domain.WagerStatusProcessed, 10000, false)
	assertWalletState(t, ctx, pool, walletID, 10000, 1)
	assertCount(t, ctx, pool, 0,
		"SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1",
		output.TransactionID,
	)
	assertOutboxTypes(t, ctx, pool, output.TransactionID, "WagerTransactionProcessed")
}

func TestProcessWagerTransactionReplayReturnsOriginalBalance(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	useCase := newProcessWagerUseCase(pool)
	bet := newWagerInput(t, walletID, playerID, domain.WagerKindBet, 2500)

	first, err := useCase.Execute(ctx, bet)
	if err != nil {
		t.Fatalf("process original BET: %v", err)
	}
	win := newWagerInput(t, walletID, playerID, domain.WagerKindWin, 1000)
	if _, err := useCase.Execute(ctx, win); err != nil {
		t.Fatalf("process intervening WIN: %v", err)
	}

	replay, err := useCase.Execute(ctx, bet)
	if err != nil {
		t.Fatalf("replay BET: %v", err)
	}
	assertWagerOutput(t, replay, domain.WagerStatusProcessed, 7500, true)
	if replay.TransactionID != first.TransactionID {
		t.Fatalf("replay changed transaction ID: %s != %s", replay.TransactionID, first.TransactionID)
	}
	assertWalletState(t, ctx, pool, walletID, 8500, 3)
	assertCount(t, ctx, pool, 1,
		"SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1",
		first.TransactionID,
	)
}

func TestProcessWagerTransactionConflictsOnChangedPayloadForSameKey(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	useCase := newProcessWagerUseCase(pool)
	firstInput := newWagerInput(t, walletID, playerID, domain.WagerKindBet, 2500)
	first, err := useCase.Execute(ctx, firstInput)
	if err != nil {
		t.Fatalf("process original BET: %v", err)
	}

	changed := firstInput
	changed.ExternalTransactionID = "external-" + newTestUUID(t)
	changed.Money = mustMoney(t, 1000)
	_, err = useCase.Execute(ctx, changed)
	if !errors.Is(err, application.ErrIdempotencyConflict) {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	assertWalletState(t, ctx, pool, walletID, 7500, 2)
	assertCount(t, ctx, pool, 1,
		"SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1",
		first.TransactionID,
	)
}

func TestProcessWagerTransactionConflictsOnReusedExternalID(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	useCase := newProcessWagerUseCase(pool)
	firstInput := newWagerInput(t, walletID, playerID, domain.WagerKindBet, 2500)
	if _, err := useCase.Execute(ctx, firstInput); err != nil {
		t.Fatalf("process original BET: %v", err)
	}

	changed := firstInput
	changed.IdempotencyKey = "key-" + newTestUUID(t)
	_, err := useCase.Execute(ctx, changed)
	if !errors.Is(err, application.ErrExternalTransactionConflict) {
		t.Fatalf("expected external transaction conflict, got %v", err)
	}
	assertWalletState(t, ctx, pool, walletID, 7500, 2)
}

func TestProcessWagerTransactionSerializesTwoLargeBETs(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	inputs := []application.ProcessWagerTransactionInput{
		newWagerInput(t, walletID, playerID, domain.WagerKindBet, 8000),
		newWagerInput(t, walletID, playerID, domain.WagerKindBet, 8000),
	}

	results := runConcurrentWagers(t, ctx, pool, inputs)
	processed := 0
	rejected := 0
	transactionIDs := make([]string, 0, 2)
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("process concurrent BET: %v", result.err)
		}
		transactionIDs = append(transactionIDs, result.output.TransactionID)
		switch result.output.Status {
		case domain.WagerStatusProcessed:
			processed++
		case domain.WagerStatusRejected:
			rejected++
		}
	}
	if processed != 1 || rejected != 1 {
		t.Fatalf("expected one processed and one rejected, got processed=%d rejected=%d", processed, rejected)
	}
	assertWalletState(t, ctx, pool, walletID, 2000, 2)
	assertCount(t, ctx, pool, 1,
		`SELECT count(*) FROM wallet_ledger_entries
         WHERE transaction_id::text = ANY($1) AND direction = 'DEBIT' AND money_amount = 8000`,
		transactionIDs,
	)
}

func TestProcessWagerTransactionFiftyConcurrentDuplicatesHaveOneEffect(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletID, playerID := createFundedWallet(t, ctx, pool, 10000)
	input := newWagerInput(t, walletID, playerID, domain.WagerKindBet, 2500)
	inputs := make([]application.ProcessWagerTransactionInput, 50)
	for index := range inputs {
		inputs[index] = input
	}

	results := runConcurrentWagers(t, ctx, pool, inputs)
	nonReplay := 0
	replays := 0
	transactionID := ""
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("process duplicate BET: %v", result.err)
		}
		if result.output.IdempotentReplay {
			replays++
		} else {
			nonReplay++
		}
		if transactionID == "" {
			transactionID = result.output.TransactionID
		} else if result.output.TransactionID != transactionID {
			t.Fatalf("duplicate returned another transaction: %s != %s", result.output.TransactionID, transactionID)
		}
	}
	if nonReplay != 1 || replays != 49 {
		t.Fatalf("expected one original and 49 replays, got original=%d replays=%d", nonReplay, replays)
	}
	assertWalletState(t, ctx, pool, walletID, 7500, 2)
	assertCount(t, ctx, pool, 1,
		"SELECT count(*) FROM wallet_ledger_entries WHERE transaction_id = $1",
		transactionID,
	)
	assertCount(t, ctx, pool, 1,
		`SELECT count(*) FROM wager_transactions
         WHERE provider_id = $1 AND idempotency_key = $2`,
		input.ProviderID,
		input.IdempotencyKey,
	)
}

func TestProcessWagerTransactionDoesNotGloballyLockDifferentWallets(t *testing.T) {
	ctx, pool := createWalletTestPool(t)
	walletA, playerA := createFundedWallet(t, ctx, pool, 10000)
	walletB, playerB := createFundedWallet(t, ctx, pool, 10000)

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin blocking transaction: %v", err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, "SELECT id FROM wallets WHERE id = $1 FOR UPDATE", walletA); err != nil {
		t.Fatalf("lock wallet A: %v", err)
	}
	inputA := newWagerInput(t, walletA, playerA, domain.WagerKindBet, 1000)
	inputB := newWagerInput(t, walletB, playerB, domain.WagerKindBet, 1000)

	type asyncResult struct {
		output application.ProcessWagerTransactionOutput
		err    error
	}
	resultA := make(chan asyncResult, 1)
	go func() {
		output, executeErr := newProcessWagerUseCase(pool).Execute(
			ctx,
			inputA,
		)
		resultA <- asyncResult{output: output, err: executeErr}
	}()

	resultB := make(chan asyncResult, 1)
	go func() {
		output, executeErr := newProcessWagerUseCase(pool).Execute(
			ctx,
			inputB,
		)
		resultB <- asyncResult{output: output, err: executeErr}
	}()

	select {
	case result := <-resultB:
		if result.err != nil || result.output.Status != domain.WagerStatusProcessed {
			t.Fatalf("wallet B did not progress independently: output=%+v err=%v", result.output, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wallet B was blocked by the lock held on wallet A")
	}

	select {
	case result := <-resultA:
		t.Fatalf("wallet A unexpectedly bypassed its row lock: output=%+v err=%v", result.output, result.err)
	default:
	}

	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release wallet A lock: %v", err)
	}
	select {
	case result := <-resultA:
		if result.err != nil || result.output.Status != domain.WagerStatusProcessed {
			t.Fatalf("wallet A did not finish after lock release: output=%+v err=%v", result.output, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wallet A did not finish after its row lock was released")
	}

	assertWalletState(t, ctx, pool, walletA, 9000, 2)
	assertWalletState(t, ctx, pool, walletB, 9000, 2)
}

type wagerExecutionResult struct {
	output application.ProcessWagerTransactionOutput
	err    error
}

func runConcurrentWagers(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	inputs []application.ProcessWagerTransactionInput,
) []wagerExecutionResult {
	t.Helper()

	start := make(chan struct{})
	results := make([]wagerExecutionResult, len(inputs))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(inputs))
	for index := range inputs {
		index := index
		go func() {
			defer waitGroup.Done()
			<-start
			results[index].output, results[index].err = newProcessWagerUseCase(pool).Execute(ctx, inputs[index])
		}()
	}
	close(start)
	waitGroup.Wait()

	return results
}

func newProcessWagerUseCase(pool *pgxpool.Pool) *application.ProcessWagerTransaction {
	return application.NewProcessWagerTransaction(NewTransactionManager(pool), nil)
}

func createFundedWallet(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	amount int64,
) (string, string) {
	t.Helper()

	playerID := newTestUUID(t)
	output, err := application.NewCreateWallet(NewTransactionManager(pool), nil).Execute(
		ctx,
		application.CreateWalletInput{
			PlayerID:       playerID,
			InitialBalance: mustMoney(t, amount),
		},
	)
	if err != nil {
		t.Fatalf("create funded wallet: %v", err)
	}

	return output.WalletID, playerID
}

func newWagerInput(
	t *testing.T,
	walletID string,
	playerID string,
	kind domain.WagerKind,
	amount int64,
) application.ProcessWagerTransactionInput {
	t.Helper()

	identity := newTestUUID(t)
	return application.ProcessWagerTransactionInput{
		ProviderID:            "provider-test",
		ExternalTransactionID: "external-" + identity,
		IdempotencyKey:        "key-" + identity,
		PlayerID:              playerID,
		WalletID:              walletID,
		RoundID:               "round-" + identity,
		GameID:                "game-" + identity,
		Kind:                  kind,
		Money:                 mustMoney(t, amount),
	}
}

func assertWagerOutput(
	t *testing.T,
	output application.ProcessWagerTransactionOutput,
	status domain.WagerStatus,
	balance int64,
	replay bool,
) {
	t.Helper()
	if output.TransactionID == "" ||
		output.Status != status ||
		output.Balance.Amount() != balance ||
		output.IdempotentReplay != replay {
		t.Fatalf("unexpected wager output: %+v", output)
	}
}

func assertWalletState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	walletID string,
	balance int64,
	version int64,
) {
	t.Helper()

	var gotBalance int64
	var gotVersion int64
	if err := pool.QueryRow(
		ctx,
		"SELECT balance_amount, version FROM wallets WHERE id = $1",
		walletID,
	).Scan(&gotBalance, &gotVersion); err != nil {
		t.Fatalf("query wallet state: %v", err)
	}
	if gotBalance != balance || gotVersion != version {
		t.Fatalf("unexpected wallet state: balance=%d version=%d", gotBalance, gotVersion)
	}
}

func assertLedger(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	transactionID string,
	direction domain.LedgerDirection,
	money int64,
	before int64,
	after int64,
) {
	t.Helper()

	var gotDirection string
	var gotMoney int64
	var gotBefore int64
	var gotAfter int64
	if err := pool.QueryRow(
		ctx,
		`SELECT direction, money_amount, balance_before, balance_after
         FROM wallet_ledger_entries WHERE transaction_id = $1`,
		transactionID,
	).Scan(&gotDirection, &gotMoney, &gotBefore, &gotAfter); err != nil {
		t.Fatalf("query ledger: %v", err)
	}
	if gotDirection != string(direction) || gotMoney != money || gotBefore != before || gotAfter != after {
		t.Fatalf("unexpected ledger: direction=%s money=%d before=%d after=%d", gotDirection, gotMoney, gotBefore, gotAfter)
	}
}

func assertOutboxTypes(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	correlationID string,
	want ...string,
) {
	t.Helper()

	rows, err := pool.Query(
		ctx,
		"SELECT event_type FROM outbox_events WHERE correlation_id = $1 ORDER BY event_type",
		correlationID,
	)
	if err != nil {
		t.Fatalf("query outbox event types: %v", err)
	}
	defer rows.Close()

	got := make([]string, 0, len(want))
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("scan outbox event type: %v", err)
		}
		got = append(got, eventType)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outbox event types: %v", err)
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected outbox event types: got=%v want=%v", got, want)
	}
}
