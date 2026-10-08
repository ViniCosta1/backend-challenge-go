package sqs

import (
	"context"
	"errors"
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

func TestConsumerStopDrainsOrCancelsWithoutPrematureAck(t *testing.T) {
	for _, deadlineStop := range []bool{false, true} {
		name := "drain_to_commit"
		if deadlineStop {
			name = "deadline_rolls_back"
		}
		t.Run(name, func(t *testing.T) {
			f := realMessaging(t)
			wallet := f.wallet(t, 10000)
			lock, err := f.pool.Begin(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback(context.Background())
			if _, err := lock.Exec(f.ctx, "SELECT id FROM wallets WHERE id=$1 FOR UPDATE", wallet.WalletID); err != nil {
				t.Fatal(err)
			}
			config := f.pool.Config().Copy()
			name := "consumer-stop-" + uuid.NewString()
			config.ConnConfig.RuntimeParams["application_name"] = name
			consumerPool, err := pgxpool.NewWithConfig(f.ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(consumerPool.Close)
			processor, err := application.NewProcessWagerMessage(postgres.NewTransactionManager(consumerPool), "wager-transactions", nil)
			if err != nil {
				t.Fatal(err)
			}
			consumer := NewConsumer(f.client, f.queues.Input, processor, f.logger)
			f.send(t, requestBody(t, wallet, domain.WagerKindBet, "25.00"))
			if err := consumer.Start(f.ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = consumer.Stop(shutdown)
			})
			if err := consumer.Start(f.ctx); !errors.Is(err, ErrAlreadyStarted) {
				t.Fatal("duplicate Start accepted")
			}
			until := time.Now().Add(3 * time.Second)
			for f.count(t, "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock'", name) == 0 {
				if time.Now().After(until) {
					t.Fatal("consumer did not reach the locked wallet")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if deadlineStop {
				shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				err := consumer.Stop(shutdown)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Stop should respect deadline: %v", err)
				}
				finished, cancel := context.WithTimeout(context.Background(), time.Second)
				if err := consumer.Stop(finished); err != nil {
					t.Fatal(err)
				}
				cancel()
				if err := lock.Rollback(f.ctx); err != nil {
					t.Fatal(err)
				}
				f.assertEffects(t, wallet.WalletID, 10000, 0, 0)
				attributes, err := f.client.GetQueueAttributes(f.ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(f.queues.Input), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameApproximateNumberOfMessagesNotVisible}})
				if err != nil || attributes.Attributes["ApproximateNumberOfMessagesNotVisible"] != "1" {
					t.Fatalf("uncommitted message was acknowledged: %+v %v", attributes, err)
				}
			} else {
				stopped := make(chan error, 1)
				go func() {
					shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					stopped <- consumer.Stop(shutdown)
				}()
				select {
				case err := <-stopped:
					t.Fatalf("Stop abandoned ongoing processing: %v", err)
				case <-time.After(30 * time.Millisecond):
				}
				if err := lock.Commit(f.ctx); err != nil {
					t.Fatal(err)
				}
				if err := <-stopped; err != nil {
					t.Fatal(err)
				}
				f.assertEffects(t, wallet.WalletID, 7500, 1, 1)
				if len(f.receive(t, f.queues.Input)) != 0 {
					t.Fatal("drained commit was not acknowledged")
				}
			}
		})
	}
}

func TestPublisherStopDrainsLockedDelivery(t *testing.T) {
	f := realMessaging(t)
	f.wallet(t, 10000)
	entered := make(chan string, 2)
	release := make(chan struct{})
	observed := &observedSender{actual: NewEventSender(f.client, f.queues.Events), entered: entered, release: release}
	publisher := NewPublisher(application.NewPublishOutbox(f.manager, observed), f.logger)
	if err := publisher.Start(f.ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = publisher.Stop(shutdown)
	})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("publisher did not start")
	}
	stopped := make(chan error, 1)
	go func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		stopped <- publisher.Stop(shutdown)
	}()
	select {
	case err := <-stopped:
		close(release)
		t.Fatalf("Stop did not drain delivery: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if f.count(t, "SELECT count(*) FROM outbox_events WHERE published_at IS NOT NULL") != 1 {
		t.Fatal("Stop published a new event or abandoned ongoing send")
	}
}
