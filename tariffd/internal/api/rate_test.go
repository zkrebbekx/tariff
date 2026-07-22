package api

import (
	"net/http"
	"testing"
)

// usd is the currency body used across the rating tests.
var usd = map[string]any{"code": "USD", "decimals": 2, "rounding": "half_up"}

// TestRateExactRateRoundTrip is the load-bearing test: a sub-cent rate given as
// a decimal string and as the identical fraction must parse to the same exact
// rate and rate 65000 units + $10 flat to exactly $49.00 over HTTP. A float64
// $0.0006 would drift; the string-parsed big.Rat does not.
func TestRateExactRateRoundTrip(t *testing.T) {
	h := newHarness(t)

	for _, rate := range []string{"0.0006", "6/10000", "3/5000"} {
		body := map[string]any{
			"model":    "per_unit",
			"currency": usd,
			"unitRate": rate,
			"flatFee":  1000,
			"quantity": 65000,
		}
		resp, data := h.post("/v1/rate", body)
		wantStatus(t, resp, data, http.StatusOK)
		var got rateResponse
		decodeInto(t, data, &got)
		if got.Total != 4900 {
			t.Fatalf("rate %q: total = %d, want 4900; body: %s", rate, got.Total, data)
		}
		if got.TotalFormatted != "49.00" {
			t.Fatalf("rate %q: totalFormatted = %q, want \"49.00\"", rate, got.TotalFormatted)
		}
		// The usage line rate echoes back in canonical exact form and round-trips.
		if got.Lines[0].Rate != "3/5000" {
			t.Fatalf("rate %q: line rate = %q, want \"3/5000\" (the canonical form of 0.0006)", rate, got.Lines[0].Rate)
		}
	}
}

// TestRateRejectsNumericRate proves a rate is never accepted as a JSON number:
// a number in the string rate field fails to decode and is a 400 invalid_body,
// not a silently-truncated float.
func TestRateRejectsNumericRate(t *testing.T) {
	h := newHarness(t)
	raw := `{"model":"per_unit","currency":{"code":"USD","decimals":2,"rounding":"half_up"},"unitRate":0.0006,"quantity":65000}`
	resp, data := h.post("/v1/rate", raw)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_body")
}

// TestRateGraduatedGolden pins the Stripe graduated vector: quantity 6 across
// 1–5 @ $7, 6–10 @ $6.50, 11+ @ $6 is $41.50 = 5×$7 + 1×$6.50, and the two
// tier lines reconcile to the total.
func TestRateGraduatedGolden(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"model":    "graduated",
		"currency": usd,
		"tiers": []map[string]any{
			{"upTo": 5, "unitRate": "7"},
			{"upTo": 10, "unitRate": "6.5"},
			{"last": true, "unitRate": "6"},
		},
		"quantity": 6,
	}
	resp, data := h.post("/v1/rate", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got rateResponse
	decodeInto(t, data, &got)
	if got.Total != 4150 || got.TotalFormatted != "41.50" {
		t.Fatalf("total = %d (%q), want 4150 (\"41.50\"); body: %s", got.Total, got.TotalFormatted, data)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("lines = %d, want 2; body: %s", len(got.Lines), data)
	}
	var sum int64
	for _, l := range got.Lines {
		sum += l.Subtotal
	}
	if sum != got.Total {
		t.Fatalf("line subtotals sum to %d, want %d (must reconcile)", sum, got.Total)
	}
	// The $6.50 tier's rate echoes as the exact fraction 13/2.
	if got.Lines[1].Rate != "13/2" {
		t.Fatalf("second tier rate = %q, want \"13/2\"", got.Lines[1].Rate)
	}
}

// TestRateVolumeGolden pins the Stripe volume vector: quantity 6 charges the
// whole quantity at $6.50 = $39.00, one line.
func TestRateVolumeGolden(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"model":    "volume",
		"currency": usd,
		"tiers": []map[string]any{
			{"upTo": 5, "unitRate": "7"},
			{"upTo": 10, "unitRate": "6.5"},
			{"last": true, "unitRate": "6"},
		},
		"quantity": 6,
	}
	resp, data := h.post("/v1/rate", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got rateResponse
	decodeInto(t, data, &got)
	if got.Total != 3900 {
		t.Fatalf("total = %d, want 3900; body: %s", got.Total, data)
	}
}

// TestRatePackageGolden pins the Lago package vector: $5 per 100-unit block,
// 100 free, 201 units → $10 (two chargeable blocks after the free 100).
func TestRatePackageGolden(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"model":         "package",
		"currency":      usd,
		"packageSize":   100,
		"packagePrice":  500,
		"freeAllowance": 100,
		"quantity":      201,
	}
	resp, data := h.post("/v1/rate", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got rateResponse
	decodeInto(t, data, &got)
	if got.Total != 1000 || got.TotalFormatted != "10.00" {
		t.Fatalf("total = %d (%q), want 1000 (\"10.00\"); body: %s", got.Total, got.TotalFormatted, data)
	}
}

// TestRateJPYZeroDecimals proves the minor-unit scale is currency-driven, not
// hardcoded to cents: JPY has zero decimals, so Format renders whole units.
func TestRateJPYZeroDecimals(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"model":    "per_unit",
		"currency": map[string]any{"code": "JPY", "decimals": 0, "rounding": "half_even"},
		"unitRate": "150",
		"quantity": 3,
	}
	resp, data := h.post("/v1/rate", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got rateResponse
	decodeInto(t, data, &got)
	if got.Total != 450 || got.TotalFormatted != "450" {
		t.Fatalf("total = %d (%q), want 450 (\"450\"); body: %s", got.Total, got.TotalFormatted, data)
	}
}

// TestRateValidationErrors walks the rating error surface, each mapping to its
// mirrored sentinel code with a 400.
func TestRateValidationErrors(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{
			name: "negative quantity",
			body: map[string]any{"model": "per_unit", "currency": usd, "unitRate": "1", "quantity": -1},
			code: "negative_quantity",
		},
		{
			name: "empty tiers",
			body: map[string]any{"model": "graduated", "currency": usd, "tiers": []map[string]any{}, "quantity": 1},
			code: "empty_tiers",
		},
		{
			name: "tier order",
			body: map[string]any{"model": "graduated", "currency": usd, "tiers": []map[string]any{
				{"upTo": 10, "unitRate": "1"}, {"upTo": 5, "unitRate": "1"}, {"last": true, "unitRate": "1"},
			}, "quantity": 1},
			code: "tier_order",
		},
		{
			name: "missing rate (per_unit, no unitRate)",
			body: map[string]any{"model": "per_unit", "currency": usd, "quantity": 1},
			code: "no_rate",
		},
		{
			name: "bad package",
			body: map[string]any{"model": "package", "currency": usd, "packageSize": 0, "packagePrice": 100, "quantity": 1},
			code: "bad_package",
		},
		{
			name: "bad allowance",
			body: map[string]any{"model": "per_unit", "currency": usd, "unitRate": "1", "freeAllowance": -1, "quantity": 1},
			code: "bad_allowance",
		},
		{
			name: "bad currency (unset rounding)",
			body: map[string]any{"model": "per_unit", "currency": map[string]any{"code": "USD", "decimals": 2}, "unitRate": "1", "quantity": 1},
			code: "bad_currency",
		},
		{
			name: "bad currency (negative decimals)",
			body: map[string]any{"model": "per_unit", "currency": map[string]any{"code": "USD", "decimals": -1, "rounding": "half_up"}, "unitRate": "1", "quantity": 1},
			code: "bad_currency",
		},
		{
			name: "unknown model",
			body: map[string]any{"model": "surge", "currency": usd, "quantity": 1},
			code: "invalid_argument", // rejected at the handler enum, before the library
		},
		{
			name: "unparseable rate",
			body: map[string]any{"model": "per_unit", "currency": usd, "unitRate": "not-a-rate", "quantity": 1},
			code: "invalid_argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, data := h.post("/v1/rate", tc.body)
			wantError(t, resp, data, http.StatusBadRequest, tc.code)
		})
	}
}

// TestRateOverflow proves a rate that overflows int64 minor units is a 400
// overflow, not a wrapped-negative total.
func TestRateOverflow(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"model":    "per_unit",
		"currency": usd,
		"unitRate": "1000000000000",
		"quantity": 1000000000000,
	}
	resp, data := h.post("/v1/rate", body)
	wantError(t, resp, data, http.StatusBadRequest, "overflow")
}

// TestRateEmptyBody and malformed JSON are invalid_body.
func TestRateMalformedBody(t *testing.T) {
	h := newHarness(t)
	resp, data := h.post("/v1/rate", `{"model":`)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_body")

	resp, data = h.post("/v1/rate", `{"model":"per_unit","currency":{"code":"USD","decimals":2,"rounding":"half_up"},"unitRate":"1","quantity":1}{}`)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_body")

	resp, data = h.post("/v1/rate", `{"bogusField":1}`)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_body")
}
