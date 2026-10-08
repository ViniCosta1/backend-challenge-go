package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadValidConfiguration(t *testing.T) {
	values := requiredValues()
	values["SQS_RECEIVE_WAIT"] = "10s"
	values["PENDING_REFERENCE_BATCH_SIZE"] = "7"
	config, err := load(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if config.SQS.ReceiveWait != 10*time.Second || config.Workers.PendingReferenceBatchSize != 7 {
		t.Fatalf("unexpected parsed configuration: %+v", config)
	}
}

func TestLoadReportsMissingAndInvalidConfiguration(t *testing.T) {
	values := requiredValues()
	delete(values, "DATABASE_URL")
	values["SQS_PROCESSING_TIMEOUT"] = "40s"
	_, err := load(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "SQS_PROCESSING_TIMEOUT") {
		t.Fatalf("expected clear aggregate validation error, got %v", err)
	}
}

func requiredValues() map[string]string {
	return map[string]string{
		"HTTP_ADDR": ":0", "DATABASE_URL": "postgres://example", "OIDC_ISSUER_URL": "https://issuer.example/realms/test",
		"OIDC_AUDIENCE": "wager-api", "AWS_REGION": "us-east-1", "SQS_INPUT_QUEUE_NAME": "input.fifo",
		"SQS_DLQ_QUEUE_NAME": "dlq.fifo", "SQS_EVENTS_QUEUE_NAME": "events.fifo", "SQS_CONSUMER_NAME": "consumer",
	}
}
