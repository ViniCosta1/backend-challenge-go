package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/oidc"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/postgres"
	"github.com/vinicosta1/backend-challenge-go/internal/infrastructure/sqs"
	"github.com/vinicosta1/backend-challenge-go/internal/observability"
	"github.com/vinicosta1/backend-challenge-go/internal/transport/httpapi"
)

type fixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	server    *httptest.Server
	issuer    string
	providerA string
	providerB string
	internal  string
	logs      *bytes.Buffer
	sqsClient *awssqs.Client
	sqsConfig sqs.Config
}

func realAPI(t *testing.T) *fixture {
	t.Helper()
	databaseURL, issuer := os.Getenv("POSTGRES_TEST_DATABASE_URL"), os.Getenv("KEYCLOAK_TEST_ISSUER_URL")
	if databaseURL == "" || issuer == "" {
		t.Skip("POSTGRES_TEST_DATABASE_URL and KEYCLOAK_TEST_ISSUER_URL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	schema := "http_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean test schema: %v", err)
		}
		admin.Close()
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"000001_init.up.sql", "000002_wager_result_balance.up.sql", "000003_pending_reference_state.up.sql"} {
		data, err := os.ReadFile("../../database/migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(data)); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	verifier, err := oidc.NewAuthenticator(ctx, issuer, "wager-api")
	if err != nil {
		t.Fatalf("real OIDC discovery: %v", err)
	}
	f := &fixture{ctx: ctx, pool: pool, issuer: issuer, logs: &bytes.Buffer{}}
	f.providerA = f.token(t, "provider-a", envDefault("PROVIDER_A_CLIENT_SECRET", "provider-a-local-secret"))
	f.providerB = f.token(t, "provider-b", envDefault("PROVIDER_B_CLIENT_SECRET", "provider-b-local-secret"))
	f.internal = f.token(t, "internal-service", envDefault("INTERNAL_SERVICE_CLIENT_SECRET", "internal-service-local-secret"))
	repositories := postgres.NewRepositorySet(pool)
	transactions := postgres.NewTransactionManager(pool)
	logger := slog.New(slog.NewJSONHandler(f.logs, nil))
	checks := []application.ReadinessCheck{{Name: "postgres", Check: pool.Ping}}
	if endpoint := os.Getenv("SQS_TEST_ENDPOINT_URL"); endpoint != "" {
		f.sqsClient, f.sqsConfig = realSQSQueues(t, ctx, endpoint)
		checks = append(checks, application.ReadinessCheck{Name: "sqs", Check: sqs.NewReadinessChecker(f.sqsClient, f.sqsConfig).Check})
	}
	readiness, err := application.NewReadiness(checks...)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewMetrics(pool)
	f.server = httptest.NewServer(httpapi.NewHandler(application.NewCreateWallet(transactions, nil), application.NewProcessWagerTransaction(transactions, nil),
		application.NewReadWallets(repositories.Wallets(), repositories.Ledger(), logger), application.NewReadWagers(repositories.WagerTransactions()), readiness, verifier, logger,
		httpapi.WithObservability(metrics, metrics.Handler())))
	t.Cleanup(f.server.Close)
	return f
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (f *fixture) token(t *testing.T, client, secret string) string {
	t.Helper()
	data := url.Values{"grant_type": {"client_credentials"}, "client_id": {client}, "client_secret": {secret}}
	request, err := http.NewRequestWithContext(f.ctx, http.MethodPost, f.issuer+"/protocol/openid-connect/token", strings.NewReader(data.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	clientHTTP := &http.Client{Timeout: 10 * time.Second}
	response, err := clientHTTP.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Keycloak client_credentials for %s: %d", client, response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&token); err != nil || token.AccessToken == "" {
		t.Fatal("Keycloak did not return an access token")
	}
	return token.AccessToken
}

func (f *fixture) request(t *testing.T, method, path, token string, body any, key string, status int) []byte {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, bytes.NewReader(data))
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
	response, err := f.server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s: expected %d, got %d: %s", method, path, status, response.StatusCode, payload)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatal("response is not JSON")
	}
	return payload
}

type wireMoney struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}
type wireWallet struct {
	ID       string    `json:"id"`
	PlayerID string    `json:"playerId"`
	Balance  wireMoney `json:"balance"`
	Version  int64     `json:"version"`
}
type wireWager struct {
	TransactionID    string             `json:"transactionId"`
	Status           domain.WagerStatus `json:"status"`
	Balance          *wireMoney         `json:"balance"`
	IdempotentReplay bool               `json:"idempotentReplay"`
	FailureCode      domain.FailureCode `json:"failureCode"`
	ProviderID       string             `json:"providerId"`
}

func decode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

func walletBody(player, amount string) any {
	return struct {
		PlayerID       string    `json:"playerId"`
		InitialBalance wireMoney `json:"initialBalance"`
	}{player, wireMoney{amount, "BRL"}}
}

type wagerBody struct {
	ProviderID  string           `json:"providerId,omitempty"`
	ExternalID  string           `json:"externalTransactionId"`
	PlayerID    string           `json:"playerId"`
	WalletID    string           `json:"walletId"`
	RoundID     string           `json:"roundId"`
	GameID      string           `json:"gameId"`
	Kind        domain.WagerKind `json:"kind"`
	Money       wireMoney        `json:"money"`
	ReferenceID string           `json:"referenceExternalTransactionId,omitempty"`
}

func wager(wallet wireWallet, kind domain.WagerKind, amount string) wagerBody {
	return wagerBody{ExternalID: uuid.NewString(), PlayerID: wallet.PlayerID, WalletID: wallet.ID, RoundID: "round-test", GameID: "game-test", Kind: kind, Money: wireMoney{amount, "BRL"}}
}

func (f *fixture) createWallet(t *testing.T, amount string) wireWallet {
	t.Helper()
	return decode[wireWallet](t, f.request(t, "POST", "/wallets", f.internal, walletBody(uuid.NewString(), amount), "", 201))
}

func count(t *testing.T, f *fixture, query string, args ...any) int {
	t.Helper()
	var result int
	if err := f.pool.QueryRow(f.ctx, query, args...).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHTTPRealKeycloakAuthorizationAndProviderIsolation(t *testing.T) {
	f := realAPI(t)
	body := walletBody(uuid.NewString(), "100.00")
	f.request(t, "POST", "/wallets", "", body, "", 401)
	f.request(t, "POST", "/wallets", "invalid-token", body, "", 401)
	f.request(t, "POST", "/wallets", f.providerA, body, "", 403)
	if count(t, f, "SELECT count(*) FROM wallets") != 0 {
		t.Fatal("unauthorized wallet request persisted state")
	}
	wallet := f.createWallet(t, "100.00")
	for _, path := range []string{"/wallets/" + wallet.ID, "/wallets/" + wallet.ID + "/ledger"} {
		f.request(t, "GET", path, f.providerA, nil, "", 403)
	}
	f.request(t, "POST", "/wallets/"+wallet.ID+"/reconciliation", f.providerA, nil, "", 403)

	forbidden := wager(wallet, domain.WagerKindBet, "25.00")
	forbidden.ProviderID = "provider-b"
	f.request(t, "POST", "/wagering/transactions", f.providerA, forbidden, "forbidden-key", 403)
	f.request(t, "POST", "/wagering/transactions", f.internal, forbidden, "internal-key", 403)
	if count(t, f, "SELECT count(*) FROM wager_transactions WHERE kind <> 'OPENING'") != 0 {
		t.Fatal("unauthorized operation persisted financial state")
	}

	own := wager(wallet, domain.WagerKindBet, "25.00")
	a := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, own, "a-key", 200))
	if a.Balance == nil || a.Balance.Amount != "75.00" {
		t.Fatal("provider-a operation did not process")
	}
	aRead := decode[wireWager](t, f.request(t, "GET", "/wagering/transactions/"+a.TransactionID, f.providerA, nil, "", 200))
	if aRead.ProviderID != "provider-a" {
		t.Fatal("providerId was not derived from the token azp")
	}
	other := wager(wallet, domain.WagerKindWin, "10.00")
	other.ProviderID = "provider-b"
	b := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerB, other, "b-key", 200))
	f.request(t, "GET", "/wagering/transactions/"+b.TransactionID, f.providerB, nil, "", 200)
	foreign := f.request(t, "GET", "/wagering/transactions/"+b.TransactionID, f.providerA, nil, "", 404)
	missing := f.request(t, "GET", "/wagering/transactions/"+uuid.NewString(), f.providerA, nil, "", 404)
	if !bytes.Equal(foreign, missing) {
		t.Fatal("foreign and missing transactions must have identical responses")
	}
	f.request(t, "GET", "/providers/provider-b/wagering/transactions/"+other.ExternalID, f.providerA, nil, "", 404)
	f.request(t, "GET", "/providers/provider-a/wagering/transactions/"+other.ExternalID, f.providerA, nil, "", 404)
	f.request(t, "GET", "/providers/provider-a/wagering/transactions/"+own.ExternalID, f.providerA, nil, "", 200)
	f.request(t, "GET", "/providers/provider-b/wagering/transactions/"+other.ExternalID, f.providerB, nil, "", 200)

	noRole := f.token(t, "auth-no-role-test", "auth-no-role-test-local-secret")
	f.request(t, "POST", "/wagering/transactions", noRole, own, "no-role", 403)
	wrongAudience := f.token(t, "auth-wrong-audience-test", "auth-wrong-audience-test-local-secret")
	f.request(t, "POST", "/wagering/transactions", wrongAudience, own, "wrong-audience", 401)
	validButChanged := f.providerA[:len(f.providerA)-8] + "invalid!"
	f.request(t, "POST", "/wagering/transactions", validButChanged, own, "invalid-signature", 401)
	expiring := f.token(t, "auth-expiry-test", "auth-expiry-test-local-secret")
	time.Sleep(3 * time.Second)
	f.request(t, "POST", "/wagering/transactions", expiring, own, "expired", 401)
	if count(t, f, "SELECT count(*) FROM wager_transactions WHERE kind <> 'OPENING'") != 2 {
		t.Fatal("rejected authentication changed financial state")
	}
	if count(t, f, "SELECT count(*) FROM wallet_ledger_entries") != 3 {
		t.Fatal("unexpected financial ledger effects")
	}
	f.request(t, "GET", "/wallets/"+wallet.ID+"/ledger", f.internal, nil, "", 200)
	f.request(t, "POST", "/wallets/"+wallet.ID+"/reconciliation", f.internal, nil, "", 200)
}

func TestHTTPWalletAndFinancialContracts(t *testing.T) {
	f := realAPI(t)
	zero := f.createWallet(t, "0.00")
	if zero.Balance.Amount != "0.00" || zero.Version != 1 {
		t.Fatal("invalid zero wallet response")
	}
	if count(t, f, "SELECT count(*) FROM wager_transactions WHERE wallet_id=$1", zero.ID) != 0 {
		t.Fatal("zero wallet created OPENING")
	}
	wallet := f.createWallet(t, "100.00")
	if wallet.Balance.Amount != "100.00" || wallet.Version != 1 {
		t.Fatal("invalid positive wallet response")
	}
	f.request(t, "POST", "/wallets", f.internal, walletBody(wallet.PlayerID, "100.00"), "", 409)
	f.request(t, "GET", "/wallets/"+wallet.ID, f.internal, nil, "", 200)
	bet := wager(wallet, domain.WagerKindBet, "25.00")
	first := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, bet, "bet-key", 200))
	replay := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, bet, "bet-key", 200))
	if !replay.IdempotentReplay || replay.TransactionID != first.TransactionID || replay.Balance.Amount != "75.00" {
		t.Fatal("invalid successful replay")
	}
	changed := bet
	changed.Money.Amount = "26.00"
	f.request(t, "POST", "/wagering/transactions", f.providerA, changed, "bet-key", 409)
	f.request(t, "POST", "/wagering/transactions", f.providerA, bet, "another-key", 409)
	win := wager(wallet, domain.WagerKindWin, "10.00")
	f.request(t, "POST", "/wagering/transactions", f.providerA, win, "win-key", 200)
	replay = decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, bet, "bet-key", 200))
	if replay.Balance.Amount != "75.00" {
		t.Fatal("replay recomputed the current wallet balance")
	}
	insufficient := wager(wallet, domain.WagerKindBet, "100.00")
	rejected := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, insufficient, "rejected-key", 422))
	if rejected.FailureCode != domain.FailureCodeInsufficientBalance || rejected.Balance.Amount != "85.00" {
		t.Fatal("rejection snapshot incorrect")
	}
	f.request(t, "POST", "/wagering/transactions", f.providerA, wager(wallet, domain.WagerKindWin, "5.00"), "another-win", 200)
	rejectedReplay := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, insufficient, "rejected-key", 422))
	if !rejectedReplay.IdempotentReplay || rejectedReplay.Balance.Amount != "85.00" {
		t.Fatal("rejected replay recomputed wallet balance")
	}
	pending := wager(wallet, domain.WagerKindRefund, "25.00")
	pending.ReferenceID = "not-yet-received"
	p := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, pending, "pending-key", 202))
	pr := decode[wireWager](t, f.request(t, "POST", "/wagering/transactions", f.providerA, pending, "pending-key", 202))
	if p.Balance != nil || !pr.IdempotentReplay || pr.TransactionID != p.TransactionID {
		t.Fatal("pending replay must preserve state without a balance")
	}
	f.request(t, "POST", "/wagering/transactions", f.providerA, wager(wallet, domain.WagerKindLoss, "0.00"), "loss-key", 200)
	state := decode[wireWallet](t, f.request(t, "GET", "/wallets/"+wallet.ID, f.internal, nil, "", 200))
	if state.Balance.Amount != "90.00" || state.Version != 4 {
		t.Fatal("unexpected wallet balance/version")
	}
	if count(t, f, "SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id=$1", wallet.ID) != 4 {
		t.Fatal("replay or rejected/pending/LOSS created a ledger entry")
	}

	for _, amount := range []string{"-1.00", "1e2", "NaN", "Infinity", "1.001", "1.-1", "1.+1", "+1.00"} {
		bad := bet
		bad.Money.Amount = amount
		f.request(t, "POST", "/wagering/transactions", f.providerA, bad, "invalid-money", 400)
	}
	f.request(t, "POST", "/wallets", f.internal, json.RawMessage(`{"playerId":"`+uuid.NewString()+`","initialBalance":{"amount":25,"currency":"BRL"}}`), "", 400)
	f.request(t, "POST", "/wagering/transactions", f.providerA, bet, "", 400)
	f.request(t, "POST", "/wagering/transactions", f.providerA, wager(wallet, domain.WagerKindOpening, "1.00"), "opening-key", 400)
	f.request(t, "POST", "/wallets", f.internal, walletBody("bad-uuid", "1.00"), "", 400)
	if count(t, f, "SELECT count(*) FROM wallet_ledger_entries WHERE wallet_id=$1", wallet.ID) != 4 {
		t.Fatal("invalid input changed financial state")
	}
}

type wireLedgerPage struct {
	Entries []struct {
		ID        string    `json:"id"`
		CreatedAt time.Time `json:"createdAt"`
	} `json:"entries"`
	NextCursor string `json:"nextCursor"`
}

func TestHTTPLedgerPaginationAndReconciliation(t *testing.T) {
	f := realAPI(t)
	wallet := f.createWallet(t, "100.00")
	for index := 0; index < 3; index++ {
		f.request(t, "POST", "/wagering/transactions", f.providerA, wager(wallet, domain.WagerKindWin, "1.00"), fmt.Sprintf("win-%d", index), 200)
	}
	seen := make(map[string]bool)
	cursor := ""
	for index := 0; index < 4; index++ {
		path := "/wallets/" + wallet.ID + "/ledger?limit=1"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		page := decode[wireLedgerPage](t, f.request(t, "GET", path, f.internal, nil, "", 200))
		if len(page.Entries) != 1 || seen[page.Entries[0].ID] {
			t.Fatal("cursor pagination skipped or duplicated a ledger entry")
		}
		seen[page.Entries[0].ID] = true
		cursor = page.NextCursor
		if index < 3 && cursor == "" || index == 3 && cursor != "" {
			t.Fatal("invalid nextCursor boundary")
		}
	}
	f.request(t, "GET", "/wallets/"+wallet.ID+"/ledger?cursor=garbage", f.internal, nil, "", 400)
	f.request(t, "GET", "/wallets/"+wallet.ID+"/ledger?limit=101", f.internal, nil, "", 400)
	f.request(t, "GET", "/wallets/"+wallet.ID+"/ledger?limit=0", f.internal, nil, "", 400)
	result := decode[struct {
		Consistent bool      `json:"consistent"`
		Stored     wireMoney `json:"storedBalance"`
		Calculated wireMoney `json:"calculatedBalance"`
		Difference wireMoney `json:"difference"`
		Checked    int64     `json:"checkedEntries"`
	}](t,
		f.request(t, "POST", "/wallets/"+wallet.ID+"/reconciliation", f.internal, nil, "", 200))
	if !result.Consistent || result.Stored.Amount != "103.00" || result.Calculated.Amount != "103.00" || result.Difference.Amount != "0.00" || result.Checked != 4 {
		t.Fatalf("invalid reconciliation: %+v", result)
	}
	// Explicit corruption fixture checks diagnosis/logging without automatic repair.
	if _, err := f.pool.Exec(f.ctx, "UPDATE wallets SET balance_amount = balance_amount + 1 WHERE id=$1", wallet.ID); err != nil {
		t.Fatal(err)
	}
	mismatch := decode[struct {
		Consistent bool      `json:"consistent"`
		Difference wireMoney `json:"difference"`
	}](t,
		f.request(t, "POST", "/wallets/"+wallet.ID+"/reconciliation", f.internal, nil, "", 200))
	if mismatch.Consistent || mismatch.Difference.Amount != "0.01" {
		t.Fatal("divergence not reported")
	}
	if !strings.Contains(f.logs.String(), "wallet reconciliation mismatch") || !strings.Contains(f.logs.String(), wallet.ID) {
		t.Fatal("missing structured reconciliation log")
	}
	metricsResponse, err := f.server.Client().Get(f.server.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	metricsBody, readErr := io.ReadAll(metricsResponse.Body)
	metricsResponse.Body.Close()
	if readErr != nil || metricsResponse.StatusCode != http.StatusOK ||
		!bytes.Contains(metricsBody, []byte("wager_reconciliation_divergences_total 1")) {
		t.Fatalf("reconciliation divergence metric was not incremented: status=%d body=%s err=%v",
			metricsResponse.StatusCode, metricsBody, readErr)
	}
	state := decode[wireWallet](t, f.request(t, "GET", "/wallets/"+wallet.ID, f.internal, nil, "", 200))
	if state.Balance.Amount != "103.01" || state.Version != 4 {
		t.Fatal("reconciliation modified financial state")
	}
}

func TestHTTPHealthAndUnavailablePostgres(t *testing.T) {
	f := realAPI(t)
	wallet := f.createWallet(t, "0.00")
	f.request(t, "GET", "/health/live", "", nil, "", 200)
	ready := decode[struct {
		Scope  string            `json:"scope"`
		Checks map[string]string `json:"checks"`
	}](t, f.request(t, "GET", "/health/ready", "", nil, "", 200))
	expectedChecks := 1
	if f.sqsClient != nil {
		expectedChecks = 2
		if ready.Checks["sqs"] != "up" {
			t.Fatal("SQS readiness must check real queues")
		}
	}
	if ready.Scope != "configured_dependencies" || ready.Checks["postgres"] != "up" || len(ready.Checks) != expectedChecks {
		t.Fatal("readiness must report only real configured checks")
	}
	f.pool.Close()
	f.request(t, "GET", "/health/live", "", nil, "", 200)
	f.request(t, "GET", "/health/ready", "", nil, "", 503)
	f.request(t, "GET", "/wallets/"+wallet.ID, f.internal, nil, "", 503)
}

func TestHTTPLedgerCursorBreaksTimestampTies(t *testing.T) {
	f := realAPI(t)
	wallet := f.createWallet(t, "0.00")
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	ids := []string{"00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000001"}
	// Insert in the reverse UUID order with identical timestamps. Rehydration
	// supplies a deterministic, valid fixture without changing append-only rows.
	err := postgres.NewTransactionManager(f.pool).WithinTransaction(f.ctx, func(repositories application.TransactionRepositories) error {
		state, err := repositories.Wallets().FindByIDForUpdate(f.ctx, wallet.ID)
		if err != nil {
			return err
		}
		money, err := domain.NewMoney(100, "BRL")
		if err != nil {
			return err
		}
		for _, id := range ids {
			before := state.Balance()
			if err := state.Credit(money); err != nil {
				return err
			}
			transaction, err := domain.NewWagerTransaction(domain.NewWagerTransactionParams{
				ID: uuid.NewString(), ProviderID: "provider-a", ExternalTransactionID: id,
				IdempotencyKey: id, PayloadHash: strings.Repeat("a", 64), WalletID: wallet.ID,
				PlayerID: wallet.PlayerID, RoundID: "round-test", GameID: "game-test", Kind: domain.WagerKindWin, Money: money,
			})
			if err != nil {
				return err
			}
			if err := transaction.MarkProcessed(state.Balance()); err != nil {
				return err
			}
			if err := repositories.WagerTransactions().Create(f.ctx, &transaction); err != nil {
				return err
			}
			entry, err := domain.RehydrateWalletLedgerEntry(domain.RehydrateWalletLedgerEntryParams{
				ID: id, WalletID: wallet.ID, TransactionID: transaction.ID(), Direction: domain.LedgerDirectionCredit,
				Money: money, BalanceBefore: before, BalanceAfter: state.Balance(), CreatedAt: createdAt,
			})
			if err != nil {
				return err
			}
			if err := repositories.Ledger().Create(f.ctx, &entry); err != nil {
				return err
			}
		}
		return repositories.Wallets().Update(f.ctx, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/wallets/" + wallet.ID + "/ledger?limit=1"
	first := decode[wireLedgerPage](t, f.request(t, "GET", path, f.internal, nil, "", 200))
	if len(first.Entries) != 1 || first.Entries[0].ID != ids[1] || first.NextCursor == "" {
		t.Fatal("first page did not order tied timestamps by UUID")
	}
	second := decode[wireLedgerPage](t, f.request(t, "GET", path+"&cursor="+url.QueryEscape(first.NextCursor), f.internal, nil, "", 200))
	if len(second.Entries) != 1 || second.Entries[0].ID != ids[0] || second.NextCursor != "" || !first.Entries[0].CreatedAt.Equal(second.Entries[0].CreatedAt) {
		t.Fatal("cursor skipped or duplicated an entry with the same timestamp")
	}
	other := f.createWallet(t, "0.00")
	f.request(t, "GET", "/wallets/"+other.ID+"/ledger?cursor="+url.QueryEscape(first.NextCursor), f.internal, nil, "", 400)
}
