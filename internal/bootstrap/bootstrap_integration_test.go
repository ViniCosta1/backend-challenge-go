package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	"github.com/vinicosta1/backend-challenge-go/internal/config"
	infraSQS "github.com/vinicosta1/backend-challenge-go/internal/infrastructure/sqs"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/worker"
	"github.com/vinicosta1/backend-challenge-go/internal/transport/httpserver"
)

type composedFixture struct {
	ctx      context.Context
	admin    *pgxpool.Pool
	pool     *pgxpool.Pool
	settings config.Config
	client   *awssqs.Client
	queues   infraSQS.Queues
	issuer   string
	schema   string
}

type runningApp struct {
	app       *fx.App
	server    *httpserver.Server
	consumer  *infraSQS.Consumer
	publisher *infraSQS.Publisher
	pending   *worker.PendingReference
	pool      *pgxpool.Pool
}

func TestComposedApplicationLifecycleAndEndToEnd(t *testing.T) {
	f := newComposedFixture(t)
	instance := f.startApp(t)
	internal := f.token(t, "internal-service", envOr("INTERNAL_SERVICE_CLIENT_SECRET", "internal-service-local-secret"))
	provider := f.token(t, "provider-a", envOr("PROVIDER_A_CLIENT_SECRET", "provider-a-local-secret"))

	for _, endpoint := range []string{"/health/live", "/health/ready", "/metrics"} {
		status, body := f.request(t, instance, http.MethodGet, endpoint, "", nil, "")
		if status != http.StatusOK {
			t.Fatalf("%s returned %d: %s", endpoint, status, body)
		}
	}

	playerID := uuid.NewString()
	status, body := f.request(t, instance, http.MethodPost, "/wallets", internal, map[string]any{
		"playerId": playerID, "initialBalance": map[string]string{"amount": "100.00", "currency": "BRL"},
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("create wallet: %d %s", status, body)
	}
	var wallet struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &wallet); err != nil || wallet.ID == "" {
		t.Fatalf("decode wallet: %v %s", err, body)
	}

	status, body = f.request(t, instance, http.MethodPost, "/wagering/transactions", provider, wagerBody(playerID, wallet.ID, "BET", "25.00", uuid.NewString()), uuid.NewString())
	if status != http.StatusOK {
		t.Fatalf("HTTP BET: %d %s", status, body)
	}

	messageID := uuid.NewString()
	envelope := map[string]any{
		"messageId": messageID, "type": "WagerTransactionRequested", "occurredAt": time.Now().UTC(),
		"data": wagerData(playerID, wallet.ID, "WIN", "10.00", uuid.NewString(), uuid.NewString()),
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.SendMessage(f.ctx, &awssqs.SendMessageInput{QueueUrl: aws.String(f.queues.Input),
		MessageBody: aws.String(string(payload)), MessageGroupId: aws.String(wallet.ID), MessageDeduplicationId: aws.String(messageID)}); err != nil {
		t.Fatal(err)
	}
	f.eventually(t, 10*time.Second, func() bool {
		var balance, inbox int64
		if err := f.pool.QueryRow(f.ctx, "SELECT balance_amount FROM wallets WHERE id=$1", wallet.ID).Scan(&balance); err != nil {
			return false
		}
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM inbox_messages WHERE completed_at IS NOT NULL").Scan(&inbox); err != nil {
			return false
		}
		return balance == 8500 && inbox == 1
	})
	f.eventually(t, 10*time.Second, func() bool {
		var pending int
		return f.pool.QueryRow(f.ctx, "SELECT count(*) FROM outbox_events WHERE published_at IS NULL").Scan(&pending) == nil && pending == 0
	})
	output, err := f.client.ReceiveMessage(f.ctx, &awssqs.ReceiveMessageInput{QueueUrl: aws.String(f.queues.Events), MaxNumberOfMessages: 10})
	if err != nil || len(output.Messages) == 0 {
		t.Fatalf("outbox publisher did not deliver events: %v", err)
	}
	if _, err := f.client.DeleteQueue(f.ctx, &awssqs.DeleteQueueInput{QueueUrl: aws.String(f.queues.DLQ)}); err != nil {
		t.Fatal(err)
	}
	status, body = f.request(t, instance, http.MethodGet, "/health/ready", "", nil, "")
	if status != http.StatusServiceUnavailable || !bytes.Contains(body, []byte(`"sqs":"down"`)) {
		t.Fatalf("readiness did not reflect lost SQS dependency: %d %s", status, body)
	}

	f.stopApp(t, instance)
	if instance.server.Running() || instance.consumer.Running() || instance.publisher.Running() || instance.pending.Running() {
		t.Fatal("lifecycle left a component running")
	}
	if err := instance.pool.Ping(context.Background()); err == nil {
		t.Fatal("PostgreSQL pool was not closed after runtime components")
	}
}

func TestThreeComposedInstancesCoordinateWalletLocking(t *testing.T) {
	f := newComposedFixture(t)
	instances := []*runningApp{f.startApp(t), f.startApp(t), f.startApp(t)}
	defer func() {
		for index := len(instances) - 1; index >= 0; index-- {
			if instances[index].app != nil {
				f.stopApp(t, instances[index])
			}
		}
	}()
	internal := f.token(t, "internal-service", envOr("INTERNAL_SERVICE_CLIENT_SECRET", "internal-service-local-secret"))
	provider := f.token(t, "provider-a", envOr("PROVIDER_A_CLIENT_SECRET", "provider-a-local-secret"))
	playerID := uuid.NewString()
	status, body := f.request(t, instances[0], http.MethodPost, "/wallets", internal, map[string]any{
		"playerId": playerID, "initialBalance": map[string]string{"amount": "100.00", "currency": "BRL"},
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("create wallet: %d %s", status, body)
	}
	var wallet struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &wallet); err != nil {
		t.Fatal(err)
	}

	statuses := make(chan int, 2)
	var group sync.WaitGroup
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func(instance *runningApp) {
			defer group.Done()
			status, _ := f.request(t, instance, http.MethodPost, "/wagering/transactions", provider,
				wagerBody(playerID, wallet.ID, "BET", "80.00", uuid.NewString()), uuid.NewString())
			statuses <- status
		}(instances[index])
	}
	group.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusUnprocessableEntity] != 1 {
		t.Fatalf("expected one processed and one rejected BET, got %v", counts)
	}
	status, body = f.request(t, instances[2], http.MethodGet, "/wallets/"+wallet.ID, internal, nil, "")
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"amount":"20.00"`)) {
		t.Fatalf("third instance observed wrong wallet result: %d %s", status, body)
	}
	var debits int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id=$1 AND direction='DEBIT'", wallet.ID).Scan(&debits); err != nil || debits != 1 {
		t.Fatalf("expected one debit ledger, got %d: %v", debits, err)
	}
}

// TestComposedApplicationChildProcess is the entry point used by the
// independent-process integration test below. It starts the complete Fx graph
// and performs the same SIGTERM shutdown path as cmd/api.
func TestComposedApplicationChildProcess(t *testing.T) {
	if os.Getenv("FX_APP_CHILD") == "" {
		t.Skip("helper for independent Fx application process test")
	}
	app := New()
	start, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.Start(start); err != nil {
		t.Fatal(err)
	}
	signals, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	<-signals.Done()
	stopSignals()
	stop, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopCancel()
	if err := app.Stop(stop); err != nil {
		t.Fatal(err)
	}
}

func TestThreeIndependentApplicationProcessesCoordinateWalletLocking(t *testing.T) {
	f := newComposedFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type childProcess struct {
		command *exec.Cmd
		output  bytes.Buffer
		done    chan error
		address string
	}
	children := make([]*childProcess, 3)
	for index := range children {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		child := &childProcess{address: address, done: make(chan error, 1)}
		child.command = exec.Command(binary, "-test.run=^TestComposedApplicationChildProcess$", "-test.v")
		child.command.Env = append(os.Environ(), f.childEnvironment(address)...)
		child.command.Stdout, child.command.Stderr = &child.output, &child.output
		if err := child.command.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { child.done <- child.command.Wait() }()
		children[index] = child
	}
	stopChildren := func() {
		for _, child := range children {
			if child == nil || child.command.Process == nil {
				continue
			}
			_ = child.command.Process.Signal(syscall.SIGTERM)
		}
		for _, child := range children {
			if child == nil || child.command.Process == nil {
				continue
			}
			select {
			case err := <-child.done:
				if err != nil {
					t.Errorf("child application stopped with error: %v\n%s", err, child.output.String())
				}
			case <-time.After(35 * time.Second):
				_ = child.command.Process.Kill()
				err := <-child.done
				t.Errorf("child application did not stop after SIGTERM: %v\n%s", err, child.output.String())
			}
		}
	}
	t.Cleanup(stopChildren)
	for _, child := range children {
		deadline := time.Now().Add(30 * time.Second)
		for {
			select {
			case err := <-child.done:
				t.Fatalf("child application exited before readiness: %v\n%s", err, child.output.String())
			default:
			}
			request, _ := http.NewRequestWithContext(f.ctx, http.MethodGet, "http://"+child.address+"/health/live", nil)
			response, err := http.DefaultClient.Do(request)
			if err == nil {
				_ = response.Body.Close()
				if response.StatusCode == http.StatusOK {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("child application did not become live\n%s", child.output.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	internal := f.token(t, "internal-service", envOr("INTERNAL_SERVICE_CLIENT_SECRET", "internal-service-local-secret"))
	provider := f.token(t, "provider-a", envOr("PROVIDER_A_CLIENT_SECRET", "provider-a-local-secret"))
	playerID := uuid.NewString()
	status, body := f.requestAddress(t, children[0].address, http.MethodPost, "/wallets", internal, map[string]any{
		"playerId": playerID, "initialBalance": map[string]string{"amount": "100.00", "currency": "BRL"},
	}, "")
	if status != http.StatusCreated {
		t.Fatalf("create wallet through child: %d %s", status, body)
	}
	var wallet struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &wallet); err != nil || wallet.ID == "" {
		t.Fatalf("decode wallet: %v %s", err, body)
	}

	statuses := make(chan int, 2)
	var group sync.WaitGroup
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func(child *childProcess) {
			defer group.Done()
			status, _ := f.requestAddress(t, child.address, http.MethodPost, "/wagering/transactions", provider,
				wagerBody(playerID, wallet.ID, "BET", "80.00", uuid.NewString()), uuid.NewString())
			statuses <- status
		}(children[index])
	}
	group.Wait()
	close(statuses)
	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusUnprocessableEntity] != 1 {
		t.Fatalf("three OS processes produced wrong outcomes: %v", counts)
	}
	status, body = f.requestAddress(t, children[2].address, http.MethodGet, "/wallets/"+wallet.ID, internal, nil, "")
	if status != http.StatusOK || !bytes.Contains(body, []byte(`"amount":"20.00"`)) {
		t.Fatalf("third process observed wrong result: %d %s", status, body)
	}
	var debits int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id=$1 AND direction='DEBIT'", wallet.ID).Scan(&debits); err != nil || debits != 1 {
		t.Fatalf("expected one debit ledger, got %d: %v", debits, err)
	}
	stopChildren()
	children = nil
}

func newComposedFixture(t *testing.T) *composedFixture {
	t.Helper()
	databaseURL := os.Getenv("POSTGRES_TEST_DATABASE_URL")
	issuer := os.Getenv("KEYCLOAK_TEST_ISSUER_URL")
	endpoint := os.Getenv("SQS_TEST_ENDPOINT_URL")
	if databaseURL == "" || issuer == "" || endpoint == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL, KEYCLOAK_TEST_ISSUER_URL, and SQS_TEST_ENDPOINT_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	base, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, base.Copy())
	if err != nil {
		t.Fatal(err)
	}
	schema := "fx_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	database := base.Copy()
	database.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, database.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"000001_init.up.sql", "000002_wager_result_balance.up.sql", "000003_pending_reference_state.up.sql"} {
		migration, err := os.ReadFile("../database/migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	sqsSettings := infraSQS.Config{Endpoint: endpoint, Region: "us-east-1", AccessKey: "test", SecretKey: "test"}
	client, err := infraSQS.NewClient(ctx, sqsSettings)
	if err != nil {
		t.Fatal(err)
	}
	createQueue := func(suffix string) (string, string) {
		name := "fx-" + uuid.NewString() + "-" + suffix + ".fifo"
		response, err := client.CreateQueue(ctx, &awssqs.CreateQueueInput{QueueName: aws.String(name), Attributes: map[string]string{
			"FifoQueue": "true", "ContentBasedDeduplication": "false", "VisibilityTimeout": "30"}})
		if err != nil {
			t.Fatal(err)
		}
		queueURL := aws.ToString(response.QueueUrl)
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = client.DeleteQueue(cleanup, &awssqs.DeleteQueueInput{QueueUrl: aws.String(queueURL)})
		})
		return name, queueURL
	}
	sqsSettings.DLQ.Name, sqsSettings.DLQ.URL = createQueue("dlq")
	sqsSettings.Input.Name, sqsSettings.Input.URL = createQueue("input")
	sqsSettings.Events.Name, sqsSettings.Events.URL = createQueue("events")
	dlq, err := client.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{QueueUrl: aws.String(sqsSettings.DLQ.URL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn}})
	if err != nil {
		t.Fatal(err)
	}
	redrive, _ := json.Marshal(map[string]string{"deadLetterTargetArn": dlq.Attributes["QueueArn"], "maxReceiveCount": "5"})
	if _, err := client.SetQueueAttributes(ctx, &awssqs.SetQueueAttributesInput{QueueUrl: aws.String(sqsSettings.Input.URL), Attributes: map[string]string{"RedrivePolicy": string(redrive)}}); err != nil {
		t.Fatal(err)
	}
	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}
	applicationDatabaseURL := databaseURL + separator + "search_path=" + url.QueryEscape(schema)
	settings := config.Config{
		HTTP:     config.HTTP{Address: "127.0.0.1:0", ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second},
		Database: config.Database{URL: applicationDatabaseURL, MaxConns: 16}, OIDC: config.OIDC{IssuerURL: issuer, Audience: "wager-api"},
		SQS: config.SQS{Endpoint: endpoint, Region: "us-east-1", AccessKey: "test", SecretKey: "test",
			Input: config.Queue{Name: sqsSettings.Input.Name, URL: sqsSettings.Input.URL}, DLQ: config.Queue{Name: sqsSettings.DLQ.Name, URL: sqsSettings.DLQ.URL}, Events: config.Queue{Name: sqsSettings.Events.Name, URL: sqsSettings.Events.URL},
			ConsumerName: "fx-consumer", ReceiveWait: time.Second, VisibilityTimeout: 30 * time.Second, ProcessingTimeout: 20 * time.Second, OperationTimeout: 5 * time.Second, IdleDelay: 20 * time.Millisecond, MaxReceiveCount: 5},
		Workers: config.Workers{OutboxPollInterval: 20 * time.Millisecond, OutboxOperationTimeout: 5 * time.Second, PendingReferencePollInterval: 20 * time.Millisecond, PendingReferenceOperationTimeout: 5 * time.Second, PendingReferenceBatchSize: 10},
		Runtime: config.Runtime{StartupTimeout: 10 * time.Second, ShutdownTimeout: 10 * time.Second, LogLevel: slog.LevelError},
	}
	return &composedFixture{ctx: ctx, admin: admin, pool: pool, settings: settings, client: client,
		queues: infraSQS.Queues{Input: sqsSettings.Input.URL, DLQ: sqsSettings.DLQ.URL, Events: sqsSettings.Events.URL}, issuer: issuer, schema: schema}
}

func (f *composedFixture) startApp(t *testing.T) *runningApp {
	t.Helper()
	instance := &runningApp{}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	instance.app = New(fx.Replace(f.settings), fx.Replace(logger), fx.Populate(&instance.server, &instance.consumer, &instance.publisher, &instance.pending, &instance.pool))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := instance.app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !instance.server.Running() || !instance.consumer.Running() || !instance.publisher.Running() || !instance.pending.Running() {
		t.Fatal("not all Fx lifecycle components started")
	}
	t.Cleanup(func() {
		if instance.app != nil {
			f.stopApp(t, instance)
		}
	})
	return instance
}

func (f *composedFixture) stopApp(t *testing.T, instance *runningApp) {
	t.Helper()
	if instance.app == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := instance.app.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	instance.app = nil
}

func (f *composedFixture) token(t *testing.T, clientID, secret string) string {
	t.Helper()
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {clientID}, "client_secret": {secret}}
	request, _ := http.NewRequestWithContext(f.ctx, http.MethodPost, f.issuer+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&token) != nil || token.AccessToken == "" {
		t.Fatalf("token for %s failed with %d", clientID, response.StatusCode)
	}
	return token.AccessToken
}

func (f *composedFixture) request(t *testing.T, instance *runningApp, method, path, token string, body any, key string) (int, []byte) {
	return f.requestAddress(t, instance.server.Address(), method, path, token, body, key)
}

func (f *composedFixture) requestAddress(t *testing.T, address, method, path, token string, body any, key string) (int, []byte) {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	request, err := http.NewRequestWithContext(f.ctx, method, "http://"+address+path, bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	return response.StatusCode, data
}

func (f *composedFixture) childEnvironment(address string) []string {
	s := f.settings
	return []string{
		"FX_APP_CHILD=1", "HTTP_ADDR=" + address,
		"HTTP_READ_HEADER_TIMEOUT=" + s.HTTP.ReadHeaderTimeout.String(),
		"HTTP_READ_TIMEOUT=" + s.HTTP.ReadTimeout.String(), "HTTP_WRITE_TIMEOUT=" + s.HTTP.WriteTimeout.String(),
		"HTTP_IDLE_TIMEOUT=" + s.HTTP.IdleTimeout.String(), "DATABASE_URL=" + s.Database.URL,
		"DATABASE_MAX_CONNS=" + strconv.Itoa(int(s.Database.MaxConns)), "OIDC_ISSUER_URL=" + s.OIDC.IssuerURL,
		"OIDC_AUDIENCE=" + s.OIDC.Audience, "SQS_ENDPOINT_URL=" + s.SQS.Endpoint,
		"AWS_REGION=" + s.SQS.Region, "AWS_ACCESS_KEY_ID=" + s.SQS.AccessKey,
		"AWS_SECRET_ACCESS_KEY=" + s.SQS.SecretKey, "SQS_INPUT_QUEUE_NAME=" + s.SQS.Input.Name,
		"SQS_INPUT_QUEUE_URL=" + s.SQS.Input.URL, "SQS_DLQ_QUEUE_NAME=" + s.SQS.DLQ.Name,
		"SQS_DLQ_QUEUE_URL=" + s.SQS.DLQ.URL, "SQS_EVENTS_QUEUE_NAME=" + s.SQS.Events.Name,
		"SQS_EVENTS_QUEUE_URL=" + s.SQS.Events.URL, "SQS_CONSUMER_NAME=" + s.SQS.ConsumerName,
		"SQS_RECEIVE_WAIT=" + s.SQS.ReceiveWait.String(), "SQS_VISIBILITY_TIMEOUT=" + s.SQS.VisibilityTimeout.String(),
		"SQS_PROCESSING_TIMEOUT=" + s.SQS.ProcessingTimeout.String(), "SQS_OPERATION_TIMEOUT=" + s.SQS.OperationTimeout.String(),
		"SQS_IDLE_DELAY=" + s.SQS.IdleDelay.String(), "SQS_MAX_RECEIVE_COUNT=" + strconv.Itoa(int(s.SQS.MaxReceiveCount)),
		"OUTBOX_POLL_INTERVAL=" + s.Workers.OutboxPollInterval.String(),
		"OUTBOX_OPERATION_TIMEOUT=" + s.Workers.OutboxOperationTimeout.String(),
		"PENDING_REFERENCE_POLL_INTERVAL=" + s.Workers.PendingReferencePollInterval.String(),
		"PENDING_REFERENCE_OPERATION_TIMEOUT=" + s.Workers.PendingReferenceOperationTimeout.String(),
		"PENDING_REFERENCE_BATCH_SIZE=" + strconv.Itoa(s.Workers.PendingReferenceBatchSize),
		"STARTUP_TIMEOUT=" + s.Runtime.StartupTimeout.String(), "SHUTDOWN_TIMEOUT=" + s.Runtime.ShutdownTimeout.String(),
		"LOG_LEVEL=error",
	}
}

func (f *composedFixture) eventually(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func wagerBody(playerID, walletID, kind, amount, externalID string) map[string]any {
	return wagerData(playerID, walletID, kind, amount, externalID, "")
}

func wagerData(playerID, walletID, kind, amount, externalID, idempotencyKey string) map[string]any {
	data := map[string]any{"providerId": "provider-a", "externalTransactionId": externalID, "playerId": playerID,
		"walletId": walletID, "roundId": "round-1", "gameId": "game-1", "kind": kind,
		"money": map[string]string{"amount": amount, "currency": "BRL"}}
	if idempotencyKey != "" {
		data["idempotencyKey"] = idempotencyKey
	}
	return data
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
