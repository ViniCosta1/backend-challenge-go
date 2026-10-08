package domain

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParseAmount(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		{
			name:    "valid amount",
			input:   "25.37",
			want:    2537,
			wantErr: false,
		},
		{
			name:    "zero amount",
			input:   "0.00",
			want:    0,
			wantErr: false,
		},
		{
			name:    "amount without decimals",
			input:   "25",
			wantErr: true,
		},
		{
			name:    "amount with one decimal",
			input:   "25.0",
			wantErr: true,
		},
		{
			name:    "amount with three decimals",
			input:   "25.000",
			wantErr: true,
		},
		{name: "maximum int64", input: "92233720368547758.07", want: math.MaxInt64},
		{name: "overflow by cents", input: "92233720368547758.08", wantErr: true},
		{name: "overflow integer component", input: "922337203685477580.00", wantErr: true},
		{name: "empty", input: "", wantErr: true},
		{name: "leading plus", input: "+1.00", wantErr: true},
		{name: "negative", input: "-1.00", wantErr: true},
		{name: "signed cents plus", input: "1.+1", wantErr: true},
		{name: "signed cents minus", input: "1.-1", wantErr: true},
		{name: "NaN", input: "NaN", wantErr: true},
		{name: "Infinity", input: "Infinity", wantErr: true},
		{name: "scientific notation", input: "1e2.00", wantErr: true},
		{name: "leading whitespace", input: " 1.00", wantErr: true},
		{name: "trailing whitespace", input: "1.00 ", wantErr: true},
		{name: "extra decimal point", input: "1.00.00", wantErr: true},
		{name: "missing integer component", input: ".00", wantErr: true},
		{name: "non decimal character", input: "1a.00", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAmount(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Errorf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestNewMoney(t *testing.T) {
	tests := []struct {
		name     string
		amount   int64
		currency string
		wantErr  bool
	}{
		{
			name:     "valid money",
			amount:   2537,
			currency: "BRL",
			wantErr:  false,
		},
		{
			name:     "zero is valid",
			amount:   0,
			currency: "BRL",
			wantErr:  false,
		},
		{
			name:     "negative amount",
			amount:   -100,
			currency: "BRL",
			wantErr:  true,
		},
		{
			name:     "unsupported currency",
			amount:   100,
			currency: "USD",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money, err := NewMoney(tt.amount, tt.currency)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if money.amount != tt.amount {
				t.Errorf("expected amount %d, got %d", tt.amount, money.amount)
			}

			if money.currency != tt.currency {
				t.Errorf("expected currency %s, got %s", tt.currency, money.currency)
			}
		})
	}
}

func TestMoney_Add(t *testing.T) {
	tests := []struct {
		name    string
		a       Money
		b       Money
		want    int64
		wantErr bool
	}{
		{
			name: "sum two positive amounts",
			a:    Money{amount: 1000, currency: "BRL"},
			b:    Money{amount: 500, currency: "BRL"},
			want: 1500,
		},
		{
			name: "sum positive and negative amounts",
			a:    Money{amount: 1000, currency: "BRL"},
			b:    Money{amount: -500, currency: "BRL"},
			want: 500,
		},
		{
			name: "sum two negative amounts",
			a:    Money{amount: -1000, currency: "BRL"},
			b:    Money{amount: -500, currency: "BRL"},
			want: -1500,
		},
		{
			name:    "currency mismatch",
			a:       Money{amount: 1000, currency: "BRL"},
			b:       Money{amount: 500, currency: "USD"},
			wantErr: true,
		},
		{
			name:    "positive overflow",
			a:       Money{amount: math.MaxInt64, currency: "BRL"},
			b:       Money{amount: 1, currency: "BRL"},
			wantErr: true,
		},
		{
			name:    "negative overflow",
			a:       Money{amount: math.MinInt64, currency: "BRL"},
			b:       Money{amount: -1, currency: "BRL"},
			wantErr: true,
		},
		{
			name: "maximum valid sum",
			a:    Money{amount: math.MaxInt64 - 1, currency: "BRL"},
			b:    Money{amount: 1, currency: "BRL"},
			want: math.MaxInt64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.a.Add(tt.b)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.amount != tt.want {
				t.Errorf("expected %d, got %d", tt.want, result.amount)
			}

			if result.currency != tt.a.currency {
				t.Errorf("expected currency %s, got %s",
					tt.a.currency, result.currency)
			}
		})
	}
}

func TestMoney_Subtract(t *testing.T) {
	tests := []struct {
		name    string
		a       Money
		b       Money
		want    int64
		wantErr bool
	}{
		{
			name: "subtract two positive amounts",
			a:    Money{amount: 1000, currency: "BRL"},
			b:    Money{amount: 500, currency: "BRL"},
			want: 500,
		},
		{
			name: "subtract resulting in negative amount",
			a:    Money{amount: 500, currency: "BRL"},
			b:    Money{amount: 1000, currency: "BRL"},
			want: -500,
		},
		{
			name: "subtract negative amount",
			a:    Money{amount: 1000, currency: "BRL"},
			b:    Money{amount: -500, currency: "BRL"},
			want: 1500,
		},
		{
			name: "subtract equal amounts",
			a:    Money{amount: 1000, currency: "BRL"},
			b:    Money{amount: 1000, currency: "BRL"},
			want: 0,
		},
		{
			name:    "currency mismatch",
			a:       Money{amount: 1000, currency: "BRL"},
			b:       Money{amount: 500, currency: "USD"},
			wantErr: true,
		},
		{
			name:    "positive overflow",
			a:       Money{amount: math.MaxInt64, currency: "BRL"},
			b:       Money{amount: -1, currency: "BRL"},
			wantErr: true,
		},
		{
			name:    "negative overflow",
			a:       Money{amount: math.MinInt64, currency: "BRL"},
			b:       Money{amount: 1, currency: "BRL"},
			wantErr: true,
		},
		{
			name: "minimum valid subtraction",
			a:    Money{amount: math.MinInt64 + 1, currency: "BRL"},
			b:    Money{amount: 1, currency: "BRL"},
			want: math.MinInt64,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.a.Subtract(tt.b)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.amount != tt.want {
				t.Errorf("expected %d, got %d", tt.want, result.amount)
			}

			if result.currency != tt.a.currency {
				t.Errorf("expected currency %s, got %s",
					tt.a.currency, result.currency)
			}
		})
	}
}

func TestMoney_Negate(t *testing.T) {
	tests := []struct {
		name    string
		amount  int64
		want    int64
		wantErr bool
	}{
		{
			name:   "negate positive amount",
			amount: 2500,
			want:   -2500,
		},
		{
			name:   "negate negative amount",
			amount: -2500,
			want:   2500,
		},
		{
			name:   "negate zero",
			amount: 0,
			want:   0,
		},
		{
			name:    "negate minimum int64",
			amount:  math.MinInt64,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			money := Money{
				amount:   tt.amount,
				currency: "BRL",
			}

			result, err := money.Negate()

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.amount != tt.want {
				t.Errorf("expected %d, got %d", tt.want, result.amount)
			}

			if money.amount != tt.amount {
				t.Error("Negate modified the original Money")
			}
		})
	}
}

func TestMoney_Compare(t *testing.T) {
	tests := []struct {
		name    string
		a       Money
		b       Money
		want    int
		wantErr bool
	}{
		{
			name: "greater than",
			a:    Money{amount: 1000, currency: "BRL"},
			b:    Money{amount: 500, currency: "BRL"},
			want: 1,
		},
		{
			name: "less than",
			a:    Money{amount: 500, currency: "BRL"},
			b:    Money{amount: 1000, currency: "BRL"},
			want: -1,
		},
		{
			name: "equal amounts",
			a:    Money{amount: 1000, currency: "BRL"},
			b:    Money{amount: 1000, currency: "BRL"},
			want: 0,
		},
		{
			name: "compare negative amounts",
			a:    Money{amount: -500, currency: "BRL"},
			b:    Money{amount: 500, currency: "BRL"},
			want: -1,
		},
		{
			name:    "currency mismatch",
			a:       Money{amount: 1000, currency: "BRL"},
			b:       Money{amount: 1000, currency: "USD"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.a.Compare(tt.b)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tt.want {
				t.Errorf("expected %d, got %d", tt.want, result)
			}
		})
	}
}

func TestMoney_MarshalJSON(t *testing.T) {
	tests := []struct {
		name  string
		money Money
		want  string
	}{
		{
			name:  "positive amount",
			money: Money{amount: 2537, currency: "BRL"},
			want:  `{"amount":"25.37","currency":"BRL"}`,
		},
		{
			name:  "zero amount",
			money: Money{amount: 0, currency: "BRL"},
			want:  `{"amount":"0.00","currency":"BRL"}`,
		},
		{
			name:  "amount with zero cents",
			money: Money{amount: 2500, currency: "BRL"},
			want:  `{"amount":"25.00","currency":"BRL"}`,
		},
		{
			name:  "negative amount",
			money: Money{amount: -2537, currency: "BRL"},
			want:  `{"amount":"-25.37","currency":"BRL"}`,
		},
		{
			name:  "negative cents",
			money: Money{amount: -5, currency: "BRL"},
			want:  `{"amount":"-0.05","currency":"BRL"}`,
		},
		{
			name:  "maximum int64",
			money: Money{amount: math.MaxInt64, currency: "BRL"},
			want:  `{"amount":"92233720368547758.07","currency":"BRL"}`,
		},
		{
			name:  "minimum int64",
			money: Money{amount: math.MinInt64, currency: "BRL"},
			want:  `{"amount":"-92233720368547758.08","currency":"BRL"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := json.Marshal(tt.money)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if string(result) != tt.want {
				t.Errorf("expected %s, got %s", tt.want, result)
			}
		})
	}
}

func TestZeroMoney(t *testing.T) {
	t.Run("valid currency", func(t *testing.T) {
		money, err := ZeroMoney("BRL")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if money.amount != 0 {
			t.Errorf("expected zero, got %d", money.amount)
		}

		if money.currency != "BRL" {
			t.Errorf("expected BRL, got %s", money.currency)
		}
	})

	t.Run("unsupported currency", func(t *testing.T) {
		_, err := ZeroMoney("USD")

		if err == nil {
			t.Fatal("expected error for unsupported currency")
		}
	})
}
