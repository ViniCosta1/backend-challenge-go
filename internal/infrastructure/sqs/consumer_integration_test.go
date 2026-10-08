package sqs

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

func TestConsumerRealBETRedeliveryAndInboxConflict(t *testing.T) {
	f := realMessaging(t)
	wallet := f.wallet(t, 10000)
	body := requestBody(t, wallet, domain.WagerKindBet, "25.00")
	f.send(t, body)
	// Simulate process loss after SQL commit but before SQS DeleteMessage.
	messages := f.receive(t, f.queues.Input)
	if len(messages) != 1 {
		t.Fatal("message not received")
	}
	input, err := DecodeWagerMessage(aws.ToString(messages[0].Body))
	if err != nil {
		t.Fatal(err)
	}
	processor, err := application.NewProcessWagerMessage(f.manager, "wager-transactions", nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := processor.Execute(f.ctx, input)
	if err != nil || first.Wager.Status != domain.WagerStatusProcessed {
		t.Fatalf("initial processing: %+v %v", first, err)
	}
	f.assertEffects(t, wallet.WalletID, 7500, 1, 1)
	f.release(t, messages[0])
	// A reconstructed consumer resumes solely from persisted Inbox state.
	result, err := f.consumer(t).PollOnce(f.ctx)
	if err != nil || result.Completed != 1 || result.InboxReplays != 1 {
		t.Fatalf("redelivery: %+v %v", result, err)
	}
	f.assertEffects(t, wallet.WalletID, 7500, 1, 1)
	if len(f.receive(t, f.queues.Input)) != 0 {
		t.Fatal("committed message was not deleted")
	}
	var changed wagerRequest
	if err := json.Unmarshal([]byte(body), &changed); err != nil {
		t.Fatal(err)
	}
	changed.Data.Money.Amount = "26.00"
	modified, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := DecodeWagerMessage(string(modified))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Execute(f.ctx, conflict); !errors.Is(err, application.ErrInboxPayloadConflict) {
		t.Fatalf("expected Inbox payload conflict: %v", err)
	}
	// Bypass the broker dedup window deliberately to model a producer bug or
	// malicious reuse of messageId with changed bytes. Inbox is the guard.
	f.sendRaw(t, string(modified), conflict.Wager.WalletID, uuid.NewString())
	result, err = f.consumer(t).PollOnce(f.ctx)
	if err != nil || result.Invalid != 1 || result.Completed != 0 {
		t.Fatalf("changed envelope must be permanent: %+v %v", result, err)
	}
	f.assertEffects(t, wallet.WalletID, 7500, 1, 1)
}

func TestConsumerNormalTerminalAndPendingResults(t *testing.T) {
	f := realMessaging(t)
	wallet := f.wallet(t, 10000)
	consumer := f.consumer(t)
	for _, test := range []struct {
		kind   domain.WagerKind
		amount string
		status domain.WagerStatus
	}{
		{domain.WagerKindBet, "25.00", domain.WagerStatusProcessed},
		{domain.WagerKindBet, "100.00", domain.WagerStatusRejected},
		{domain.WagerKindLoss, "0.00", domain.WagerStatusProcessed},
		{domain.WagerKindRefund, "25.00", domain.WagerStatusPendingReference},
	} {
		body := requestBody(t, wallet, test.kind, test.amount)
		var envelope wagerRequest
		if err := json.Unmarshal([]byte(body), &envelope); err != nil {
			t.Fatal(err)
		}
		if test.kind == domain.WagerKindRefund {
			envelope.Data.ReferenceExternalTransactionID = "late-bet"
			data, _ := json.Marshal(envelope)
			body = string(data)
		}
		f.send(t, body)
		result, err := consumer.PollOnce(f.ctx)
		if err != nil || result.Completed != 1 {
			t.Fatalf("consumer result: %+v %v", result, err)
		}
		if count := f.count(t, "SELECT count(*) FROM wager_transactions WHERE external_transaction_id=$1 AND status=$2", envelope.Data.ExternalTransactionID, test.status); count != 1 {
			t.Fatal("terminal/durable status not persisted")
		}
		if len(f.receive(t, f.queues.Input)) != 0 {
			t.Fatal("durable result was not acknowledged")
		}
	}
	f.assertEffects(t, wallet.WalletID, 7500, 1, 4)
	// A new financial processor and retry service recover the pending reference.
	input, err := DecodeWagerMessage(requestBody(t, wallet, domain.WagerKindBet, "25.00"))
	if err != nil {
		t.Fatal(err)
	}
	input.Wager.ExternalTransactionID = "late-bet"
	if _, err := application.NewProcessWagerTransaction(f.manager, nil).Execute(f.ctx, input.Wager); err != nil {
		t.Fatal(err)
	}
	retried, err := application.NewRetryPendingReferences(f.manager, nil).Execute(f.ctx, time.Now().UTC().Add(2*time.Second), 10)
	if err != nil || retried != 1 {
		t.Fatalf("pending restart: %d %v", retried, err)
	}
	f.assertEffects(t, wallet.WalletID, 7500, 2, 4)
}

func TestConsumerFailureBeforeCommitRollsBackEverything(t *testing.T) {
	f := realMessaging(t)
	wallet := f.wallet(t, 10000)
	body := requestBody(t, wallet, domain.WagerKindBet, "25.00")
	f.send(t, body)
	// The final Inbox update fails AFTER wallet/ledger/outbox writes, proving
	// all those writes share the same real PostgreSQL transaction.
	_, err := f.pool.Exec(f.ctx, `CREATE FUNCTION fail_inbox_completion() RETURNS trigger LANGUAGE plpgsql AS $$
        BEGIN RAISE EXCEPTION 'injected failure at Inbox completion'; END; $$;
        CREATE TRIGGER test_fail_inbox BEFORE UPDATE ON inbox_messages
        FOR EACH ROW EXECUTE FUNCTION fail_inbox_completion();`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.consumer(t).PollOnce(f.ctx)
	if err != nil || result.Retried != 1 || result.Completed != 0 {
		t.Fatalf("failure: %+v %v", result, err)
	}
	f.assertEffects(t, wallet.WalletID, 10000, 0, 0)
	if f.count(t, "SELECT count(*) FROM inbox_messages") != 0 || f.count(t, "SELECT count(*) FROM wager_transactions WHERE kind <> 'OPENING'") != 0 || f.count(t, "SELECT count(*) FROM outbox_events") != 2 {
		t.Fatal("partial financial/Inbox state survived rollback")
	}
	if _, err := f.pool.Exec(f.ctx, "DROP TRIGGER test_fail_inbox ON inbox_messages; DROP FUNCTION fail_inbox_completion()"); err != nil {
		t.Fatal(err)
	}
	// Respect the first 1-second visibility retry; no new broker message.
	deadline := time.Now().Add(5 * time.Second)
	for {
		result, err = f.consumer(t).PollOnce(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result.Completed == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unacked message did not retry")
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.assertEffects(t, wallet.WalletID, 7500, 1, 1)
}

func TestInvalidMessageReachesRealSQSRedriveDLQ(t *testing.T) {
	f := realMessaging(t)
	f.sendRaw(t, `{"type":"invalid"}`, uuid.NewString(), uuid.NewString())
	consumer := f.consumer(t)
	deadline := time.Now().Add(45 * time.Second)
	invalid := 0
	for time.Now().Before(deadline) {
		result, err := consumer.PollOnce(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		invalid += result.Invalid
		messages := f.receive(t, f.queues.DLQ)
		if len(messages) > 0 {
			if aws.ToString(messages[0].Body) != `{"type":"invalid"}` || invalid != 5 {
				t.Fatalf("unexpected redrive/body, invalid=%d", invalid)
			}
			if f.count(t, "SELECT count(*) FROM inbox_messages") != 0 || f.count(t, "SELECT count(*) FROM wager_transactions") != 0 {
				t.Fatal("invalid input left financial state")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("SQS did not redrive after five deliveries, invalid=%d", invalid)
}

func TestSQSReadinessChecksAllRealQueues(t *testing.T) {
	f := realMessaging(t)
	checker := NewReadinessChecker(f.client, f.config)
	if err := checker.Check(f.ctx); err != nil {
		t.Fatal(err)
	}
	// Resolve by name just as cmd/api does, without cached queue URLs.
	byName := f.config
	byName.Input.URL, byName.DLQ.URL, byName.Events.URL = "", "", ""
	if err := NewReadinessChecker(f.client, byName).Check(f.ctx); err != nil {
		t.Fatal(err)
	}
	byName.Events.Name = "missing-" + uuid.NewString() + ".fifo"
	if err := NewReadinessChecker(f.client, byName).Check(f.ctx); err == nil {
		t.Fatal("readiness ignored missing output queue")
	}
}
