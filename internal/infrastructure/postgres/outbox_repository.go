package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type OutboxRepository struct {
	db DBTX
}

func NewOutboxRepository(db DBTX) *OutboxRepository {
	return &OutboxRepository{db: db}
}

func (r *OutboxRepository) Create(
	ctx context.Context,
	event *domain.OutboxEvent,
) error {
	if event == nil {
		return fmt.Errorf("create outbox event: event is required")
	}

	const query = `
INSERT INTO outbox_events (
    event_id,
    aggregate_id,
    event_type,
    correlation_id,
    causation_id,
    version,
    payload,
    occurred_at,
    attempts,
    next_attempt_at,
    published_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	causationID, hasCausationID := event.CausationID()
	publishedAt, published := event.PublishedAt()

	if _, err := r.db.Exec(
		ctx,
		query,
		event.EventID(),
		event.AggregateID(),
		event.EventType(),
		event.CorrelationID(),
		nullableStringWhen(causationID, hasCausationID),
		event.Version(),
		event.Payload(),
		event.OccurredAt(),
		event.Attempts(),
		event.NextAttemptAt(),
		nullableTime(publishedAt, published),
	); err != nil {
		return fmt.Errorf("create outbox event %q: %w", event.EventID(), classifyDatabaseError(err))
	}

	return nil
}

func nullableStringWhen(value string, present bool) any {
	if !present {
		return nil
	}

	return value
}

var _ application.OutboxRepository = (*OutboxRepository)(nil)

func (r *OutboxRepository) FindNextDueForUpdate(ctx context.Context, dueAt time.Time) (*domain.OutboxEvent, error) {
	const query = `SELECT event_id::text, aggregate_id::text, event_type, correlation_id,
        causation_id, version, payload, occurred_at, attempts, next_attempt_at, published_at
        FROM outbox_events
        WHERE published_at IS NULL AND next_attempt_at <= $1
        ORDER BY next_attempt_at, event_id
        LIMIT 1 FOR UPDATE SKIP LOCKED`
	var p domain.RehydrateOutboxEventParams
	var eventType string
	var causation pgtype.Text
	var published pgtype.Timestamptz
	if err := r.db.QueryRow(ctx, query, dueAt).Scan(&p.EventID, &p.AggregateID, &eventType, &p.CorrelationID,
		&causation, &p.Version, &p.Payload, &p.OccurredAt, &p.Attempts, &p.NextAttemptAt, &published); err != nil {
		return nil, wrapQueryError("claim due outbox event", err)
	}
	p.EventType = domain.IntegrationEventType(eventType)
	p.CausationID = textValue(causation)
	p.OccurredAt = p.OccurredAt.UTC()
	p.NextAttemptAt = p.NextAttemptAt.UTC()
	if published.Valid {
		timestamp := published.Time.UTC()
		p.PublishedAt = &timestamp
	}
	event, err := domain.RehydrateOutboxEvent(p)
	if err != nil {
		return nil, fmt.Errorf("rehydrate outbox: %w", err)
	}
	return &event, nil
}

// Only delivery fields evolve. The snapshot and envelope are never updated.
func (r *OutboxRepository) UpdateDelivery(ctx context.Context, event *domain.OutboxEvent) error {
	if event == nil {
		return fmt.Errorf("outbox event is required")
	}
	published, hasPublished := event.PublishedAt()
	tag, err := r.db.Exec(ctx, `UPDATE outbox_events
        SET attempts=$2, next_attempt_at=$3, published_at=$4
        WHERE event_id=$1 AND published_at IS NULL`,
		event.EventID(), event.Attempts(), event.NextAttemptAt(), nullableTime(published, hasPublished))
	if err != nil {
		return wrapQueryError("update outbox delivery", err)
	}
	if tag.RowsAffected() != 1 {
		return application.ErrNotFound
	}
	return nil
}
