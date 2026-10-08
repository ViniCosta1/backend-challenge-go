package sqs

import (
	"context"
	"errors"
	"sync"
)

var ErrAlreadyStarted = errors.New("component already started")

// lifecycle only coordinates local start/stop, never financial ownership.
// PostgreSQL and SQS remain the durable sources of truth.
type lifecycle struct {
	mu                    sync.Mutex
	running               bool
	stopPolling, stopWork context.CancelFunc
	done                  chan struct{}
}

func (l *lifecycle) start(ctx context.Context, run func(polling, work context.Context)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		return ErrAlreadyStarted
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	polling, stopPolling := context.WithCancel(ctx)
	work, stopWork := context.WithCancel(ctx)
	done := make(chan struct{})
	l.running, l.stopPolling, l.stopWork, l.done = true, stopPolling, stopWork, done
	go func() {
		defer func() {
			stopPolling()
			stopWork()
			l.mu.Lock()
			l.running = false
			close(done)
			l.mu.Unlock()
		}()
		run(polling, work)
	}()
	return nil
}

func (l *lifecycle) stop(ctx context.Context) error {
	l.mu.Lock()
	if !l.running {
		l.mu.Unlock()
		return nil
	}
	stopPolling, stopWork, done := l.stopPolling, l.stopWork, l.done
	l.mu.Unlock()
	// Stop receiving immediately, but leave current work alive to commit/ack.
	stopPolling()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		// Cancellation rolls back uncommitted work. Committed-but-unacked
		// messages remain safe for durable Inbox redelivery.
		stopWork()
		// Every operation receives the work context and has its own bounded
		// timeout. Wait for the owner goroutine to observe cancellation before
		// returning so Fx may safely close PostgreSQL and SQS dependencies.
		<-done
		return ctx.Err()
	}
}

func (l *lifecycle) isRunning() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}
