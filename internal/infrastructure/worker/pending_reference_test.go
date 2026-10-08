package worker

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPendingReferenceSettingsRejectInvalidValues(t *testing.T) {
	if _, err := NewPendingReference(nil, nil, nil, PendingReferenceSettings{PollInterval: time.Second, OperationTimeout: time.Second, BatchSize: 1}); err == nil {
		t.Fatal("expected nil use case to be rejected")
	}
}

func TestPendingReferenceStopWaitsForCancelledCycle(t *testing.T) {
	_, stopPolling := context.WithCancel(context.Background())
	work, stopWork := context.WithCancel(context.Background())
	done := make(chan struct{})
	allowExit := make(chan struct{})
	worker := &PendingReference{running: true, stopPolling: stopPolling, stopWork: stopWork, done: done}
	go func() {
		<-work.Done()
		<-allowExit
		worker.mu.Lock()
		worker.running = false
		close(done)
		worker.mu.Unlock()
	}()
	stopContext, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Stop(stopContext) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while the pending cycle could still use dependencies")
	case <-time.After(20 * time.Millisecond):
	}
	close(allowExit)
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation result, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not wait for the pending cycle")
	}
}
