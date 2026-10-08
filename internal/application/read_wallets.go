package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/vinicosta1/backend-challenge-go/internal/domain"
)

type LedgerPosition struct {
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

type ledgerCursor struct {
	WalletID string `json:"walletId"`
	LedgerPosition
}

type LedgerPage struct {
	Entries    []domain.WalletLedgerEntry
	NextCursor string
}

type ReconciliationSnapshot struct {
	WalletID          string
	StoredBalance     domain.Money
	CalculatedBalance domain.Money
	CheckedEntries    int64
}

type ReconciliationResult struct {
	ReconciliationSnapshot
	Difference domain.Money
	Consistent bool
}

type ReadWallets struct {
	wallets WalletRepository
	ledger  WalletLedgerRepository
	logger  *slog.Logger
}

func NewReadWallets(wallets WalletRepository, ledger WalletLedgerRepository, logger *slog.Logger) *ReadWallets {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReadWallets{wallets: wallets, ledger: ledger, logger: logger}
}

func (u *ReadWallets) FindByID(ctx context.Context, id string) (*domain.Wallet, error) {
	return u.wallets.FindByID(ctx, id)
}

func (u *ReadWallets) ListLedger(ctx context.Context, walletID, cursor string, limit int) (LedgerPage, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return LedgerPage{}, ErrInvalidLimit
	}
	var after *LedgerPosition
	if cursor != "" {
		if len(cursor) > 1024 {
			return LedgerPage{}, ErrInvalidCursor
		}
		payload, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil {
			return LedgerPage{}, ErrInvalidCursor
		}
		var position ledgerCursor
		if err := json.Unmarshal(payload, &position); err != nil || position.WalletID != walletID || position.CreatedAt.IsZero() {
			return LedgerPage{}, ErrInvalidCursor
		}
		if _, err := uuid.Parse(position.ID); err != nil {
			return LedgerPage{}, ErrInvalidCursor
		}
		position.CreatedAt = position.CreatedAt.UTC()
		after = &position.LedgerPosition
	}
	if _, err := u.wallets.FindByID(ctx, walletID); err != nil {
		return LedgerPage{}, err
	}
	entries, err := u.ledger.ListByWallet(ctx, walletID, after, limit+1)
	if err != nil {
		return LedgerPage{}, err
	}
	page := LedgerPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		last := &page.Entries[limit-1]
		payload, err := json.Marshal(ledgerCursor{WalletID: walletID, LedgerPosition: LedgerPosition{CreatedAt: last.CreatedAt(), ID: last.ID()}})
		if err != nil {
			return LedgerPage{}, err
		}
		page.NextCursor = base64.RawURLEncoding.EncodeToString(payload)
	}
	return page, nil
}

func (u *ReadWallets) Reconcile(ctx context.Context, walletID string) (ReconciliationResult, error) {
	snapshot, err := u.wallets.ReconciliationSnapshot(ctx, walletID)
	if err != nil {
		return ReconciliationResult{}, err
	}
	difference, err := snapshot.StoredBalance.Subtract(snapshot.CalculatedBalance)
	if err != nil {
		return ReconciliationResult{}, err
	}
	result := ReconciliationResult{ReconciliationSnapshot: snapshot, Difference: difference, Consistent: difference.Amount() == 0}
	if !result.Consistent {
		u.logger.ErrorContext(ctx, "wallet reconciliation mismatch", "wallet_id", walletID,
			"stored_amount", snapshot.StoredBalance.Amount(), "calculated_amount", snapshot.CalculatedBalance.Amount(),
			"difference_amount", difference.Amount(), "checked_entries", snapshot.CheckedEntries)
	}
	return result, nil
}
