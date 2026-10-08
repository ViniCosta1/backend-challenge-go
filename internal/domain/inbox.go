package domain

import (
	"errors"
	"time"
)

var (
	ErrInboxConsumerNameRequired = errors.New("inbox consumer name is required")
	ErrInboxMessageIDRequired    = errors.New("inbox message id is required")
	ErrInboxPayloadHashRequired  = errors.New("inbox payload hash is required")
	ErrInvalidInboxReceivedAt    = errors.New("invalid inbox received at")
	ErrInvalidInboxCompletedAt   = errors.New("invalid inbox completed at")
)

type InboxMessage struct {
	consumerName string
	messageID    string
	payloadHash  string
	receivedAt   time.Time
	completedAt  *time.Time
}

func NewInboxMessage(
	consumerName string,
	messageID string,
	payloadHash string,
) (InboxMessage, error) {
	if err := validateInboxIdentity(consumerName, messageID, payloadHash); err != nil {
		return InboxMessage{}, err
	}

	return InboxMessage{
		consumerName: consumerName,
		messageID:    messageID,
		payloadHash:  payloadHash,
		receivedAt:   time.Now().UTC(),
	}, nil
}

type RehydrateInboxMessageParams struct {
	ConsumerName string
	MessageID    string
	PayloadHash  string
	ReceivedAt   time.Time
	CompletedAt  *time.Time
}

func RehydrateInboxMessage(
	p RehydrateInboxMessageParams,
) (InboxMessage, error) {
	if err := validateInboxIdentity(
		p.ConsumerName,
		p.MessageID,
		p.PayloadHash,
	); err != nil {
		return InboxMessage{}, err
	}

	if p.ReceivedAt.IsZero() || p.ReceivedAt.Location() != time.UTC {
		return InboxMessage{}, ErrInvalidInboxReceivedAt
	}

	completedAt, err := validateAndCopyInboxCompletedAt(
		p.ReceivedAt,
		p.CompletedAt,
	)
	if err != nil {
		return InboxMessage{}, err
	}

	return InboxMessage{
		consumerName: p.ConsumerName,
		messageID:    p.MessageID,
		payloadHash:  p.PayloadHash,
		receivedAt:   p.ReceivedAt,
		completedAt:  completedAt,
	}, nil
}

func validateInboxIdentity(
	consumerName string,
	messageID string,
	payloadHash string,
) error {
	if consumerName == "" {
		return ErrInboxConsumerNameRequired
	}
	if messageID == "" {
		return ErrInboxMessageIDRequired
	}
	if payloadHash == "" {
		return ErrInboxPayloadHashRequired
	}

	return nil
}

func validateAndCopyInboxCompletedAt(
	receivedAt time.Time,
	completedAt *time.Time,
) (*time.Time, error) {
	if completedAt == nil {
		return nil, nil
	}

	if completedAt.IsZero() ||
		completedAt.Location() != time.UTC ||
		completedAt.Before(receivedAt) {
		return nil, ErrInvalidInboxCompletedAt
	}

	completedAtCopy := *completedAt
	return &completedAtCopy, nil
}

func (m *InboxMessage) MarkCompleted() error {
	if m.completedAt != nil {
		return nil
	}

	completedAt := time.Now().UTC()
	if completedAt.Before(m.receivedAt) {
		return ErrInvalidInboxCompletedAt
	}

	m.completedAt = &completedAt
	return nil
}

func (m *InboxMessage) IsCompleted() bool {
	return m.completedAt != nil
}

func (m *InboxMessage) ConsumerName() string {
	return m.consumerName
}

func (m *InboxMessage) MessageID() string {
	return m.messageID
}

func (m *InboxMessage) PayloadHash() string {
	return m.payloadHash
}

func (m *InboxMessage) ReceivedAt() time.Time {
	return m.receivedAt
}

func (m *InboxMessage) CompletedAt() (time.Time, bool) {
	if m.completedAt == nil {
		return time.Time{}, false
	}

	return *m.completedAt, true
}
