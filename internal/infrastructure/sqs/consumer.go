package sqs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

const InputVisibilitySeconds int32 = 30

type Consumer struct {
	lifecycle lifecycle
	client    *awssqs.Client
	queueURL  string
	processor *application.ProcessWagerMessage
	logger    *slog.Logger
	metrics   ConsumerMetrics
	settings  ConsumerSettings
}

type ConsumeResult struct{ Received, Completed, InboxReplays, Invalid, Retried int }

type ConsumerMetrics interface {
	ObserveWager(status, transport string, replay bool, duration time.Duration)
	RecordRetry(component string, count int)
	RecordDLQCandidate()
	RecordDuplicate(kind string)
	RecordConflict(kind string)
	ObserveSQSMessage(result string)
}

type ConsumerSettings struct {
	ReceiveWait, VisibilityTimeout, ProcessingTimeout, OperationTimeout, IdleDelay time.Duration
	MaxReceiveCount                                                                int32
}

type ConsumerOption func(*Consumer)

func WithConsumerSettings(settings ConsumerSettings) ConsumerOption {
	return func(consumer *Consumer) { consumer.settings = settings }
}

func WithConsumerMetrics(metrics ConsumerMetrics) ConsumerOption {
	return func(consumer *Consumer) { consumer.metrics = metrics }
}

func NewConsumer(client *awssqs.Client, queueURL string, processor *application.ProcessWagerMessage, logger *slog.Logger, options ...ConsumerOption) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	consumer := &Consumer{client: client, queueURL: queueURL, processor: processor, logger: logger,
		settings: ConsumerSettings{ReceiveWait: 20 * time.Second, VisibilityTimeout: 30 * time.Second,
			ProcessingTimeout: 20 * time.Second, OperationTimeout: 5 * time.Second, IdleDelay: time.Second, MaxReceiveCount: 5}}
	for _, option := range options {
		option(consumer)
	}
	return consumer
}

func (c *Consumer) Start(ctx context.Context) error {
	return c.lifecycle.start(ctx, func(polling, work context.Context) {
		for polling.Err() == nil {
			result, err := c.poll(polling, work, int32(c.settings.ReceiveWait/time.Second))
			if err != nil && polling.Err() == nil {
				c.logger.ErrorContext(work, "SQS consumer pass failed", "error", err)
				if !pause(polling, c.settings.IdleDelay) {
					return
				}
			}
			if err == nil && result.Received == 0 && !pause(polling, c.settings.IdleDelay) {
				return
			}
		}
	})
}

func (c *Consumer) Stop(ctx context.Context) error { return c.lifecycle.stop(ctx) }

func (c *Consumer) Running() bool { return c.lifecycle.isRunning() }

// PollOnce is a bounded, non-long-poll pass, also useful for manual execution.
func (c *Consumer) PollOnce(ctx context.Context) (ConsumeResult, error) { return c.poll(ctx, ctx, 0) }

func (c *Consumer) poll(receiving, work context.Context, wait int32) (ConsumeResult, error) {
	var result ConsumeResult
	response, err := c.client.ReceiveMessage(receiving, &awssqs.ReceiveMessageInput{
		QueueUrl: aws.String(c.queueURL), MaxNumberOfMessages: 1, WaitTimeSeconds: wait,
		VisibilityTimeout:           int32(c.settings.VisibilityTimeout / time.Second),
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameApproximateReceiveCount},
	})
	if err != nil {
		return result, fmt.Errorf("receive SQS: %w", err)
	}
	for _, message := range response.Messages {
		if receiving.Err() != nil {
			return result, receiving.Err()
		}
		result.Received++
		started := time.Now()
		// Processing fits inside the 30s visibility lease. A visibility lapse
		// is still safe: Inbox and wager constraints arbitrate duplicates.
		processing, cancel := context.WithTimeout(work, c.settings.ProcessingTimeout)
		input, err := DecodeWagerMessage(aws.ToString(message.Body))
		var output application.ProcessWagerMessageOutput
		if err == nil {
			output, err = c.processor.Execute(processing, input)
		}
		cancel()
		if err != nil {
			permanent := isPermanentMessageError(err)
			if permanent {
				result.Invalid++
				if c.metrics != nil {
					c.metrics.ObserveSQSMessage("invalid")
					c.metrics.RecordConflict(messageConflictKind(err))
				}
			} else {
				result.Retried++
				if c.metrics != nil {
					c.metrics.ObserveSQSMessage("retry")
					c.metrics.RecordRetry("sqs_consumer", 1)
				}
			}
			c.logger.WarnContext(work, "SQS wager message not completed", "message_id", input.MessageID,
				"sqs_message_id", aws.ToString(message.MessageId), "permanent", permanent, "error", err)
			count, _ := strconv.ParseInt(message.Attributes["ApproximateReceiveCount"], 10, 32)
			if int32(count) >= c.settings.MaxReceiveCount && c.metrics != nil {
				c.metrics.RecordDLQCandidate()
			}
			visibility := InputBackoff(int32(count))
			operation, cancel := context.WithTimeout(work, c.settings.OperationTimeout)
			_, visibilityErr := c.client.ChangeMessageVisibility(operation, &awssqs.ChangeMessageVisibilityInput{
				QueueUrl: aws.String(c.queueURL), ReceiptHandle: message.ReceiptHandle, VisibilityTimeout: visibility})
			cancel()
			if visibilityErr != nil {
				return result, fmt.Errorf("schedule SQS visibility retry: %w", visibilityErr)
			}
			// Invalid messages also remain in SQS; finite redrive sends them
			// to the configured DLQ without another financial effect.
			continue
		}
		// Execute returned only after SQL COMMIT. No earlier path deletes.
		operation, cancel := context.WithTimeout(work, c.settings.OperationTimeout)
		_, err = c.client.DeleteMessage(operation, &awssqs.DeleteMessageInput{QueueUrl: aws.String(c.queueURL), ReceiptHandle: message.ReceiptHandle})
		cancel()
		if err != nil {
			return result, fmt.Errorf("delete committed SQS message: %w", err)
		}
		result.Completed++
		if output.InboxReplay {
			result.InboxReplays++
		}
		if c.metrics != nil {
			if output.InboxReplay {
				c.metrics.ObserveSQSMessage("inbox_replay")
				c.metrics.RecordDuplicate("inbox")
			} else {
				c.metrics.ObserveSQSMessage("completed")
				c.metrics.ObserveWager(string(output.Wager.Status), "sqs", output.Wager.IdempotentReplay, time.Since(started))
			}
		}
		c.logger.InfoContext(work, "SQS wager message committed and deleted", "message_id", input.MessageID,
			"transaction_id", output.Wager.TransactionID, "wallet_id", input.Wager.WalletID,
			"provider_id", input.Wager.ProviderID, "inbox_replay", output.InboxReplay)
	}
	return result, nil
}

func messageConflictKind(err error) string {
	switch {
	case errors.Is(err, application.ErrInboxPayloadConflict):
		return "inbox_payload"
	case errors.Is(err, application.ErrIdempotencyConflict):
		return "idempotency"
	case errors.Is(err, application.ErrExternalTransactionConflict):
		return "external_transaction"
	default:
		return "invalid_message"
	}
}

// 1, 2, 4, 8... seconds capped at 30s. Redrive limits input delivery attempts.
func InputBackoff(receiveCount int32) int32 {
	delay := int32(1)
	for count := int32(1); count < receiveCount && delay < 30; count++ {
		delay *= 2
	}
	if delay > 30 {
		return 30
	}
	return delay
}

func isPermanentMessageError(err error) bool {
	for _, sentinel := range []error{application.ErrInvalidMessage, application.ErrInboxPayloadConflict,
		application.ErrIdempotencyConflict, application.ErrExternalTransactionConflict, application.ErrWagerWalletRelationship,
		application.ErrUnsupportedWagerKind, application.ErrNotFound, domain.ErrInvalidWagerKind,
		domain.ErrInvalidWagerAmount, domain.ErrInvalidWagerCurrency, domain.ErrInvalidWagerState, domain.ErrMissingReference} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

func pause(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
