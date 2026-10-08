package sqs

import (
	"context"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/postgres"
)

func TestSQSConsumerChildProcess(t *testing.T) {
	queue := os.Getenv("SQS_CHILD_QUEUE")
	if queue == "" {
		t.Skip("helper for independent process test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	settings, err := pgxpool.ParseConfig(os.Getenv("POSTGRES_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	settings.ConnConfig.RuntimeParams["search_path"] = os.Getenv("SQS_CHILD_SCHEMA")
	pool, err := pgxpool.NewWithConfig(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	client, err := NewClient(ctx, Config{Endpoint: os.Getenv("SQS_TEST_ENDPOINT_URL"), Region: "us-east-1", AccessKey: "test", SecretKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := application.NewProcessWagerMessage(postgres.NewTransactionManager(pool), "wager-transactions", nil)
	if err != nil {
		t.Fatal(err)
	}
	consumer := NewConsumer(client, queue, processor, nil)
	for {
		result, err := consumer.PollOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result.Completed == 1 {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestThreeIndependentConsumersCannotDuplicateFinancialEffects(t *testing.T) {
	f := realMessaging(t)
	wallet := f.wallet(t, 10000)
	body := requestBody(t, wallet, domain.WagerKindBet, "25.00")
	// Independent groups bypass FIFO's ordering/dedup protection deliberately,
	// proving the PostgreSQL Inbox PK and financial constraints arbitrate races.
	for index := 0; index < 3; index++ {
		f.sendRaw(t, body, uuid.NewString(), uuid.NewString())
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errs := make([]error, 3)
	outputs := make([][]byte, 3)
	start := make(chan struct{})
	for index := 0; index < 3; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			command := exec.CommandContext(f.ctx, binary, "-test.run=^TestSQSConsumerChildProcess$", "-test.v")
			command.Env = append(os.Environ(), "SQS_CHILD_QUEUE="+f.queues.Input, "SQS_CHILD_SCHEMA="+f.pool.Config().ConnConfig.RuntimeParams["search_path"])
			outputs[index], errs[index] = command.CombinedOutput()
		}(index)
	}
	close(start)
	group.Wait()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("consumer process %d: %v\n%s", index, err, outputs[index])
		}
	}
	f.assertEffects(t, wallet.WalletID, 7500, 1, 1)
	if f.count(t, "SELECT count(*) FROM wager_transactions WHERE kind='BET'") != 1 || f.count(t, "SELECT count(*) FROM outbox_events") != 4 {
		t.Fatal("independent processes duplicated financial state")
	}
	if len(f.receive(t, f.queues.Input)) != 0 {
		t.Fatal("consumers did not remove committed deliveries")
	}
}
