package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/auth"
)

type API struct {
	createWallet   *application.CreateWallet
	processWager   *application.ProcessWagerTransaction
	wallets        *application.ReadWallets
	wagers         *application.ReadWagers
	readiness      *application.Readiness
	authenticator  auth.Authenticator
	logger         *slog.Logger
	metrics        Metrics
	metricsHandler http.Handler
}

type Metrics interface {
	ObserveHTTPRequest(method, route string, status int, duration time.Duration)
	ObserveWager(status, transport string, replay bool, duration time.Duration)
	RecordConflict(kind string)
	RecordReconciliationDivergence()
}

type Option func(*API)

func WithObservability(metrics Metrics, handler http.Handler) Option {
	return func(api *API) {
		api.metrics = metrics
		api.metricsHandler = handler
	}
}

func NewHandler(createWallet *application.CreateWallet, processWager *application.ProcessWagerTransaction,
	wallets *application.ReadWallets, wagers *application.ReadWagers,
	readiness *application.Readiness, authenticator auth.Authenticator, logger *slog.Logger, options ...Option) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	a := &API{createWallet: createWallet, processWager: processWager, wallets: wallets, wagers: wagers,
		readiness: readiness, authenticator: authenticator, logger: logger}
	for _, option := range options {
		option(a)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{"alive"})
	})
	mux.HandleFunc("GET /health/ready", a.ready)
	if a.metricsHandler != nil {
		mux.Handle("GET /metrics", a.metricsHandler)
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		})
	}
	mux.HandleFunc("POST /wallets", a.authorize(false, a.createWalletHandler))
	mux.HandleFunc("GET /wallets/{walletId}", a.authorize(false, a.getWallet))
	mux.HandleFunc("GET /wallets/{walletId}/ledger", a.authorize(false, a.listLedger))
	mux.HandleFunc("POST /wallets/{walletId}/reconciliation", a.authorize(false, a.reconcile))
	mux.HandleFunc("POST /wagering/transactions", a.authorize(true, a.processTransaction))
	mux.HandleFunc("GET /wagering/transactions/{transactionId}", a.authorize(true, a.getTransaction))
	mux.HandleFunc("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}", a.authorize(true, a.getExternalTransaction))
	// Path fallbacks give unknown methods and paths the same JSON error shape.
	for _, path := range []string{"/health/live", "/health/ready", "/wallets", "/wallets/{walletId}", "/wallets/{walletId}/ledger", "/wallets/{walletId}/reconciliation", "/wagering/transactions", "/wagering/transactions/{transactionId}", "/providers/{providerId}/wagering/transactions/{externalTransactionId}"} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
	})
	return a.observeRequests(mux)
}

type identityContextKey struct{}

func (a *API) authorize(provider bool, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 {
			unauthorized(w)
			return
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(w)
			return
		}
		identity, err := a.authenticator.Authenticate(r.Context(), parts[1])
		if err != nil {
			unauthorized(w)
			return
		}
		allowed := identity.Internal && !identity.Provider
		if provider {
			allowed = identity.Provider && !identity.Internal
		}
		if !allowed {
			writeError(w, http.StatusForbidden, "forbidden", "identity is not authorized for this endpoint")
			return
		}
		setRequestFields(r.Context(), requestFields{ProviderID: identity.ClientID})
		next(w, r.WithContext(context.WithValue(r.Context(), identityContextKey{}, identity)))
	}
}

func authenticatedIdentity(r *http.Request) auth.Identity {
	identity, _ := r.Context().Value(identityContextKey{}).(auth.Identity)
	return identity
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="wager-api"`)
	writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer credentials are required")
}

func (a *API) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	result := a.readiness.Execute(ctx)
	status, code := "ready", http.StatusOK
	if !result.Ready {
		status, code = "not_ready", http.StatusServiceUnavailable
	}
	writeJSON(w, code, struct {
		Status string            `json:"status"`
		Scope  string            `json:"scope"`
		Checks map[string]string `json:"checks"`
	}{status, "configured_dependencies", result.Checks})
}
