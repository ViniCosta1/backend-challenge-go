package domain

import (
	"errors"
	"testing"
)

func TestWagerTransactionReferenceLifecycle(t *testing.T) {
	wager := mustNewWagerTransaction(t, func(p *NewWagerTransactionParams) {
		p.Kind = WagerKindRefund
		p.ReferenceExternalTransactionID = "external-bet-1"
	})

	if err := wager.MarkPendingReference(); err != nil {
		t.Fatalf("unexpected pending-reference error: %v", err)
	}
	if wager.Status() != WagerStatusPendingReference {
		t.Fatalf("expected PENDING_REFERENCE, got %s", wager.Status())
	}

	if err := wager.ResolveReference("transaction-bet-1"); err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}

	resultBalance := Money{amount: 10000, currency: "BRL"}
	if err := wager.MarkProcessed(resultBalance); err != nil {
		t.Fatalf("unexpected processed error: %v", err)
	}

	gotBalance, ok := wager.ResultBalance()
	if wager.Status() != WagerStatusProcessed || !wager.IsTerminal() ||
		!ok || gotBalance != resultBalance {
		t.Error("expected a terminal processed transaction with its result balance")
	}
}

func TestWagerTransactionMarkProcessed(t *testing.T) {
	t.Run("processes transaction without reference", func(t *testing.T) {
		wager := mustNewWagerTransaction(t, nil)
		resultBalance := Money{amount: 7500, currency: "BRL"}

		if err := wager.MarkProcessed(resultBalance); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if wager.Status() != WagerStatusProcessed || !wager.IsTerminal() {
			t.Error("expected terminal PROCESSED status")
		}
	})

	tests := []struct {
		name          string
		changeWager   func(*WagerTransaction)
		resultBalance Money
		wantErr       error
	}{
		{
			name: "unresolved reference",
			changeWager: func(w *WagerTransaction) {
				w.referenceExternalTransactionID = "external-bet-1"
			},
			resultBalance: Money{amount: 7500, currency: "BRL"},
			wantErr:       ErrInvalidWagerState,
		},
		{
			name:          "negative result balance",
			resultBalance: Money{amount: -1, currency: "BRL"},
			wantErr:       ErrInvalidWagerResultBalance,
		},
		{
			name:          "different result currency",
			resultBalance: Money{amount: 7500, currency: "USD"},
			wantErr:       ErrInvalidWagerResultBalance,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wager := mustNewWagerTransaction(t, nil)
			if tt.changeWager != nil {
				tt.changeWager(&wager)
			}

			err := wager.MarkProcessed(tt.resultBalance)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
			if wager.Status() != WagerStatusPending {
				t.Errorf("expected status to remain PENDING, got %s", wager.Status())
			}
		})
	}
}

func TestWagerTransactionTerminalOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		transition func(*WagerTransaction) error
		wantStatus WagerStatus
	}{
		{
			name: "rejected",
			transition: func(w *WagerTransaction) error {
				return w.MarkRejected("INSUFFICIENT_BALANCE")
			},
			wantStatus: WagerStatusRejected,
		},
		{
			name: "failed",
			transition: func(w *WagerTransaction) error {
				return w.MarkFailed("PERMANENT_INFRA_FAILURE")
			},
			wantStatus: WagerStatusFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wager := mustNewWagerTransaction(t, nil)

			if err := tt.transition(&wager); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if wager.Status() != tt.wantStatus ||
				wager.FailureCode() == "" ||
				!wager.IsTerminal() {
				t.Errorf("expected terminal %s outcome", tt.wantStatus)
			}
		})
	}

	t.Run("requires failure code", func(t *testing.T) {
		wager := mustNewWagerTransaction(t, nil)
		if err := wager.MarkRejected(""); !errors.Is(err, ErrFailureCodeRequired) {
			t.Fatalf("expected ErrFailureCodeRequired, got %v", err)
		}
		if err := wager.MarkFailed(""); !errors.Is(err, ErrFailureCodeRequired) {
			t.Fatalf("expected ErrFailureCodeRequired, got %v", err)
		}
	})
}

func TestWagerTransactionReferenceValidation(t *testing.T) {
	t.Run("pending reference requires external reference", func(t *testing.T) {
		wager := mustNewWagerTransaction(t, nil)
		if err := wager.MarkPendingReference(); !errors.Is(err, ErrMissingReference) {
			t.Fatalf("expected ErrMissingReference, got %v", err)
		}
	})

	t.Run("pending reference transition cannot repeat", func(t *testing.T) {
		wager := mustNewWagerTransaction(t, func(p *NewWagerTransactionParams) {
			p.Kind = WagerKindWin
			p.ReferenceExternalTransactionID = "external-bet-1"
		})
		if err := wager.MarkPendingReference(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := wager.MarkPendingReference(); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expected ErrInvalidTransition, got %v", err)
		}
	})

	t.Run("resolve requires external reference", func(t *testing.T) {
		wager := mustNewWagerTransaction(t, nil)
		if err := wager.ResolveReference("transaction-bet-1"); !errors.Is(err, ErrMissingReference) {
			t.Fatalf("expected ErrMissingReference, got %v", err)
		}
	})

	t.Run("resolve requires internal transaction id", func(t *testing.T) {
		wager := mustNewWagerTransaction(t, func(p *NewWagerTransactionParams) {
			p.Kind = WagerKindWin
			p.ReferenceExternalTransactionID = "external-bet-1"
		})
		if err := wager.ResolveReference(""); !errors.Is(err, ErrReferenceTransactionIDRequired) {
			t.Fatalf("expected ErrReferenceTransactionIDRequired, got %v", err)
		}
	})
}

func TestWagerTransactionTerminalStateCannotChange(t *testing.T) {
	terminalStatuses := []WagerStatus{
		WagerStatusProcessed,
		WagerStatusRejected,
		WagerStatusFailed,
	}

	for _, status := range terminalStatuses {
		t.Run(string(status), func(t *testing.T) {
			wager := mustNewWagerTransaction(t, nil)
			wager.status = status

			transitions := []func() error{
				wager.MarkPendingReference,
				func() error { return wager.ResolveReference("transaction-bet-1") },
				func() error {
					return wager.MarkProcessed(Money{amount: 7500, currency: "BRL"})
				},
				func() error { return wager.MarkRejected("FAILURE") },
				func() error { return wager.MarkFailed("FAILURE") },
			}

			for _, transition := range transitions {
				if err := transition(); !errors.Is(err, ErrTerminalWager) {
					t.Errorf("expected ErrTerminalWager, got %v", err)
				}
			}
		})
	}
}
