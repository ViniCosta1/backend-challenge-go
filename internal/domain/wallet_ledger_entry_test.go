package domain

import (
	"errors"
	"testing"
	"time"
)

func ledgerMoney(amount int64) Money {
	return Money{amount: amount, currency: "BRL"}
}

func validWalletLedgerEntryParams() NewWalletLedgerEntryParams {
	return NewWalletLedgerEntryParams{
		ID:            "ledger-entry-1",
		WalletID:      "wallet-1",
		TransactionID: "transaction-1",
		Direction:     LedgerDirectionDebit,
		Money:         ledgerMoney(2500),
		BalanceBefore: ledgerMoney(10000),
		BalanceAfter:  ledgerMoney(7500),
	}
}

func TestNewWalletLedgerEntry(t *testing.T) {
	tests := []struct {
		name      string
		direction LedgerDirection
		after     Money
	}{
		{
			name:      "valid credit",
			direction: LedgerDirectionCredit,
			after:     ledgerMoney(12500),
		},
		{
			name:      "valid debit",
			direction: LedgerDirectionDebit,
			after:     ledgerMoney(7500),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := validWalletLedgerEntryParams()
			params.Direction = tt.direction
			params.BalanceAfter = tt.after

			entry, err := NewWalletLedgerEntry(params)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if entry.direction != tt.direction ||
				entry.money != params.Money ||
				entry.balanceBefore != params.BalanceBefore ||
				entry.balanceAfter != params.BalanceAfter {
				t.Error("ledger entry did not preserve the financial movement")
			}
			if entry.createdAt.IsZero() || entry.createdAt.Location() != time.UTC {
				t.Error("expected a non-zero UTC creation timestamp")
			}
		})
	}
}

func TestNewWalletLedgerEntryRejectsInvalidEntry(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*NewWalletLedgerEntryParams)
		wantErr error
	}{
		{
			name: "inconsistent debit",
			change: func(p *NewWalletLedgerEntryParams) {
				p.BalanceAfter = ledgerMoney(8000)
			},
			wantErr: ErrInconsistentLedgerEntry,
		},
		{
			name: "inconsistent credit",
			change: func(p *NewWalletLedgerEntryParams) {
				p.Direction = LedgerDirectionCredit
				p.BalanceAfter = ledgerMoney(12000)
			},
			wantErr: ErrInconsistentLedgerEntry,
		},
		{
			name:    "invalid direction",
			change:  func(p *NewWalletLedgerEntryParams) { p.Direction = "UNKNOWN" },
			wantErr: ErrInvalidLedgerDirection,
		},
		{
			name:    "missing entry id",
			change:  func(p *NewWalletLedgerEntryParams) { p.ID = "" },
			wantErr: ErrLedgerEntryIDRequired,
		},
		{
			name:    "missing wallet id",
			change:  func(p *NewWalletLedgerEntryParams) { p.WalletID = "" },
			wantErr: ErrLedgerWalletIDRequired,
		},
		{
			name: "missing transaction id",
			change: func(p *NewWalletLedgerEntryParams) {
				p.TransactionID = ""
			},
			wantErr: ErrLedgerTransactionIDRequired,
		},
		{
			name: "incompatible movement currency",
			change: func(p *NewWalletLedgerEntryParams) {
				p.Money.currency = "USD"
			},
			wantErr: ErrInvalidLedgerMoney,
		},
		{
			name: "incompatible balance currency",
			change: func(p *NewWalletLedgerEntryParams) {
				p.BalanceAfter.currency = "USD"
			},
			wantErr: ErrInvalidLedgerBalance,
		},
		{
			name:    "zero movement",
			change:  func(p *NewWalletLedgerEntryParams) { p.Money.amount = 0 },
			wantErr: ErrInvalidLedgerMoney,
		},
		{
			name: "negative balance",
			change: func(p *NewWalletLedgerEntryParams) {
				p.BalanceAfter.amount = -1
			},
			wantErr: ErrInvalidLedgerBalance,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := validWalletLedgerEntryParams()
			tt.change(&params)

			_, err := NewWalletLedgerEntry(params)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestRehydrateWalletLedgerEntry(t *testing.T) {
	createdAt := time.Date(2026, 10, 7, 12, 30, 0, 0, time.UTC)
	params := RehydrateWalletLedgerEntryParams{
		ID:            "ledger-entry-1",
		WalletID:      "wallet-1",
		TransactionID: "transaction-1",
		Direction:     LedgerDirectionCredit,
		Money:         ledgerMoney(2500),
		BalanceBefore: ledgerMoney(10000),
		BalanceAfter:  ledgerMoney(12500),
		CreatedAt:     createdAt,
	}

	entry, err := RehydrateWalletLedgerEntry(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := WalletLedgerEntry{
		id:            params.ID,
		walletID:      params.WalletID,
		transactionID: params.TransactionID,
		direction:     params.Direction,
		money:         params.Money,
		balanceBefore: params.BalanceBefore,
		balanceAfter:  params.BalanceAfter,
		createdAt:     params.CreatedAt,
	}
	if entry != want {
		t.Errorf("expected rehydrated entry %+v, got %+v", want, entry)
	}
}

func TestRehydrateWalletLedgerEntryRejectsInvalidState(t *testing.T) {
	params := RehydrateWalletLedgerEntryParams{
		ID:            "ledger-entry-1",
		WalletID:      "wallet-1",
		TransactionID: "transaction-1",
		Direction:     LedgerDirectionDebit,
		Money:         ledgerMoney(2500),
		BalanceBefore: ledgerMoney(10000),
		BalanceAfter:  ledgerMoney(7500),
	}

	_, err := RehydrateWalletLedgerEntry(params)
	if !errors.Is(err, ErrInvalidLedgerCreatedAt) {
		t.Fatalf("expected ErrInvalidLedgerCreatedAt, got %v", err)
	}
}
