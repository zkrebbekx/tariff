package api

import (
	"net/http"
	"testing"
)

// TestProrationStripeGolden pins the Stripe upgrade vector to the second: a $10
// plan changing to $20 at the exact midpoint of a period credits −$5 for the
// unused old price, charges +$10 for the remaining new price, and nets $5.
func TestProrationStripeGolden(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"oldAmount": 1000,
		"newAmount": 2000,
		"currency":  usd,
		"period":    map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-01-03T00:00:00Z"},
		"at":        "2026-01-02T00:00:00Z",
		"basis":     "second",
	}
	resp, data := h.post("/v1/proration", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got prorationResponse
	decodeInto(t, data, &got)
	if got.Credit != -500 || got.Charge != 1000 || got.Net != 500 {
		t.Fatalf("got credit/charge/net = %d/%d/%d, want -500/1000/500; body: %s",
			got.Credit, got.Charge, got.Net, data)
	}
	if got.CreditFormatted != "-5.00" || got.ChargeFormatted != "10.00" || got.NetFormatted != "5.00" {
		t.Fatalf("formatted = %q/%q/%q, want -5.00/10.00/5.00",
			got.CreditFormatted, got.ChargeFormatted, got.NetFormatted)
	}
}

// TestProrationChargebeeDayGolden pins the Chargebee day-based vector: a $31 →
// $62 change with 16 of 31 days remaining credits −$16, charges $32, nets $16.
func TestProrationChargebeeDayGolden(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"oldAmount": 3100,
		"newAmount": 6200,
		"currency":  usd,
		"period":    map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-02-01T00:00:00Z"},
		"at":        "2026-01-16T00:00:00Z",
		"basis":     "day",
	}
	resp, data := h.post("/v1/proration", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got prorationResponse
	decodeInto(t, data, &got)
	if got.Credit != -1600 || got.Charge != 3200 || got.Net != 1600 {
		t.Fatalf("got credit/charge/net = %d/%d/%d, want -1600/3200/1600; body: %s",
			got.Credit, got.Charge, got.Net, data)
	}
}

// TestProrationTrialToPaid: oldAmount 0 is a trial-to-paid change, zero credit.
func TestProrationTrialToPaid(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"oldAmount": 0,
		"newAmount": 2000,
		"currency":  usd,
		"period":    map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-01-03T00:00:00Z"},
		"at":        "2026-01-02T00:00:00Z",
	}
	resp, data := h.post("/v1/proration", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got prorationResponse
	decodeInto(t, data, &got)
	if got.Credit != 0 || got.Charge != 1000 {
		t.Fatalf("got credit/charge = %d/%d, want 0/1000; body: %s", got.Credit, got.Charge, data)
	}
}

func TestProrationErrors(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{
			name: "end before start",
			body: map[string]any{"oldAmount": 1000, "newAmount": 2000, "currency": usd,
				"period": map[string]any{"start": "2026-01-03T00:00:00Z", "end": "2026-01-01T00:00:00Z"},
				"at":     "2026-01-02T00:00:00Z"},
			code: "bad_period",
		},
		{
			name: "negative amount",
			body: map[string]any{"oldAmount": -1, "newAmount": 2000, "currency": usd,
				"period": map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-01-03T00:00:00Z"},
				"at":     "2026-01-02T00:00:00Z"},
			code: "negative_amount",
		},
		{
			name: "unknown basis",
			body: map[string]any{"oldAmount": 1000, "newAmount": 2000, "currency": usd,
				"period": map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-01-03T00:00:00Z"},
				"at":     "2026-01-02T00:00:00Z", "basis": "hourly"},
			code: "invalid_argument",
		},
		{
			name: "bad timestamp",
			body: map[string]any{"oldAmount": 1000, "newAmount": 2000, "currency": usd,
				"period": map[string]any{"start": "yesterday", "end": "2026-01-03T00:00:00Z"},
				"at":     "2026-01-02T00:00:00Z"},
			code: "invalid_argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, data := h.post("/v1/proration", tc.body)
			wantError(t, resp, data, http.StatusBadRequest, tc.code)
		})
	}
}

// TestFractionExact returns the exact rational and its display decimal.
func TestFractionExact(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"period": map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-01-03T00:00:00Z"},
		"from":   "2026-01-02T00:00:00Z",
		"to":     "2026-01-03T00:00:00Z",
		"basis":  "second",
	}
	resp, data := h.post("/v1/proration/fraction", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got fractionResponse
	decodeInto(t, data, &got)
	if got.Fraction != "1/2" {
		t.Fatalf("fraction = %q, want \"1/2\"; body: %s", got.Fraction, data)
	}
	if got.FractionDecimal != 0.5 {
		t.Fatalf("fractionDecimal = %v, want 0.5", got.FractionDecimal)
	}
}

// TestFractionBadWindow: from strictly after to is bad_window.
func TestFractionBadWindow(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"period": map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-01-03T00:00:00Z"},
		"from":   "2026-01-03T00:00:00Z",
		"to":     "2026-01-02T00:00:00Z",
	}
	resp, data := h.post("/v1/proration/fraction", body)
	wantError(t, resp, data, http.StatusBadRequest, "bad_window")
}
