package domain

import (
	"errors"
	"testing"
	"time"
)

func TestReversalDirectionProtectsReferenceInvariants(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*WagerTransaction)
		want   error
	}{
		{"provider", func(w *WagerTransaction) { w.providerID = "another-provider" }, ErrReferenceMismatch},
		{"player", func(w *WagerTransaction) { w.playerID = "another-player" }, ErrReferenceMismatch},
		{"wallet", func(w *WagerTransaction) { w.walletID = "another-wallet" }, ErrReferenceMismatch},
		{"round", func(w *WagerTransaction) { w.roundID = "another-round" }, ErrReferenceMismatch},
		{"currency", func(w *WagerTransaction) { w.money.currency = "USD" }, ErrReferenceMismatch},
		{"value", func(w *WagerTransaction) { w.money.amount++ }, ErrReferenceMismatch},
		{"pending", func(w *WagerTransaction) { w.status = WagerStatusPending }, ErrReferencePending},
		{"rejected", func(w *WagerTransaction) { w.status = WagerStatusRejected }, ErrReferenceIncompatible},
		{"kind", func(w *WagerTransaction) { w.kind = WagerKindWin }, ErrReferenceIncompatible},
	} {
		t.Run(test.name, func(t *testing.T) {
			reference := mustNewWagerTransaction(t, nil)
			if err := reference.MarkProcessed(Money{amount: 7500, currency: "BRL"}); err != nil {
				t.Fatal(err)
			}
			reversal := mustNewWagerTransaction(t, func(p *NewWagerTransactionParams) {
				p.Kind = WagerKindRefund
				p.ReferenceExternalTransactionID = reference.ExternalTransactionID()
			})
			test.change(&reference)
			_, err := reversal.ReversalDirection(&reference)
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestValidateWinReferenceProtectsRelationshipWithoutMatchingAmount(t *testing.T) {
	reference := mustNewWagerTransaction(t, nil)
	if err := reference.MarkProcessed(Money{amount: 7500, currency: "BRL"}); err != nil {
		t.Fatal(err)
	}
	win := mustNewWagerTransaction(t, func(p *NewWagerTransactionParams) {
		p.Kind = WagerKindWin
		p.Money = Money{amount: 9000, currency: "BRL"}
		p.ReferenceExternalTransactionID = reference.ExternalTransactionID()
	})
	if err := win.ValidateWinReference(&reference); err != nil {
		t.Fatalf("WIN amount must be independent from its BET: %v", err)
	}

	for _, test := range []struct {
		name   string
		change func(*WagerTransaction)
		want   error
	}{
		{"provider", func(w *WagerTransaction) { w.providerID = "another-provider" }, ErrReferenceMismatch},
		{"player", func(w *WagerTransaction) { w.playerID = "another-player" }, ErrReferenceMismatch},
		{"wallet", func(w *WagerTransaction) { w.walletID = "another-wallet" }, ErrReferenceMismatch},
		{"round", func(w *WagerTransaction) { w.roundID = "another-round" }, ErrReferenceMismatch},
		{"currency", func(w *WagerTransaction) { w.money.currency = "USD" }, ErrReferenceMismatch},
		{"pending", func(w *WagerTransaction) { w.status = WagerStatusPending }, ErrReferencePending},
		{"rejected", func(w *WagerTransaction) { w.status = WagerStatusRejected }, ErrReferenceIncompatible},
		{"kind", func(w *WagerTransaction) { w.kind = WagerKindWin }, ErrReferenceIncompatible},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := reference
			test.change(&changed)
			if err := win.ValidateWinReference(&changed); !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
		})
	}
}

func TestRejectedBalanceAndRetryStateArePreserved(t *testing.T) {
	wager := mustNewWagerTransaction(t, func(p *NewWagerTransactionParams) {
		p.Kind = WagerKindRefund
		p.ReferenceExternalTransactionID = "original-bet"
	})
	if err := wager.MarkPendingReference(); err != nil {
		t.Fatal(err)
	}
	next := time.Now().UTC().Add(time.Second)
	if err := wager.ScheduleReferenceRetry(next); err != nil {
		t.Fatal(err)
	}
	if err := wager.RegisterReferenceAttempt(); err != nil {
		t.Fatal(err)
	}
	if err := wager.MarkRejectedWithBalance(FailureCodeReferenceNotFound, Money{amount: 2000, currency: "BRL"}); err != nil {
		t.Fatal(err)
	}
	if _, scheduled := wager.ReferenceNextAttemptAt(); scheduled {
		t.Fatal("terminal rejection must clear its retry schedule")
	}
	if err := wager.RegisterReferenceAttempt(); !errors.Is(err, ErrInvalidReferenceRetry) {
		t.Fatalf("terminal retry must fail, got %v", err)
	}
	params := validRehydrateWagerTransactionParams()
	params.Status = WagerStatusRejected
	params.FailureCode = FailureCodeInsufficientBalance
	balance := Money{amount: 2000, currency: "BRL"}
	params.ResultBalance = &balance
	params.ReferenceAttempts = 1
	rehydrated, err := RehydrateWagerTransaction(params)
	if err != nil {
		t.Fatal(err)
	}
	balance.amount = 9999
	got, ok := rehydrated.ResultBalance()
	if !ok || got.amount != 2000 || rehydrated.ReferenceAttempts() != 1 {
		t.Fatal("rehydration must preserve and copy the persisted result")
	}
}

func TestReferenceRetryRejectsInvalidStates(t *testing.T) {
	wager := mustNewWagerTransaction(t, nil)
	if err := wager.ScheduleReferenceRetry(time.Now().UTC().Add(time.Second)); !errors.Is(err, ErrInvalidReferenceRetry) {
		t.Fatalf("schedule on non-pending reference: %v", err)
	}
	params := validRehydrateWagerTransactionParams()
	params.ReferenceAttempts = -1
	if _, err := RehydrateWagerTransaction(params); !errors.Is(err, ErrInvalidReferenceRetry) {
		t.Fatalf("negative persisted attempts: %v", err)
	}
	params.ReferenceAttempts = 0
	next := params.CreatedAt.Add(time.Second)
	params.ReferenceNextAttemptAt = &next
	if _, err := RehydrateWagerTransaction(params); !errors.Is(err, ErrInvalidReferenceRetry) {
		t.Fatalf("schedule outside pending-reference state: %v", err)
	}
}
