package domain

import (
	"errors"
	"testing"
	"time"
)

func validNewWagerTransactionParams() NewWagerTransactionParams {
	return NewWagerTransactionParams{
		ID:                    "transaction-1",
		ProviderID:            "provider-1",
		ExternalTransactionID: "external-1",
		IdempotencyKey:        "provider-1:external-1",
		PayloadHash:           "payload-hash",
		WalletID:              "wallet-1",
		PlayerID:              "player-1",
		RoundID:               "round-1",
		GameID:                "game-1",
		Kind:                  WagerKindBet,
		Money:                 Money{amount: 2500, currency: "BRL"},
	}
}

func mustNewWagerTransaction(
	t *testing.T,
	change func(*NewWagerTransactionParams),
) WagerTransaction {
	t.Helper()

	params := validNewWagerTransactionParams()
	if change != nil {
		change(&params)
	}

	wager, err := NewWagerTransaction(params)
	if err != nil {
		t.Fatalf("unexpected error creating wager transaction: %v", err)
	}

	return wager
}

func validRehydrateWagerTransactionParams() RehydrateWagerTransactionParams {
	createdAt := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)

	return RehydrateWagerTransactionParams{
		ID:                    "transaction-1",
		ProviderID:            "provider-1",
		ExternalTransactionID: "external-1",
		IdempotencyKey:        "provider-1:external-1",
		PayloadHash:           "payload-hash",
		WalletID:              "wallet-1",
		PlayerID:              "player-1",
		RoundID:               "round-1",
		GameID:                "game-1",
		Kind:                  WagerKindBet,
		Money:                 Money{amount: 2500, currency: "BRL"},
		Status:                WagerStatusPending,
		CreatedAt:             createdAt,
		UpdatedAt:             createdAt.Add(time.Minute),
	}
}

func TestNewWagerTransaction(t *testing.T) {
	tests := []struct {
		name      string
		kind      WagerKind
		amount    int64
		reference string
	}{
		{name: "bet", kind: WagerKindBet, amount: 2500},
		{name: "win", kind: WagerKindWin, amount: 2500},
		{name: "loss", kind: WagerKindLoss, amount: 0},
		{
			name:      "refund",
			kind:      WagerKindRefund,
			amount:    2500,
			reference: "external-bet-1",
		},
		{
			name:      "rollback",
			kind:      WagerKindRollback,
			amount:    2500,
			reference: "external-bet-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := validNewWagerTransactionParams()
			params.Kind = tt.kind
			params.Money.amount = tt.amount
			params.ReferenceExternalTransactionID = tt.reference

			wager, err := NewWagerTransaction(params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if wager.ID() != params.ID ||
				wager.ProviderID() != params.ProviderID ||
				wager.ExternalTransactionID() != params.ExternalTransactionID ||
				wager.WalletID() != params.WalletID ||
				wager.PlayerID() != params.PlayerID {
				t.Error("transaction did not preserve its identifiers")
			}
			if wager.Kind() != tt.kind || wager.Money() != params.Money {
				t.Error("transaction did not preserve kind and money")
			}
			if wager.Status() != WagerStatusPending || wager.IsTerminal() {
				t.Errorf("expected non-terminal PENDING, got %s", wager.Status())
			}
			if wager.CreatedAt().IsZero() ||
				!wager.CreatedAt().Equal(wager.UpdatedAt()) {
				t.Error("expected equal, non-zero creation timestamps")
			}
		})
	}
}

func TestNewWagerTransactionRejectsInvalidParams(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*NewWagerTransactionParams)
		wantErr error
	}{
		{
			name:    "missing transaction id",
			change:  func(p *NewWagerTransactionParams) { p.ID = "" },
			wantErr: ErrWagerTransactionIDRequired,
		},
		{
			name:    "missing provider id",
			change:  func(p *NewWagerTransactionParams) { p.ProviderID = "" },
			wantErr: ErrWagerProviderIDRequired,
		},
		{
			name: "missing external id",
			change: func(p *NewWagerTransactionParams) {
				p.ExternalTransactionID = ""
			},
			wantErr: ErrWagerExternalIDRequired,
		},
		{
			name: "missing idempotency key",
			change: func(p *NewWagerTransactionParams) {
				p.IdempotencyKey = ""
			},
			wantErr: ErrWagerIdempotencyKeyRequired,
		},
		{
			name:    "missing payload hash",
			change:  func(p *NewWagerTransactionParams) { p.PayloadHash = "" },
			wantErr: ErrWagerPayloadHashRequired,
		},
		{
			name:    "missing wallet id",
			change:  func(p *NewWagerTransactionParams) { p.WalletID = "" },
			wantErr: ErrWagerWalletIDRequired,
		},
		{
			name:    "missing player id",
			change:  func(p *NewWagerTransactionParams) { p.PlayerID = "" },
			wantErr: ErrWagerPlayerIDRequired,
		},
		{
			name:    "missing round id",
			change:  func(p *NewWagerTransactionParams) { p.RoundID = "" },
			wantErr: ErrWagerRoundIDRequired,
		},
		{
			name:    "missing game id",
			change:  func(p *NewWagerTransactionParams) { p.GameID = "" },
			wantErr: ErrWagerGameIDRequired,
		},
		{
			name:    "opening submitted externally",
			change:  func(p *NewWagerTransactionParams) { p.Kind = WagerKindOpening },
			wantErr: ErrInvalidWagerKind,
		},
		{
			name:    "unknown kind",
			change:  func(p *NewWagerTransactionParams) { p.Kind = "UNKNOWN" },
			wantErr: ErrInvalidWagerKind,
		},
		{
			name:    "zero bet",
			change:  func(p *NewWagerTransactionParams) { p.Money.amount = 0 },
			wantErr: ErrInvalidWagerAmount,
		},
		{
			name: "non-zero loss",
			change: func(p *NewWagerTransactionParams) {
				p.Kind = WagerKindLoss
			},
			wantErr: ErrInvalidWagerAmount,
		},
		{
			name:    "invalid currency",
			change:  func(p *NewWagerTransactionParams) { p.Money.currency = "" },
			wantErr: ErrInvalidWagerCurrency,
		},
		{
			name:    "refund without reference",
			change:  func(p *NewWagerTransactionParams) { p.Kind = WagerKindRefund },
			wantErr: ErrMissingReference,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := validNewWagerTransactionParams()
			tt.change(&params)

			_, err := NewWagerTransaction(params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestNewOpeningWagerTransaction(t *testing.T) {
	wager, err := NewOpeningWagerTransaction(
		"transaction-1",
		"wallet-1",
		"player-1",
		Money{amount: 10000, currency: "BRL"},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wager.Kind() != WagerKindOpening ||
		wager.Status() != WagerStatusProcessed ||
		!wager.IsTerminal() {
		t.Error("expected a terminal, processed OPENING transaction")
	}
	if wager.ProviderID() != "" || wager.ExternalTransactionID() != "" {
		t.Error("OPENING must not contain external metadata")
	}
}

func TestNewOpeningWagerTransactionRejectsInvalidParams(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		walletID string
		playerID string
		money    Money
		wantErr  error
	}{
		{
			name: "missing transaction id", walletID: "wallet-1",
			playerID: "player-1", money: Money{amount: 1, currency: "BRL"},
			wantErr: ErrWagerTransactionIDRequired,
		},
		{
			name: "missing wallet id", id: "transaction-1",
			playerID: "player-1", money: Money{amount: 1, currency: "BRL"},
			wantErr: ErrWagerWalletIDRequired,
		},
		{
			name: "missing player id", id: "transaction-1", walletID: "wallet-1",
			money:   Money{amount: 1, currency: "BRL"},
			wantErr: ErrWagerPlayerIDRequired,
		},
		{
			name: "zero amount", id: "transaction-1", walletID: "wallet-1",
			playerID: "player-1", money: Money{currency: "BRL"},
			wantErr: ErrInvalidWagerAmount,
		},
		{
			name: "invalid currency", id: "transaction-1", walletID: "wallet-1",
			playerID: "player-1", money: Money{amount: 1},
			wantErr: ErrInvalidWagerCurrency,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewOpeningWagerTransaction(
				tt.id,
				tt.walletID,
				tt.playerID,
				tt.money,
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestRehydrateWagerTransaction(t *testing.T) {
	params := validRehydrateWagerTransactionParams()
	params.Kind = WagerKindWin
	params.ReferenceExternalTransactionID = "external-bet-1"
	params.ReferenceTransactionID = "transaction-bet-1"
	params.Status = WagerStatusProcessed
	resultBalance := Money{amount: 12500, currency: "BRL"}
	params.ResultBalance = &resultBalance

	wager, err := RehydrateWagerTransaction(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotBalance, ok := wager.ResultBalance()
	if !ok || gotBalance != resultBalance {
		t.Errorf("expected result balance %+v, got %+v", resultBalance, gotBalance)
	}
	if wager.Status() != params.Status ||
		wager.ReferenceTransactionID() != params.ReferenceTransactionID ||
		!wager.CreatedAt().Equal(params.CreatedAt) ||
		!wager.UpdatedAt().Equal(params.UpdatedAt) {
		t.Error("rehydration did not preserve the persisted snapshot")
	}
}

func TestRehydrateOpeningWagerTransaction(t *testing.T) {
	params := validRehydrateWagerTransactionParams()
	params.ProviderID = ""
	params.ExternalTransactionID = ""
	params.IdempotencyKey = ""
	params.PayloadHash = ""
	params.RoundID = ""
	params.GameID = ""
	params.Kind = WagerKindOpening
	params.Money = Money{amount: 10000, currency: "BRL"}
	params.Status = WagerStatusProcessed

	wager, err := RehydrateWagerTransaction(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wager.Kind() != WagerKindOpening || !wager.IsTerminal() {
		t.Error("expected a terminal OPENING transaction")
	}
}

func TestRehydrateWagerTransactionRejectsInvalidSnapshot(t *testing.T) {
	resultBalance := Money{amount: 7500, currency: "BRL"}

	tests := []struct {
		name    string
		change  func(*RehydrateWagerTransactionParams)
		wantErr error
	}{
		{
			name:    "missing required external metadata",
			change:  func(p *RehydrateWagerTransactionParams) { p.ProviderID = "" },
			wantErr: ErrWagerProviderIDRequired,
		},
		{
			name:    "invalid status",
			change:  func(p *RehydrateWagerTransactionParams) { p.Status = "UNKNOWN" },
			wantErr: ErrInvalidWagerStatus,
		},
		{
			name:    "zero timestamp",
			change:  func(p *RehydrateWagerTransactionParams) { p.CreatedAt = time.Time{} },
			wantErr: ErrInvalidWagerTimestamps,
		},
		{
			name: "updated before created",
			change: func(p *RehydrateWagerTransactionParams) {
				p.UpdatedAt = p.CreatedAt.Add(-time.Second)
			},
			wantErr: ErrInvalidWagerTimestamps,
		},
		{
			name: "pending with failure",
			change: func(p *RehydrateWagerTransactionParams) {
				p.FailureCode = "FAILURE"
			},
			wantErr: ErrInvalidWagerState,
		},
		{
			name: "pending with result",
			change: func(p *RehydrateWagerTransactionParams) {
				p.ResultBalance = &resultBalance
			},
			wantErr: ErrInvalidWagerState,
		},
		{
			name: "pending reference without reference",
			change: func(p *RehydrateWagerTransactionParams) {
				p.Status = WagerStatusPendingReference
			},
			wantErr: ErrMissingReference,
		},
		{
			name: "processed without result",
			change: func(p *RehydrateWagerTransactionParams) {
				p.Status = WagerStatusProcessed
			},
			wantErr: ErrInvalidWagerState,
		},
		{
			name: "processed with unresolved reference",
			change: func(p *RehydrateWagerTransactionParams) {
				p.Kind = WagerKindWin
				p.Status = WagerStatusProcessed
				p.ReferenceExternalTransactionID = "external-bet-1"
				p.ResultBalance = &resultBalance
			},
			wantErr: ErrInvalidWagerState,
		},
		{
			name: "processed with negative result",
			change: func(p *RehydrateWagerTransactionParams) {
				negative := Money{amount: -1, currency: "BRL"}
				p.Status = WagerStatusProcessed
				p.ResultBalance = &negative
			},
			wantErr: ErrInvalidWagerResultBalance,
		},
		{
			name: "rejected without failure code",
			change: func(p *RehydrateWagerTransactionParams) {
				p.Status = WagerStatusRejected
			},
			wantErr: ErrFailureCodeRequired,
		},
		{
			name: "failed with result balance",
			change: func(p *RehydrateWagerTransactionParams) {
				p.Status = WagerStatusFailed
				p.FailureCode = "PERMANENT_FAILURE"
				p.ResultBalance = &resultBalance
			},
			wantErr: ErrInvalidWagerState,
		},
		{
			name: "internal reference without external reference",
			change: func(p *RehydrateWagerTransactionParams) {
				p.ReferenceTransactionID = "transaction-bet-1"
			},
			wantErr: ErrInvalidWagerState,
		},
		{
			name: "opening in pending status",
			change: func(p *RehydrateWagerTransactionParams) {
				makeOpeningRehydrationParams(p)
				p.Status = WagerStatusPending
			},
			wantErr: ErrInvalidWagerState,
		},
		{
			name: "opening with external metadata",
			change: func(p *RehydrateWagerTransactionParams) {
				makeOpeningRehydrationParams(p)
				p.ProviderID = "provider-1"
			},
			wantErr: ErrInvalidWagerState,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := validRehydrateWagerTransactionParams()
			tt.change(&params)

			_, err := RehydrateWagerTransaction(params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func makeOpeningRehydrationParams(p *RehydrateWagerTransactionParams) {
	p.ProviderID = ""
	p.ExternalTransactionID = ""
	p.IdempotencyKey = ""
	p.PayloadHash = ""
	p.RoundID = ""
	p.GameID = ""
	p.Kind = WagerKindOpening
	p.Money = Money{amount: 10000, currency: "BRL"}
	p.Status = WagerStatusProcessed
}
