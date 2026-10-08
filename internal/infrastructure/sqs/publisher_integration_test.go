package sqs

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type observedSender struct {
	actual  *EventSender
	mu      sync.Mutex
	ids     []string
	entered chan string
	release <-chan struct{}
}

func (s *observedSender) Publish(ctx context.Context, event *domain.OutboxEvent) error {
	if s.entered != nil {
		select {
		case s.entered <- event.EventID():
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := s.actual.Publish(ctx, event); err != nil {
		return err
	}
	s.mu.Lock()
	s.ids = append(s.ids, event.EventID())
	s.mu.Unlock()
	return nil
}

func TestOutboxPublishesPersistedSnapshotsToRealSQS(t *testing.T) {
	f := realMessaging(t)
	f.wallet(t, 10000)
	useCase := application.NewPublishOutbox(f.manager, NewEventSender(f.client, f.queues.Events))
	result, err := useCase.Execute(f.ctx, time.Now().UTC(), 10)
	if err != nil || result.Published != 2 || result.Failed != 0 {
		t.Fatalf("publish: %+v %v", result, err)
	}
	if f.count(t, "SELECT count(*) FROM outbox_events WHERE published_at IS NOT NULL AND attempts=1") != 2 {
		t.Fatal("published state was not persisted")
	}
	messages := f.receive(t, f.queues.Events)
	if len(messages) != 2 {
		t.Fatalf("expected two integration events, got %d", len(messages))
	}
	for _, message := range messages {
		var envelope struct {
			EventID string          `json:"eventId"`
			Data    json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(aws.ToString(message.Body)), &envelope); err != nil {
			t.Fatal(err)
		}
		var matches bool
		if err := f.pool.QueryRow(f.ctx, "SELECT payload=$2::jsonb FROM outbox_events WHERE event_id=$1", envelope.EventID, aws.ToString(message.Body)).Scan(&matches); err != nil || !matches {
			t.Fatalf("snapshot changed: %v", err)
		}
	}
}

func TestTwoPublishersSkipLockedAndNeverShareAnOwnedRow(t *testing.T) {
	f := realMessaging(t)
	f.wallet(t, 10000)
	entered := make(chan string, 2)
	release := make(chan struct{})
	observed := &observedSender{actual: NewEventSender(f.client, f.queues.Events), entered: entered, release: release}
	var group sync.WaitGroup
	errs := make(chan error, 2)
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := application.NewPublishOutbox(f.manager, observed).Execute(f.ctx, time.Now().UTC(), 1)
			if err == nil && result.Published != 1 {
				err = errors.New("publisher did not claim one row")
			}
			errs <- err
		}()
	}
	var ids [2]string
	for index := range ids {
		select {
		case ids[index] = <-entered:
		case <-time.After(3 * time.Second):
			close(release)
			group.Wait()
			t.Fatal("second publisher blocked instead of SKIP LOCKED")
		}
	}
	if ids[0] == ids[1] {
		close(release)
		group.Wait()
		t.Fatal("publishers simultaneously claimed the same row")
	}
	close(release)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if f.count(t, "SELECT count(*) FROM outbox_events WHERE published_at IS NOT NULL") != 2 {
		t.Fatal("concurrent delivery state incomplete")
	}
	if len(f.receive(t, f.queues.Events)) != 2 {
		t.Fatal("concurrent publication lost an event")
	}
}

func TestOutboxPublishBeforeMarkRecoveryKeepsEventIdentity(t *testing.T) {
	f := realMessaging(t)
	f.wallet(t, 10000)
	_, err := f.pool.Exec(f.ctx, `CREATE FUNCTION fail_publication_mark() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN RAISE EXCEPTION 'simulated crash after send before publication mark'; END; $$;
        CREATE TRIGGER test_crash_before_mark BEFORE UPDATE ON outbox_events
        FOR EACH ROW EXECUTE FUNCTION fail_publication_mark();`)
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedSender{actual: NewEventSender(f.client, f.queues.Events)}
	if _, err := application.NewPublishOutbox(f.manager, observed).Execute(f.ctx, time.Now().UTC(), 1); err == nil {
		t.Fatal("simulated failure did not abort publication transaction")
	}
	if len(observed.ids) != 1 || f.count(t, "SELECT count(*) FROM outbox_events WHERE published_at IS NOT NULL") != 0 {
		t.Fatal("event not sent or crash falsely marked publication")
	}
	firstID := observed.ids[0]
	if f.count(t, "SELECT count(*) FROM outbox_events WHERE attempts=0") != 2 {
		t.Fatal("crashed transaction persisted delivery state")
	}
	if _, err := f.pool.Exec(f.ctx, "DROP TRIGGER test_crash_before_mark ON outbox_events; DROP FUNCTION fail_publication_mark()"); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the service: pending state is discovered only from PostgreSQL.
	result, err := application.NewPublishOutbox(f.manager, observed).Execute(f.ctx, time.Now().UTC(), 10)
	if err != nil || result.Published != 2 {
		t.Fatalf("restart: %+v %v", result, err)
	}
	if len(observed.ids) != 3 || observed.ids[1] != firstID {
		t.Fatalf("republication changed event ID: %v", observed.ids)
	}
	// SQS may suppress the resend in its FIFO dedup window; the test proves
	// the second actual SendMessage uses the same durable ID either way.
	if f.count(t, "SELECT count(*) FROM outbox_events WHERE published_at IS NOT NULL") != 2 {
		t.Fatal("recovered publisher did not persist completion")
	}
}

func TestOutboxSQSTransientFailurePersistsBackoffAndRestart(t *testing.T) {
	f := realMessaging(t)
	f.wallet(t, 10000)
	brokenConfig := f.config
	brokenConfig.Endpoint = "http://127.0.0.1:1"
	broken, err := NewClient(f.ctx, brokenConfig)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	result, err := application.NewPublishOutbox(f.manager, NewEventSender(broken, f.queues.Events)).Execute(f.ctx, now, 1)
	if err != nil || result.Failed != 1 || result.Published != 0 {
		t.Fatalf("failure/backoff: %+v %v", result, err)
	}
	if f.count(t, "SELECT count(*) FROM outbox_events WHERE attempts=1 AND published_at IS NULL AND next_attempt_at>$1", now) != 1 {
		t.Fatal("retry/backoff was not durably recorded")
	}
	result, err = application.NewPublishOutbox(f.manager, NewEventSender(f.client, f.queues.Events)).Execute(f.ctx, time.Now().UTC().Add(2*time.Second), 10)
	if err != nil || result.Published != 2 {
		t.Fatalf("restart retry: %+v %v", result, err)
	}
	if f.count(t, "SELECT count(*) FROM outbox_events WHERE attempts=2 AND published_at IS NOT NULL") != 1 {
		t.Fatal("delivery attempt counter did not survive restart")
	}
}
