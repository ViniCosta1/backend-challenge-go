package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type InboxRepository struct {
	db DBTX
}

func NewInboxRepository(db DBTX) *InboxRepository {
	return &InboxRepository{db: db}
}

func (r *InboxRepository) Create(
	ctx context.Context,
	message *domain.InboxMessage,
) error {
	if message == nil {
		return fmt.Errorf("create inbox message: message is required")
	}

	const query = `
INSERT INTO inbox_messages (
    consumer_name,
    message_id,
    payload_hash,
    received_at,
    completed_at
) VALUES ($1, $2, $3, $4, $5)`

	completedAt, completed := message.CompletedAt()
	if _, err := r.db.Exec(
		ctx,
		query,
		message.ConsumerName(),
		message.MessageID(),
		message.PayloadHash(),
		message.ReceivedAt(),
		nullableTime(completedAt, completed),
	); err != nil {
		return fmt.Errorf(
			"create inbox message %q/%q: %w",
			message.ConsumerName(),
			message.MessageID(),
			classifyDatabaseError(err),
		)
	}

	return nil
}

func (r *InboxRepository) FindByConsumerAndMessageID(
	ctx context.Context,
	consumerName string,
	messageID string,
) (*domain.InboxMessage, error) {
	return r.find(ctx, consumerName, messageID, false)
}

func (r *InboxRepository) FindByConsumerAndMessageIDForUpdate(ctx context.Context, consumerName, messageID string) (*domain.InboxMessage, error) {
	return r.find(ctx, consumerName, messageID, true)
}

func (r *InboxRepository) find(ctx context.Context, consumerName, messageID string, forUpdate bool) (*domain.InboxMessage, error) {
	query := `
SELECT
    consumer_name,
    message_id,
    payload_hash,
    received_at,
    completed_at
FROM inbox_messages
WHERE consumer_name = $1 AND message_id = $2`
	if forUpdate {
		query += " FOR UPDATE"
	}

	var (
		persistedConsumerName string
		persistedMessageID    string
		payloadHash           string
		receivedAt            time.Time
		completedAt           pgtype.Timestamptz
	)

	resource := fmt.Sprintf(
		"find inbox message %q/%q",
		consumerName,
		messageID,
	)
	if err := r.db.QueryRow(ctx, query, consumerName, messageID).Scan(
		&persistedConsumerName,
		&persistedMessageID,
		&payloadHash,
		&receivedAt,
		&completedAt,
	); err != nil {
		return nil, wrapQueryError(resource, err)
	}

	var completedAtUTC *time.Time
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		completedAtUTC = &value
	}

	message, err := domain.RehydrateInboxMessage(
		domain.RehydrateInboxMessageParams{
			ConsumerName: persistedConsumerName,
			MessageID:    persistedMessageID,
			PayloadHash:  payloadHash,
			ReceivedAt:   receivedAt.UTC(),
			CompletedAt:  completedAtUTC,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%s: rehydrate: %w", resource, err)
	}

	return &message, nil
}

var _ application.InboxRepository = (*InboxRepository)(nil)

func (r *InboxRepository) CreateIfAbsent(ctx context.Context, message *domain.InboxMessage) (bool, error) {
	if message == nil {
		return false, fmt.Errorf("inbox message is required")
	}
	completed, hasCompleted := message.CompletedAt()
	tag, err := r.db.Exec(ctx, `INSERT INTO inbox_messages
        (consumer_name, message_id, payload_hash, received_at, completed_at)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (consumer_name, message_id) DO NOTHING`,
		message.ConsumerName(), message.MessageID(), message.PayloadHash(), message.ReceivedAt(), nullableTime(completed, hasCompleted))
	if err != nil {
		return false, wrapQueryError("reserve inbox message", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (r *InboxRepository) Complete(ctx context.Context, message *domain.InboxMessage) error {
	if message == nil || !message.IsCompleted() {
		return domain.ErrInvalidInboxCompletedAt
	}
	completed, _ := message.CompletedAt()
	tag, err := r.db.Exec(ctx, `UPDATE inbox_messages
        SET completed_at = COALESCE(completed_at, $4)
        WHERE consumer_name = $1 AND message_id = $2 AND payload_hash = $3`,
		message.ConsumerName(), message.MessageID(), message.PayloadHash(), completed)
	if err != nil {
		return wrapQueryError("complete inbox message", err)
	}
	if tag.RowsAffected() != 1 {
		return application.ErrNotFound
	}
	return nil
}
