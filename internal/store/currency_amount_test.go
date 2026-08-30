package store

import (
	"encoding/json"
	"testing"
)

func TestCurrencyAmountPreservesExactHundredthsAcrossJSON(t *testing.T) {
	for encoded, want := range map[string]CurrencyAmount{
		`0`: 0, `100`: 10_000, `100.5`: 10_050, `"100.50"`: 10_050, `1.2300`: 123,
	} {
		var amount CurrencyAmount
		if err := json.Unmarshal([]byte(encoded), &amount); err != nil {
			t.Fatalf("json.Unmarshal(%q): %v", encoded, err)
		}
		if amount != want {
			t.Fatalf("json.Unmarshal(%q)=%d, want %d", encoded, amount, want)
		}
		roundTrip, err := json.Marshal(amount)
		if err != nil {
			t.Fatal(err)
		}
		var repeated CurrencyAmount
		if err := json.Unmarshal(roundTrip, &repeated); err != nil || repeated != amount {
			t.Fatalf("round trip %q=(%d,%v), want %d", roundTrip, repeated, err, amount)
		}
	}
}

func TestCurrencyAmountRejectsUnsafeOrInexactValues(t *testing.T) {
	for _, encoded := range []string{`-1`, `1.001`, `1e2`, `"+1"`, `9000000000000001`, `null`, `""`} {
		var amount CurrencyAmount
		if err := json.Unmarshal([]byte(encoded), &amount); err == nil {
			t.Fatalf("json.Unmarshal(%q) unexpectedly succeeded as %s", encoded, amount.String())
		}
	}
}
