package domain

import (
	"testing"
	"time"
)

func TestNewWallet(t *testing.T) {
	tests := []struct {
		name           string
		id             string
		playerID       string
		initialBalance Money
		wantErr        bool
	}{
		{
			name:     "create wallet with positive balance",
			id:       "wallet-1",
			playerID: "player-1",
			initialBalance: Money{
				amount:   10000,
				currency: "BRL",
			},
		},
		{
			name:     "create wallet with zero balance",
			id:       "wallet-1",
			playerID: "player-1",
			initialBalance: Money{
				amount:   0,
				currency: "BRL",
			},
		},
		{
			name:     "empty wallet id",
			id:       "",
			playerID: "player-1",
			initialBalance: Money{
				amount:   10000,
				currency: "BRL",
			},
			wantErr: true,
		},
		{
			name:     "empty player id",
			id:       "wallet-1",
			playerID: "",
			initialBalance: Money{
				amount:   10000,
				currency: "BRL",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wallet, err := NewWallet(
				tt.id,
				tt.playerID,
				tt.initialBalance,
			)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if wallet.id != tt.id {
				t.Errorf("expected id %s, got %s", tt.id, wallet.id)
			}

			if wallet.playerID != tt.playerID {
				t.Errorf("expected playerID %s, got %s", tt.playerID, wallet.playerID)
			}

			if wallet.balance.amount != tt.initialBalance.amount {
				t.Errorf(
					"expected balance %d, got %d",
					tt.initialBalance.amount,
					wallet.balance.amount,
				)
			}

			if wallet.version != 1 {
				t.Errorf("expected version 1, got %d", wallet.version)
			}

			if wallet.createdAt.IsZero() {
				t.Error("expected createdAt to be set")
			}

			if wallet.updatedAt.IsZero() {
				t.Error("expected updatedAt to be set")
			}
		})
	}
}

func TestWallet_Debit(t *testing.T) {
	tests := []struct {
		name        string
		balance     Money
		debit       Money
		wantBalance int64
		wantVersion int32
		wantErr     bool
	}{
		{
			name: "valid debit",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			debit: Money{
				amount:   2500,
				currency: "BRL",
			},
			wantBalance: 7500,
			wantVersion: 2,
		},
		{
			name: "debit entire balance",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			debit: Money{
				amount:   10000,
				currency: "BRL",
			},
			wantBalance: 0,
			wantVersion: 2,
		},
		{
			name: "insufficient balance",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			debit: Money{
				amount:   15000,
				currency: "BRL",
			},
			wantBalance: 10000,
			wantVersion: 1,
			wantErr:     true,
		},
		{
			name: "zero debit",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			debit: Money{
				amount:   0,
				currency: "BRL",
			},
			wantBalance: 10000,
			wantVersion: 1,
			wantErr:     true,
		},
		{
			name: "currency mismatch",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			debit: Money{
				amount:   2500,
				currency: "USD",
			},
			wantBalance: 10000,
			wantVersion: 1,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wallet := Wallet{
				id:        "wallet-1",
				playerID:  "player-1",
				currency:  tt.balance.currency,
				balance:   tt.balance,
				version:   1,
				createdAt: time.Now().UTC(),
				updatedAt: time.Now().UTC(),
			}

			err := wallet.Debit(tt.debit)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if wallet.balance.amount != tt.wantBalance {
				t.Errorf(
					"expected balance %d, got %d",
					tt.wantBalance,
					wallet.balance.amount,
				)
			}

			if wallet.version != tt.wantVersion {
				t.Errorf(
					"expected version %d, got %d",
					tt.wantVersion,
					wallet.version,
				)
			}
		})
	}
}

func TestWallet_Credit(t *testing.T) {
	tests := []struct {
		name        string
		balance     Money
		credit      Money
		wantBalance int64
		wantVersion int32
		wantErr     bool
	}{
		{
			name: "valid credit",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			credit: Money{
				amount:   2500,
				currency: "BRL",
			},
			wantBalance: 12500,
			wantVersion: 2,
		},
		{
			name: "credit zero balance",
			balance: Money{
				amount:   0,
				currency: "BRL",
			},
			credit: Money{
				amount:   2500,
				currency: "BRL",
			},
			wantBalance: 2500,
			wantVersion: 2,
		},
		{
			name: "zero credit",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			credit: Money{
				amount:   0,
				currency: "BRL",
			},
			wantBalance: 10000,
			wantVersion: 1,
			wantErr:     true,
		},
		{
			name: "currency mismatch",
			balance: Money{
				amount:   10000,
				currency: "BRL",
			},
			credit: Money{
				amount:   2500,
				currency: "USD",
			},
			wantBalance: 10000,
			wantVersion: 1,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wallet := Wallet{
				id:        "wallet-1",
				playerID:  "player-1",
				currency:  tt.balance.currency,
				balance:   tt.balance,
				version:   1,
				createdAt: time.Now().UTC(),
				updatedAt: time.Now().UTC(),
			}

			err := wallet.Credit(tt.credit)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if wallet.balance.amount != tt.wantBalance {
				t.Errorf(
					"expected balance %d, got %d",
					tt.wantBalance,
					wallet.balance.amount,
				)
			}

			if wallet.version != tt.wantVersion {
				t.Errorf(
					"expected version %d, got %d",
					tt.wantVersion,
					wallet.version,
				)
			}
		})
	}
}

func TestRehydrateWallet(t *testing.T) {
	createdAt := time.Date(
		2026, 10, 1,
		10, 0, 0, 0,
		time.UTC,
	)

	updatedAt := time.Date(
		2026, 10, 2,
		12, 0, 0, 0,
		time.UTC,
	)

	balance := Money{
		amount:   7500,
		currency: "BRL",
	}

	wallet, err := RehydrateWallet(
		"wallet-1",
		"player-1",
		"BRL",
		balance,
		4,
		createdAt,
		updatedAt,
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if wallet.id != "wallet-1" {
		t.Errorf("unexpected id: %s", wallet.id)
	}

	if wallet.playerID != "player-1" {
		t.Errorf("unexpected playerID: %s", wallet.playerID)
	}

	if wallet.balance.amount != 7500 {
		t.Errorf(
			"expected balance 7500, got %d",
			wallet.balance.amount,
		)
	}

	if wallet.version != 4 {
		t.Errorf(
			"expected version 4, got %d",
			wallet.version,
		)
	}

	if !wallet.createdAt.Equal(createdAt) {
		t.Errorf(
			"expected createdAt %v, got %v",
			createdAt,
			wallet.createdAt,
		)
	}

	if !wallet.updatedAt.Equal(updatedAt) {
		t.Errorf(
			"expected updatedAt %v, got %v",
			updatedAt,
			wallet.updatedAt,
		)
	}
}
