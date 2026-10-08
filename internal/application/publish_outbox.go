package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

// IntegrationEventPublisher delivers the already-persisted snapshot.
type IntegrationEventPublisher interface {
	Publish(ctx context.Context, event *domain.OutboxEvent) error
}

type PublishOutbox struct {
	transactions TransactionManager
	publisher    IntegrationEventPublisher
}

type PublishOutboxResult struct {
	Published   int
	Failed      int
	LastEventID string
}

func NewPublishOutbox(transactions TransactionManager, publisher IntegrationEventPublisher) *PublishOutbox {
	return &PublishOutbox{transactions: transactions, publisher: publisher}
}

// Execute uses one transaction per event, keeping ownership until SendMessage
// and the operational update finish. A crash releases the PostgreSQL row lock.
func (u *PublishOutbox) Execute(ctx context.Context, dueAt time.Time, limit int) (PublishOutboxResult, error) {
	var result PublishOutboxResult
	if dueAt.IsZero() || limit < 1 || limit > 100 {
		return result, fmt.Errorf("outbox cutoff and limit between 1 and 100 are required")
	}
	dueAt = dueAt.UTC()
	for index := 0; index < limit; index++ {
		found, failed := false, false
		err := u.transactions.WithinTransaction(ctx, func(repositories TransactionRepositories) error {
			event, err := repositories.Outbox().FindNextDueForUpdate(ctx, dueAt)
			if errors.Is(err, ErrNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			found = true
			result.LastEventID = event.EventID()
			if err := event.RegisterAttempt(); err != nil {
				return err
			}
			delivery, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = u.publisher.Publish(delivery, event)
			cancel()
			if err != nil {
				failed = true
				// Retry is durably recorded, not spun in memory. Always schedule
				// after actual failure as well as after the polling cutoff.
				base := time.Now().UTC()
				if dueAt.After(base) {
					base = dueAt
				}
				if err := event.ScheduleRetry(base.Add(OutboxBackoff(event.Attempts()))); err != nil {
					return err
				}
			} else if err := event.MarkPublished(); err != nil {
				return err
			}
			return repositories.Outbox().UpdateDelivery(ctx, event)
		})
		if err != nil {
			return result, fmt.Errorf("publish outbox: %w", err)
		}
		if !found {
			break
		}
		if failed {
			result.Failed++
		} else {
			result.Published++
		}
	}
	return result, nil
}

// OutboxBackoff starts at 1s and doubles up to 5m; attempts count all sends.
func OutboxBackoff(attempts int32) time.Duration {
	delay := time.Second
	for attempt := int32(1); attempt < attempts && delay < 5*time.Minute; attempt++ {
		delay *= 2
	}
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}
