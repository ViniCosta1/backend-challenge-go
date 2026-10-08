package httpapi

import "testing"

func TestMoneyDTORejectsInvalidDecimalSyntax(t *testing.T) {
	for _, amount := range []string{"", "NaN", "Infinity", "1e2", "-1.00", "+1.00", "1.-1", "1.+1", "1.1", "1.001", ".00", " 1.00", "92233720368547758.08"} {
		t.Run(amount, func(t *testing.T) {
			if _, err := (moneyDTO{Amount: amount, Currency: "BRL"}).money(); err == nil {
				t.Fatalf("accepted invalid Money %q", amount)
			}
		})
	}
	money, err := (moneyDTO{Amount: "00025.00", Currency: "BRL"}).money()
	if err != nil || money.Amount() != 2500 {
		t.Fatalf("expected exact minor-unit conversion: amount=%d err=%v", money.Amount(), err)
	}
}
