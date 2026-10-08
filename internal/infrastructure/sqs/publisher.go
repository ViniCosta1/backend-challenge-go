package sqs

import (
	"context"
	"log/slog"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
)

// Publisher polls small batches; delivery state and ownership live in SQL.
type Publisher struct {
	lifecycle                      lifecycle
	useCase                        *application.PublishOutbox
	logger                         *slog.Logger
	metrics                        PublisherMetrics
	pollInterval, operationTimeout time.Duration
}

type PublisherMetrics interface {
	ObserveOutbox(published, failed int)
}

type PublisherOption func(*Publisher)

func WithPublisherSettings(pollInterval, operationTimeout time.Duration) PublisherOption {
	return func(publisher *Publisher) {
		publisher.pollInterval = pollInterval
		publisher.operationTimeout = operationTimeout
	}
}

func WithPublisherMetrics(metrics PublisherMetrics) PublisherOption {
	return func(publisher *Publisher) { publisher.metrics = metrics }
}

func NewPublisher(useCase *application.PublishOutbox, logger *slog.Logger, options ...PublisherOption) *Publisher {
	if logger == nil {
		logger = slog.Default()
	}
	publisher := &Publisher{useCase: useCase, logger: logger, pollInterval: time.Second, operationTimeout: 15 * time.Second}
	for _, option := range options {
		option(publisher)
	}
	return publisher
}

func (p *Publisher) Start(ctx context.Context) error {
	return p.lifecycle.start(ctx, func(polling, work context.Context) {
		for polling.Err() == nil {
			// One event per pass prevents Stop from claiming a new event while
			// the current external delivery is draining.
			operation, cancel := context.WithTimeout(work, p.operationTimeout)
			result, err := p.useCase.Execute(operation, time.Now().UTC(), 1)
			cancel()
			if err != nil {
				p.logger.ErrorContext(work, "outbox publisher pass failed", "error", err)
			}
			if result.Failed > 0 {
				p.logger.WarnContext(work, "outbox delivery failed; retry persisted", "failed", result.Failed,
					"eventId", result.LastEventID)
			}
			if p.metrics != nil {
				p.metrics.ObserveOutbox(result.Published, result.Failed)
			}
			if result.Published > 0 {
				p.logger.InfoContext(work, "outbox event published", "published", result.Published,
					"eventId", result.LastEventID)
			}
			if err != nil || result.Published == 0 {
				if !pause(polling, p.pollInterval) {
					return
				}
			}
		}
	})
}

func (p *Publisher) Stop(ctx context.Context) error { return p.lifecycle.stop(ctx) }

func (p *Publisher) Running() bool { return p.lifecycle.isRunning() }
