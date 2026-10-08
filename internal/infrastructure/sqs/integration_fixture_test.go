package sqs

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/postgres"
)

type messagingFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	client  *awssqs.Client
	queues  Queues
	config  Config
	manager *postgres.TransactionManager
	logger  *slog.Logger
}

func realMessaging(t *testing.T) *messagingFixture {
	t.Helper()
	database, endpoint := os.Getenv("POSTGRES_TEST_DATABASE_URL"), os.Getenv("SQS_TEST_ENDPOINT_URL")
	if database == "" || endpoint == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL and SQS_TEST_ENDPOINT_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	config, err := pgxpool.ParseConfig(database)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	schema := "messaging_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean schema: %v", err)
		}
		admin.Close()
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"000001_init.up.sql", "000002_wager_result_balance.up.sql", "000003_pending_reference_state.up.sql"} {
		data, err := os.ReadFile("../../database/migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(data)); err != nil {
			t.Fatal(err)
		}
	}
	settings := Config{Endpoint: endpoint, Region: "us-east-1", AccessKey: "test", SecretKey: "test"}
	client, err := NewClient(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	createQueue := func(suffix string) (string, string) {
		name := "test-" + uuid.NewString() + "-" + suffix + ".fifo"
		response, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name), Attributes: map[string]string{
			"FifoQueue": "true", "ContentBasedDeduplication": "false", "VisibilityTimeout": "30"}})
		if err != nil {
			t.Fatal(err)
		}
		queueURL := aws.ToString(response.QueueUrl)
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := client.DeleteQueue(cleanup, &awssqs.DeleteQueueInput{QueueUrl: aws.String(queueURL)}); err != nil {
				t.Errorf("clean test queue: %v", err)
			}
		})
		return name, queueURL
	}
	settings.DLQ.Name, settings.DLQ.URL = createQueue("dlq")
	settings.Input.Name, settings.Input.URL = createQueue("input")
	settings.Events.Name, settings.Events.URL = createQueue("events")
	attributes, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(settings.DLQ.URL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn}})
	if err != nil {
		t.Fatal(err)
	}
	redrive, err := json.Marshal(struct {
		ARN string `json:"deadLetterTargetArn"`
		Max string `json:"maxReceiveCount"`
	}{attributes.Attributes["QueueArn"], "5"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: aws.String(settings.Input.URL), Attributes: map[string]string{"RedrivePolicy": string(redrive)}}); err != nil {
		t.Fatal(err)
	}
	return &messagingFixture{ctx: ctx, pool: pool, client: client, config: settings,
		queues: Queues{settings.Input.URL, settings.DLQ.URL, settings.Events.URL}, manager: postgres.NewTransactionManager(pool), logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
}

func (f *messagingFixture) consumer(t *testing.T) *Consumer {
	t.Helper()
	processor, err := application.NewProcessWagerMessage(f.manager, "wager-transactions", nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewConsumer(f.client, f.queues.Input, processor, f.logger)
}

func (f *messagingFixture) wallet(t *testing.T, amount int64) application.CreateWalletOutput {
	t.Helper()
	money, err := domain.NewMoney(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	wallet, err := application.NewCreateWallet(f.manager, nil).Execute(f.ctx, application.CreateWalletInput{PlayerID: uuid.NewString(), InitialBalance: money})
	if err != nil {
		t.Fatal(err)
	}
	return wallet
}

func requestBody(t *testing.T, wallet application.CreateWalletOutput, kind domain.WagerKind, amount string) string {
	t.Helper()
	var request wagerRequest
	request.MessageID, request.Type, request.OccurredAt = uuid.NewString(), "WagerTransactionRequested", time.Now().UTC()
	request.Data.ProviderID, request.Data.ExternalTransactionID, request.Data.IdempotencyKey = "provider-a", uuid.NewString(), uuid.NewString()
	request.Data.PlayerID, request.Data.WalletID = wallet.PlayerID, wallet.WalletID
	request.Data.RoundID, request.Data.GameID, request.Data.Kind = "round-1", "game-1", kind
	request.Data.Money.Amount, request.Data.Money.Currency = amount, "BRL"
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *messagingFixture) send(t *testing.T, body string) {
	t.Helper()
	input, err := DecodeWagerMessage(body)
	if err != nil {
		t.Fatal(err)
	}
	f.sendRaw(t, body, input.Wager.WalletID, input.MessageID)
}

func (f *messagingFixture) sendRaw(t *testing.T, body, group, dedup string) {
	t.Helper()
	if _, err := f.client.SendMessage(f.ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(f.queues.Input), MessageBody: aws.String(body), MessageGroupId: aws.String(group), MessageDeduplicationId: aws.String(dedup)}); err != nil {
		t.Fatal(err)
	}
}

func (f *messagingFixture) receive(t *testing.T, queue string) []types.Message {
	t.Helper()
	response, err := f.client.ReceiveMessage(f.ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(queue), MaxNumberOfMessages: 10, WaitTimeSeconds: 0, VisibilityTimeout: 30, MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameApproximateReceiveCount}})
	if err != nil {
		t.Fatal(err)
	}
	return response.Messages
}

func (f *messagingFixture) release(t *testing.T, message types.Message) {
	t.Helper()
	if _, err := f.client.ChangeMessageVisibility(f.ctx, &awssqs.ChangeMessageVisibilityInput{QueueUrl: aws.String(f.queues.Input), ReceiptHandle: message.ReceiptHandle, VisibilityTimeout: 0}); err != nil {
		t.Fatal(err)
	}
}

func (f *messagingFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var count int
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f *messagingFixture) assertEffects(t *testing.T, walletID string, balance int64, debits, inbox int) {
	t.Helper()
	wallet, err := postgres.NewWalletRepository(f.pool).FindByID(f.ctx, walletID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Balance().Amount() != balance {
		t.Fatalf("balance %d != %d", wallet.Balance().Amount(), balance)
	}
	if count := f.count(t, "SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id=$1 AND direction='DEBIT'", walletID); count != debits {
		t.Fatalf("debits %d != %d", count, debits)
	}
	if count := f.count(t, "SELECT count(*) FROM inbox_messages WHERE completed_at IS NOT NULL"); count != inbox {
		t.Fatalf("completed Inbox %d != %d", count, inbox)
	}
}
