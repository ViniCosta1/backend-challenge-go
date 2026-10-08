package sqs

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLifecycleStopWaitsForCancelledWorkToExit(t *testing.T) {
	var component lifecycle
	workCancelled := make(chan struct{})
	allowExit := make(chan struct{})
	if err := component.start(context.Background(), func(_ context.Context, work context.Context) {
		<-work.Done()
		close(workCancelled)
		<-allowExit
	}); err != nil {
		t.Fatal(err)
	}
	stopContext, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- component.stop(stopContext) }()
	select {
	case <-workCancelled:
	case <-time.After(time.Second):
		t.Fatal("Stop did not cancel in-flight work")
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned while the owner goroutine could still use dependencies")
	default:
	}
	close(allowExit)
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation result, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the goroutine terminated")
	}
}
