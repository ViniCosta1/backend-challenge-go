package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/vinicosta1/backend-challenge-go/internal/application"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type walletResponse struct {
	ID       string       `json:"id"`
	PlayerID string       `json:"playerId"`
	Balance  domain.Money `json:"balance"`
	Version  int64        `json:"version"`
}

func (a *API) createWalletHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerID       string   `json:"playerId"`
		InitialBalance moneyDTO `json:"initialBalance"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	money, err := body.InitialBalance.money()
	if err != nil || !validUUID(body.PlayerID) {
		writeError(w, http.StatusBadRequest, "invalid_input", "playerId and initialBalance must be valid")
		return
	}
	output, err := a.createWallet.Execute(r.Context(), application.CreateWalletInput{PlayerID: body.PlayerID, InitialBalance: money})
	if err != nil {
		a.respondError(w, r, err)
		return
	}
	setRequestFields(r.Context(), requestFields{WalletID: output.WalletID})
	w.Header().Set("Location", "/wallets/"+output.WalletID)
	writeJSON(w, http.StatusCreated, walletResponse{output.WalletID, output.PlayerID, output.Balance, output.Version})
}

func walletPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("walletId")
	if !validUUID(id) {
		writeError(w, http.StatusBadRequest, "invalid_input", "walletId must be a canonical UUID")
		return "", false
	}
	return id, true
}

func (a *API) getWallet(w http.ResponseWriter, r *http.Request) {
	id, ok := walletPath(w, r)
	if !ok {
		return
	}
	setRequestFields(r.Context(), requestFields{WalletID: id})
	wallet, err := a.wallets.FindByID(r.Context(), id)
	if err != nil {
		a.respondError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, walletResponse{wallet.ID(), wallet.PlayerID(), wallet.Balance(), wallet.Version()})
}

type ledgerEntryResponse struct {
	ID            string                 `json:"id"`
	WalletID      string                 `json:"walletId"`
	TransactionID string                 `json:"transactionId"`
	Direction     domain.LedgerDirection `json:"direction"`
	Money         domain.Money           `json:"money"`
	BalanceBefore domain.Money           `json:"balanceBefore"`
	BalanceAfter  domain.Money           `json:"balanceAfter"`
	CreatedAt     time.Time              `json:"createdAt"`
}

func (a *API) listLedger(w http.ResponseWriter, r *http.Request) {
	id, ok := walletPath(w, r)
	if !ok {
		return
	}
	setRequestFields(r.Context(), requestFields{WalletID: id})
	limit := 50
	if values, exists := r.URL.Query()["limit"]; exists {
		var err error
		if len(values) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "provide one limit")
			return
		}
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 100")
			return
		}
	}
	page, err := a.wallets.ListLedger(r.Context(), id, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		a.respondError(w, r, err)
		return
	}
	entries := make([]ledgerEntryResponse, 0, len(page.Entries))
	for index := range page.Entries {
		entry := &page.Entries[index]
		entries = append(entries, ledgerEntryResponse{entry.ID(), entry.WalletID(), entry.TransactionID(), entry.Direction(), entry.Money(), entry.BalanceBefore(), entry.BalanceAfter(), entry.CreatedAt()})
	}
	writeJSON(w, http.StatusOK, struct {
		Entries    []ledgerEntryResponse `json:"entries"`
		NextCursor string                `json:"nextCursor,omitempty"`
	}{entries, page.NextCursor})
}

func (a *API) reconcile(w http.ResponseWriter, r *http.Request) {
	id, ok := walletPath(w, r)
	if !ok {
		return
	}
	setRequestFields(r.Context(), requestFields{WalletID: id})
	result, err := a.wallets.Reconcile(r.Context(), id)
	if err != nil {
		a.respondError(w, r, err)
		return
	}
	if !result.Consistent && a.metrics != nil {
		a.metrics.RecordReconciliationDivergence()
	}
	writeJSON(w, http.StatusOK, struct {
		WalletID          string       `json:"walletId"`
		StoredBalance     domain.Money `json:"storedBalance"`
		CalculatedBalance domain.Money `json:"calculatedBalance"`
		Difference        domain.Money `json:"difference"`
		Consistent        bool         `json:"consistent"`
		CheckedEntries    int64        `json:"checkedEntries"`
	}{result.WalletID, result.StoredBalance, result.CalculatedBalance, result.Difference, result.Consistent, result.CheckedEntries})
}
