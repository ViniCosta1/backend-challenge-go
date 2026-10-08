package worker

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
)

type PendingReferenceMetrics interface {
	RecordRetry(component string, count int)
}

type PendingReferenceSettings struct {
	PollInterval     time.Duration
	OperationTimeout time.Duration
	BatchSize        int
}

type PendingReference struct {
	useCase  *application.RetryPendingReferences
	logger   *slog.Logger
	metrics  PendingReferenceMetrics
	settings PendingReferenceSettings

	mu                    sync.Mutex
	running               bool
	stopPolling, stopWork context.CancelFunc
	done                  chan struct{}
}

func NewPendingReference(useCase *application.RetryPendingReferences, logger *slog.Logger,
	metrics PendingReferenceMetrics, settings PendingReferenceSettings) (*PendingReference, error) {
	if useCase == nil || settings.PollInterval <= 0 || settings.OperationTimeout <= 0 || settings.BatchSize <= 0 {
		return nil, errors.New("pending-reference worker requires use case and positive settings")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PendingReference{useCase: useCase, logger: logger, metrics: metrics, settings: settings}, nil
}

func (w *PendingReference) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return errors.New("pending-reference worker already started")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	polling, stopPolling := context.WithCancel(ctx)
	work, stopWork := context.WithCancel(ctx)
	done := make(chan struct{})
	w.running, w.stopPolling, w.stopWork, w.done = true, stopPolling, stopWork, done
	go w.run(polling, work, done)
	return nil
}

func (w *PendingReference) run(polling, work context.Context, done chan struct{}) {
	defer func() {
		w.stopPolling()
		w.stopWork()
		w.mu.Lock()
		w.running = false
		close(done)
		w.mu.Unlock()
	}()
	for polling.Err() == nil {
		operation, cancel := context.WithTimeout(work, w.settings.OperationTimeout)
		attempted, err := w.useCase.Execute(operation, time.Now().UTC(), w.settings.BatchSize)
		cancel()
		if err != nil && work.Err() == nil {
			w.logger.ErrorContext(work, "pending-reference cycle failed", "error", err)
		}
		if attempted > 0 {
			if w.metrics != nil {
				w.metrics.RecordRetry("pending_reference", attempted)
			}
			w.logger.InfoContext(work, "pending-reference cycle completed", "attempted", attempted)
		}
		timer := time.NewTimer(w.settings.PollInterval)
		select {
		case <-polling.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *PendingReference) Stop(ctx context.Context) error {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return nil
	}
	stopPolling, stopWork, done := w.stopPolling, w.stopWork, w.done
	w.mu.Unlock()
	stopPolling()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		stopWork()
		// The in-flight database pass uses the cancelled work context. Do not
		// release the Fx hook until it has exited and can no longer touch pgx.
		<-done
		return ctx.Err()
	}
}

func (w *PendingReference) Running() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
}
