package httpapi_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/postgres"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/sqs"
)

func realSQSQueues(t *testing.T, ctx context.Context, endpoint string) (*awssqs.Client, sqs.Config) {
	t.Helper()
	settings := sqs.Config{Endpoint: endpoint, Region: "us-east-1", AccessKey: "test", SecretKey: "test"}
	client, err := sqs.NewClient(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, queue := range []*sqs.QueueConfig{&settings.Input, &settings.DLQ, &settings.Events} {
		queue.Name = "http-test-" + uuid.NewString() + ".fifo"
		response, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(queue.Name), Attributes: map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false"}})
		if err != nil {
			t.Fatal(err)
		}
		queue.URL = aws.ToString(response.QueueUrl)
		queueURL := queue.URL
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// DeleteQueue on a queue removed by the readiness failure test is
			// intentionally best-effort; all URLs belong to this test fixture.
			_, _ = client.DeleteQueue(cleanup, &awssqs.DeleteQueueInput{QueueUrl: aws.String(queueURL)})
		})
	}
	return client, settings
}

func TestHTTPAndSQSShareFinancialIdempotencyBothDirections(t *testing.T) {
	if os.Getenv("SQS_TEST_ENDPOINT_URL") == "" {
		t.Skip("SQS_TEST_ENDPOINT_URL is required")
	}
	for _, firstHTTP := range []bool{true, false} {
		name := "SQS_then_HTTP"
		if firstHTTP {
			name = "HTTP_then_SQS"
		}
		t.Run(name, func(t *testing.T) {
			f := realAPI(t)
			wallet := f.createWallet(t, "100.00")
			body := wager(wallet, domain.WagerKindBet, "25.00")
			body.ProviderID = "provider-a"
			key := uuid.NewString()
			envelope := struct {
				MessageID  string    `json:"messageId"`
				Type       string    `json:"type"`
				OccurredAt time.Time `json:"occurredAt"`
				Data       struct {
					wagerBody
					IdempotencyKey string `json:"idempotencyKey"`
				} `json:"data"`
			}{MessageID: uuid.NewString(), Type: "WagerTransactionRequested", OccurredAt: time.Now().UTC()}
			envelope.Data.wagerBody, envelope.Data.IdempotencyKey = body, key
			payload, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			manager := postgres.NewTransactionManager(f.pool)
			processor, err := application.NewProcessWagerMessage(manager, "wager-transactions", nil)
			if err != nil {
				t.Fatal(err)
			}
			consume := func() {
				t.Helper()
				if _, err := f.sqsClient.SendMessage(f.ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(f.sqsConfig.Input.URL), MessageBody: aws.String(string(payload)), MessageGroupId: aws.String(wallet.ID), MessageDeduplicationId: aws.String(envelope.MessageID)}); err != nil {
					t.Fatal(err)
				}
				result, err := sqs.NewConsumer(f.sqsClient, f.sqsConfig.Input.URL, processor, nil).PollOnce(f.ctx)
				if err != nil || result.Completed != 1 || result.Invalid != 0 {
					t.Fatalf("SQS cross-transport processing: %+v %v", result, err)
				}
			}
			var output wireWager
			if firstHTTP {
				output = decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, body, key, 200))
				consume()
			} else {
				consume()
				output = decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, body, key, 200))
			}
			if output.IdempotentReplay == firstHTTP || output.Balance == nil || output.Balance.Amount != "75.00" {
				t.Fatalf("incorrect cross-transport replay: %+v", output)
			}
			state := decode[wireWallet](t, f.request(t, "GET", "/wallets/"+wallet.ID, f.internal, nil, "", 200))
			if state.Balance.Amount != "75.00" || state.Version != 2 {
				t.Fatal("cross-transport operation changed balance twice")
			}
			if count(t, f, "SELECT count(*) FROM wallet_ledger_entries WHERE direction='DEBIT'") != 1 || count(t, f, "SELECT count(*) FROM wager_transactions WHERE kind='BET'") != 1 || count(t, f, "SELECT count(*) FROM outbox_events") != 4 || count(t, f, "SELECT count(*) FROM inbox_messages WHERE completed_at IS NOT NULL") != 1 {
				t.Fatal("cross-transport delivery duplicated or lost durable records")
			}
		})
	}
}

func TestHTTPReadinessFailsWhenRealSQSQueueIsMissing(t *testing.T) {
	if os.Getenv("SQS_TEST_ENDPOINT_URL") == "" {
		t.Skip("SQS_TEST_ENDPOINT_URL is required")
	}
	f := realAPI(t)
	f.request(t, "GET", "/health/ready", "", nil, "", 200)
	if _, err := f.sqsClient.DeleteQueue(f.ctx, &awssqs.DeleteQueueInput{QueueUrl: aws.String(f.sqsConfig.Events.URL)}); err != nil {
		t.Fatal(err)
	}
	state := decode[struct {
		Checks map[string]string `json:"checks"`
	}](t, f.request(t, "GET", "/health/ready", "", nil, "", 503))
	if state.Checks["postgres"] != "up" || state.Checks["sqs"] != "down" {
		t.Fatal("readiness did not distinguish unavailable SQS")
	}
	f.request(t, "GET", "/health/live", "", nil, "", 200)
}
