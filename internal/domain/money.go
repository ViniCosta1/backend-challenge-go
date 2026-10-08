package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Money struct {
	amount   int64
	currency string
}

func NewMoney(amount int64, currency string) (Money, error) {
	if amount < 0 {
		return Money{}, errors.New("amount must be non-negative")
	}

	if currency != "BRL" {
		return Money{}, errors.New("currency must be 'BRL'")
	}

	return Money{
		amount:   amount,
		currency: currency,
	}, nil
}

// Amount String to Int64
func ParseAmount(amount string) (int64, error) {
	if amount == "" {
		return 0, errors.New("amount must not be empty")
	}

	parts := strings.Split(amount, ".")
	if len(parts) != 2 {
		return 0, errors.New("amount must contain exactly one '.'")
	}

	integers, decimals := parts[0], parts[1]
	if integers == "" || len(decimals) != 2 {
		return 0, errors.New("amount must use decimal format with exactly two decimal places")
	}
	for _, part := range []string{integers, decimals} {
		for _, character := range part {
			if character < '0' || character > '9' {
				return 0, errors.New("amount must contain only decimal digits")
			}
		}
	}

	i, err := strconv.ParseInt(integers, 10, 64)
	if err != nil {
		return 0, err
	}

	d, err := strconv.ParseInt(decimals, 10, 64)
	if err != nil {
		return 0, err
	}

	if i > (math.MaxInt64-d)/100 {
		return 0, errors.New("amount overflow")
	}

	return i*100 + d, nil
}

// Add Operation
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("currency mismatch")
	}

	// Positive overflow
	if other.amount > 0 && m.amount > math.MaxInt64-other.amount {
		return Money{}, errors.New("amount overflow")
	}

	// Negative overflow
	if other.amount < 0 && m.amount < math.MinInt64-other.amount {
		return Money{}, errors.New("amount underflow")
	}

	result := m.amount + other.amount

	return Money{
		amount:   result,
		currency: m.currency,
	}, nil
}

// Subtraction Operation
func (m Money) Subtract(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, errors.New("currency mismatch")
	}

	// Positive overflow
	if other.amount < 0 && m.amount > math.MaxInt64+other.amount {
		return Money{}, errors.New("amount overflow")
	}

	// Negative overflow
	if other.amount > 0 && m.amount < math.MinInt64+other.amount {
		return Money{}, errors.New("amount underflow")
	}

	result := m.amount - other.amount

	return Money{
		amount:   result,
		currency: m.currency,
	}, nil
}

// Negate Operation
func (m Money) Negate() (Money, error) {
	if m.amount == math.MinInt64 {
		return Money{}, errors.New("amount overflow")
	}

	return Money{
		amount:   m.amount * -1,
		currency: m.currency,
	}, nil
}

// Compare Operation
// -1 -> Less
// 0  -> Equal
// 1  -> More
func (m Money) Compare(other Money) (int, error) {
	if m.currency != other.currency {
		return 0, errors.New("currency mismatch")
	}

	if m.amount < other.amount {
		return -1, nil
	}

	if m.amount > other.amount {
		return 1, nil
	}

	return 0, nil
}

func (m Money) MarshalJSON() ([]byte, error) {
	decimals := m.amount % 100

	// negative decimals, remove -
	if decimals < 0 {
		decimals *= -1
	}

	formatted := fmt.Sprintf(
		"%d.%02d",
		m.amount/100,
		decimals,
	)

	if m.amount < 0 && m.amount/100 == 0 {
		formatted = "-" + formatted
	}

	return json.Marshal(struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}{
		Amount:   formatted,
		Currency: m.currency,
	})
}

func ZeroMoney(currency string) (Money, error) {
	return NewMoney(0, currency)
}

func (m Money) Amount() int64 {
	return m.amount
}

func (m Money) Currency() string {
	return m.currency
}
