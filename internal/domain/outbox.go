package domain

import (
	"errors"
	"time"
)

const maxOutboxAttempts int32 = 1<<31 - 1

var (
	ErrOutboxEventIDRequired     = errors.New("outbox event id is required")
	ErrOutboxAggregateIDRequired = errors.New(
		"outbox aggregate id is required",
	)
	ErrOutboxCorrelationIDRequired = errors.New(
		"outbox correlation id is required",
	)
	ErrOutboxPayloadRequired    = errors.New("outbox payload is required")
	ErrInvalidOutboxVersion     = errors.New("invalid outbox event version")
	ErrInvalidOutboxOccurredAt  = errors.New("invalid outbox occurred at")
	ErrInvalidOutboxAttempts    = errors.New("invalid outbox attempts")
	ErrInvalidOutboxNextAttempt = errors.New("invalid outbox next attempt at")
	ErrInvalidOutboxPublishedAt = errors.New("invalid outbox published at")
	ErrOutboxAlreadyPublished   = errors.New("outbox event is already published")
	ErrOutboxAttemptRequired    = errors.New("outbox attempt is required")
	ErrOutboxAttemptsExhausted  = errors.New("outbox attempts exhausted")
)

type OutboxEvent struct {
	eventID       string
	aggregateID   string
	eventType     IntegrationEventType
	version       int32
	payload       []byte
	correlationID string
	causationID   string
	occurredAt    time.Time
	attempts      int32
	nextAttemptAt time.Time
	publishedAt   *time.Time
}

func NewOutboxEvent(
	envelope IntegrationEventEnvelope,
	payload []byte,
) (OutboxEvent, error) {
	if err := validateOutboxImmutableFields(
		envelope.eventID,
		envelope.aggregateID,
		envelope.eventType,
		envelope.version,
		payload,
		envelope.correlationID,
		envelope.occurredAt,
	); err != nil {
		return OutboxEvent{}, err
	}

	return OutboxEvent{
		eventID:       envelope.eventID,
		aggregateID:   envelope.aggregateID,
		eventType:     envelope.eventType,
		version:       envelope.version,
		payload:       copyBytes(payload),
		correlationID: envelope.correlationID,
		causationID:   envelope.causationID,
		occurredAt:    envelope.occurredAt,
		nextAttemptAt: envelope.occurredAt,
	}, nil
}

type RehydrateOutboxEventParams struct {
	EventID       string
	AggregateID   string
	EventType     IntegrationEventType
	Version       int32
	Payload       []byte
	CorrelationID string
	CausationID   string
	OccurredAt    time.Time
	Attempts      int32
	NextAttemptAt time.Time
	PublishedAt   *time.Time
}

func RehydrateOutboxEvent(
	p RehydrateOutboxEventParams,
) (OutboxEvent, error) {
	if err := validateOutboxImmutableFields(
		p.EventID,
		p.AggregateID,
		p.EventType,
		p.Version,
		p.Payload,
		p.CorrelationID,
		p.OccurredAt,
	); err != nil {
		return OutboxEvent{}, err
	}

	if p.Attempts < 0 {
		return OutboxEvent{}, ErrInvalidOutboxAttempts
	}

	if p.NextAttemptAt.IsZero() ||
		p.NextAttemptAt.Location() != time.UTC ||
		p.NextAttemptAt.Before(p.OccurredAt) {
		return OutboxEvent{}, ErrInvalidOutboxNextAttempt
	}

	publishedAt, err := validateAndCopyOutboxPublishedAt(
		p.OccurredAt,
		p.PublishedAt,
	)
	if err != nil {
		return OutboxEvent{}, err
	}

	return OutboxEvent{
		eventID:       p.EventID,
		aggregateID:   p.AggregateID,
		eventType:     p.EventType,
		version:       p.Version,
		payload:       copyBytes(p.Payload),
		correlationID: p.CorrelationID,
		causationID:   p.CausationID,
		occurredAt:    p.OccurredAt,
		attempts:      p.Attempts,
		nextAttemptAt: p.NextAttemptAt,
		publishedAt:   publishedAt,
	}, nil
}

func validateOutboxImmutableFields(
	eventID string,
	aggregateID string,
	eventType IntegrationEventType,
	version int32,
	payload []byte,
	correlationID string,
	occurredAt time.Time,
) error {
	if eventID == "" {
		return ErrOutboxEventIDRequired
	}
	if aggregateID == "" {
		return ErrOutboxAggregateIDRequired
	}
	if !isValidIntegrationEventType(eventType) {
		return ErrInvalidIntegrationEventType
	}
	if version < 1 {
		return ErrInvalidOutboxVersion
	}
	if len(payload) == 0 {
		return ErrOutboxPayloadRequired
	}
	if correlationID == "" {
		return ErrOutboxCorrelationIDRequired
	}
	if occurredAt.IsZero() || occurredAt.Location() != time.UTC {
		return ErrInvalidOutboxOccurredAt
	}

	return nil
}

func validateAndCopyOutboxPublishedAt(
	occurredAt time.Time,
	publishedAt *time.Time,
) (*time.Time, error) {
	if publishedAt == nil {
		return nil, nil
	}

	if publishedAt.IsZero() ||
		publishedAt.Location() != time.UTC ||
		publishedAt.Before(occurredAt) {
		return nil, ErrInvalidOutboxPublishedAt
	}

	publishedAtCopy := *publishedAt
	return &publishedAtCopy, nil
}

func (e *OutboxEvent) RegisterAttempt() error {
	if e.publishedAt != nil {
		return ErrOutboxAlreadyPublished
	}
	if e.attempts == maxOutboxAttempts {
		return ErrOutboxAttemptsExhausted
	}

	e.attempts++
	return nil
}

func (e *OutboxEvent) ScheduleRetry(nextAttemptAt time.Time) error {
	if e.publishedAt != nil {
		return ErrOutboxAlreadyPublished
	}
	if e.attempts == 0 {
		return ErrOutboxAttemptRequired
	}
	if nextAttemptAt.IsZero() ||
		nextAttemptAt.Location() != time.UTC ||
		!nextAttemptAt.After(e.occurredAt) {
		return ErrInvalidOutboxNextAttempt
	}

	e.nextAttemptAt = nextAttemptAt
	return nil
}

func (e *OutboxEvent) MarkPublished() error {
	if e.publishedAt != nil {
		return nil
	}

	publishedAt := time.Now().UTC()
	if publishedAt.Before(e.occurredAt) {
		return ErrInvalidOutboxPublishedAt
	}

	e.publishedAt = &publishedAt
	return nil
}

func (e *OutboxEvent) IsPublished() bool {
	return e.publishedAt != nil
}

func (e *OutboxEvent) EventID() string {
	return e.eventID
}

func (e *OutboxEvent) AggregateID() string {
	return e.aggregateID
}

func (e *OutboxEvent) EventType() IntegrationEventType {
	return e.eventType
}

func (e *OutboxEvent) Version() int32 {
	return e.version
}

func (e *OutboxEvent) Payload() []byte {
	return copyBytes(e.payload)
}

func (e *OutboxEvent) CorrelationID() string {
	return e.correlationID
}

func (e *OutboxEvent) CausationID() (string, bool) {
	return e.causationID, e.causationID != ""
}

func (e *OutboxEvent) OccurredAt() time.Time {
	return e.occurredAt
}

func (e *OutboxEvent) Attempts() int32 {
	return e.attempts
}

func (e *OutboxEvent) NextAttemptAt() time.Time {
	return e.nextAttemptAt
}

func (e *OutboxEvent) PublishedAt() (time.Time, bool) {
	if e.publishedAt == nil {
		return time.Time{}, false
	}

	return *e.publishedAt, true
}

func copyBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}
