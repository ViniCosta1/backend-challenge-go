package domain

import (
	"errors"
	"time"
)

var (
	ErrIntegrationEventIDRequired = errors.New(
		"integration event id is required",
	)
	ErrIntegrationAggregateIDRequired = errors.New(
		"integration event aggregate id is required",
	)
	ErrIntegrationCorrelationIDRequired = errors.New(
		"integration event correlation id is required",
	)
	ErrIntegrationTransactionIDRequired = errors.New(
		"integration event transaction id is required",
	)
	ErrIntegrationWalletIDRequired = errors.New(
		"integration event wallet id is required",
	)
	ErrIntegrationProviderIDRequired = errors.New(
		"integration event provider id is required",
	)
	ErrIntegrationExternalTransactionIDRequired = errors.New(
		"integration event external transaction id is required",
	)
	ErrInvalidIntegrationEventPayload = errors.New(
		"invalid integration event payload",
	)
)

type IntegrationEventEnvelope struct {
	eventID       string
	eventType     IntegrationEventType
	aggregateID   string
	correlationID string
	causationID   string
	occurredAt    time.Time
	version       int32
}

func newIntegrationEventEnvelope(
	eventID string,
	eventType IntegrationEventType,
	aggregateID string,
	correlationID string,
	causationID string,
) (IntegrationEventEnvelope, error) {
	if eventID == "" {
		return IntegrationEventEnvelope{}, ErrIntegrationEventIDRequired
	}

	if !isValidIntegrationEventType(eventType) {
		return IntegrationEventEnvelope{}, ErrInvalidIntegrationEventType
	}

	if aggregateID == "" {
		return IntegrationEventEnvelope{}, ErrIntegrationAggregateIDRequired
	}

	if correlationID == "" {
		return IntegrationEventEnvelope{}, ErrIntegrationCorrelationIDRequired
	}

	return IntegrationEventEnvelope{
		eventID:       eventID,
		eventType:     eventType,
		aggregateID:   aggregateID,
		correlationID: correlationID,
		causationID:   causationID,
		occurredAt:    time.Now().UTC(),
		version:       integrationEventVersion,
	}, nil
}

// Getters

func (e IntegrationEventEnvelope) EventID() string {
	return e.eventID
}

func (e IntegrationEventEnvelope) EventType() IntegrationEventType {
	return e.eventType
}

func (e IntegrationEventEnvelope) AggregateID() string {
	return e.aggregateID
}

func (e IntegrationEventEnvelope) CorrelationID() string {
	return e.correlationID
}

func (e IntegrationEventEnvelope) CausationID() (string, bool) {
	return e.causationID, e.causationID != ""
}

func (e IntegrationEventEnvelope) OccurredAt() time.Time {
	return e.occurredAt
}

func (e IntegrationEventEnvelope) Version() int32 {
	return e.version
}
