package postgres

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type reversalProcessRequest struct {
	WalletID    string
	PlayerID    string
	ProviderID  string
	ReferenceID string
	RoundID     string
	ExternalID  string
	Key         string
	Kind        domain.WagerKind
	Amount      int64
}

type reversalProcessResult struct {
	TransactionID string
	Status        domain.WagerStatus
	FailureCode   domain.FailureCode
}

// This test is also the entry point of the independent test-binary processes.
func TestReversalChildProcess(t *testing.T) {
	requestJSON := os.Getenv("WAGER_REVERSAL_CHILD_INPUT")
	if requestJSON == "" {
		t.Skip("helper for the independent-process concurrency test")
	}
	var request reversalProcessRequest
	if err := json.Unmarshal([]byte(requestJSON), &request); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(os.Getenv("POSTGRES_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = os.Getenv("WAGER_REVERSAL_CHILD_SCHEMA")
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	input := newWagerInput(t, request.WalletID, request.PlayerID, request.Kind, request.Amount)
	input.ProviderID = request.ProviderID
	input.ExternalTransactionID = request.ExternalID
	input.IdempotencyKey = request.Key
	input.RoundID = request.RoundID
	input.ReferenceExternalTransactionID = request.ReferenceID
	output := executeWager(t, ctx, pool, input)
	payload, err := json.Marshal(reversalProcessResult{output.TransactionID, output.Status, output.FailureCode})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("REVERSAL_RESULT=" + string(payload))
}

func TestThreeIndependentProcessesCannotReverseBETTwice(t *testing.T) {
	ctx, pool := reversalTestPool(t)
	wallet, player := createFundedWallet(t, ctx, pool, 10000)
	bet := newWagerInput(t, wallet, player, domain.WagerKindBet, 2500)
	executeWager(t, ctx, pool, bet)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]*exec.Cmd, 3)
	for index, kind := range []domain.WagerKind{domain.WagerKindRefund, domain.WagerKindRollback, domain.WagerKindRefund} {
		input := reversalInput(t, bet, kind)
		payload, err := json.Marshal(reversalProcessRequest{
			WalletID: wallet, PlayerID: player, ProviderID: bet.ProviderID,
			ReferenceID: bet.ExternalTransactionID, RoundID: bet.RoundID,
			ExternalID: input.ExternalTransactionID, Key: input.IdempotencyKey,
			Kind: kind, Amount: input.Money.Amount(),
		})
		if err != nil {
			t.Fatal(err)
		}
		commands[index] = exec.CommandContext(ctx, binary, "-test.run=^TestReversalChildProcess$", "-test.v")
		commands[index].Env = append(os.Environ(),
			"WAGER_REVERSAL_CHILD_INPUT="+string(payload),
			"WAGER_REVERSAL_CHILD_SCHEMA="+pool.Config().ConnConfig.RuntimeParams["search_path"],
		)
	}
	var group sync.WaitGroup
	outputs, errs := make([][]byte, 3), make([]error, 3)
	start := make(chan struct{})
	for index := range commands {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			<-start
			outputs[i], errs[i] = commands[i].CombinedOutput()
		}(index)
	}
	close(start)
	group.Wait()
	processed, rejected := 0, 0
	for index, output := range outputs {
		if errs[index] != nil {
			t.Fatalf("independent process %d failed: %v\n%s", index, errs[index], output)
		}
		marker := "REVERSAL_RESULT="
		position := strings.Index(string(output), marker)
		if position < 0 {
			t.Fatalf("process result missing: %s", output)
		}
		payload := strings.SplitN(string(output)[position+len(marker):], "\n", 2)[0]
		var result reversalProcessResult
		if err := json.Unmarshal([]byte(payload), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status == domain.WagerStatusProcessed {
			processed++
		} else if result.Status == domain.WagerStatusRejected && result.FailureCode == domain.FailureCodeReferenceAlreadyReversed {
			rejected++
		} else {
			t.Fatalf("unexpected process result: %+v", result)
		}
	}
	if processed != 1 || rejected != 2 {
		t.Fatalf("expected one financial reversal and two rejections, got %d/%d", processed, rejected)
	}
	assertWalletState(t, ctx, pool, wallet, 10000, 3)
	assertCount(t, ctx, pool, 1,
		`SELECT count(*) FROM wallet_ledger_entries l JOIN wager_transactions w ON w.id = l.transaction_id
         WHERE l.wallet_id = $1 AND w.kind IN ('REFUND', 'ROLLBACK')`, wallet)
}
