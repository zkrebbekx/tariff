package api

import (
	"net/http"
	"testing"
)

// TestRateStairstep exercises the stairstep model and its wire spelling: a flat
// fee for landing in a tier band, regardless of the exact quantity within it.
func TestRateStairstep(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"model":    "stairstep",
		"currency": usd,
		"tiers": []map[string]any{
			{"upTo": 10, "flatRate": 1000},
			{"last": true, "flatRate": 2500},
		},
		"quantity": 7,
	}
	resp, data := h.post("/v1/rate", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got rateResponse
	decodeInto(t, data, &got)
	if got.Total != 1000 {
		t.Fatalf("stairstep total = %d, want 1000 (landed in the first band); body: %s", got.Total, data)
	}
}

// TestRateRoundingModes exercises the floor and ceil rounding vocabulary (the
// half_up and half_even modes are covered elsewhere) and the three-decimal KWD
// scale, proving the minor-unit boundary is currency-driven.
func TestRateRoundingModes(t *testing.T) {
	h := newHarness(t)

	// A rate of 3.333 KWD per unit, 1 unit, 3 decimals: 3.333 → 3333 fils.
	// Floor and ceil differ only on the fractional tail; use a rate with a
	// non-terminating minor part to see them diverge.
	floor := map[string]any{
		"model":    "per_unit",
		"currency": map[string]any{"code": "KWD", "decimals": 3, "rounding": "floor"},
		"unitRate": "10/3", // 3.3333...
		"quantity": 1,
	}
	resp, data := h.post("/v1/rate", floor)
	wantStatus(t, resp, data, http.StatusOK)
	var got rateResponse
	decodeInto(t, data, &got)
	if got.Total != 3333 || got.TotalFormatted != "3.333" {
		t.Fatalf("KWD floor total = %d (%q), want 3333 (\"3.333\"); body: %s", got.Total, got.TotalFormatted, data)
	}

	ceil := map[string]any{
		"model":    "per_unit",
		"currency": map[string]any{"code": "KWD", "decimals": 3, "rounding": "ceil"},
		"unitRate": "10/3",
		"quantity": 1,
	}
	resp, data = h.post("/v1/rate", ceil)
	wantStatus(t, resp, data, http.StatusOK)
	decodeInto(t, data, &got)
	if got.Total != 3334 {
		t.Fatalf("KWD ceil total = %d, want 3334; body: %s", got.Total, data)
	}
}

// TestProrationParseErrorBranches covers the remaining per-field parse failures
// in the proration handler: a bad currency, a bad change instant, and a bad
// period end.
func TestProrationParseErrorBranches(t *testing.T) {
	h := newHarness(t)
	base := func() map[string]any {
		return map[string]any{
			"oldAmount": 1000, "newAmount": 2000, "currency": usd,
			"period": map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-02-01T00:00:00Z"},
			"at":     "2026-01-16T00:00:00Z",
		}
	}

	badCurrency := base()
	badCurrency["currency"] = map[string]any{"code": "USD", "decimals": 2, "rounding": "banker"}
	resp, data := h.post("/v1/proration", badCurrency)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")

	badAt := base()
	badAt["at"] = "noon"
	resp, data = h.post("/v1/proration", badAt)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")

	badEnd := base()
	badEnd["period"] = map[string]any{"start": "2026-01-01T00:00:00Z", "end": "later"}
	resp, data = h.post("/v1/proration", badEnd)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
}

// TestFractionParseErrorBranches covers the fraction handler's field parse
// failures: bad period, bad from, bad to, and unknown basis.
func TestFractionParseErrorBranches(t *testing.T) {
	h := newHarness(t)
	good := map[string]any{"start": "2026-01-01T00:00:00Z", "end": "2026-01-03T00:00:00Z"}

	cases := []struct {
		name string
		body map[string]any
	}{
		{"bad period", map[string]any{"period": map[string]any{"start": "x", "end": "2026-01-03T00:00:00Z"}, "from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00Z"}},
		{"bad from", map[string]any{"period": good, "from": "x", "to": "2026-01-02T00:00:00Z"}},
		{"bad to", map[string]any{"period": good, "from": "2026-01-01T00:00:00Z", "to": "x"}},
		{"bad basis", map[string]any{"period": good, "from": "2026-01-01T00:00:00Z", "to": "2026-01-02T00:00:00Z", "basis": "weekly"}},
		{"malformed body", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp *http.Response
			var data []byte
			if tc.body == nil {
				resp, data = h.post("/v1/proration/fraction", `{"period":`)
				wantError(t, resp, data, http.StatusBadRequest, "invalid_body")
				return
			}
			resp, data = h.post("/v1/proration/fraction", tc.body)
			wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
		})
	}
}

// TestBoundaryBadAnchor covers the anniversary-anchor parse failure.
func TestBoundaryBadAnchor(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{"anchor": "not-a-time", "from": "2026-01-31T00:00:00Z", "unit": "monthly"}
	resp, data := h.post("/v1/boundary", body)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
}

// TestComposeMalformedAndCurrency covers the compose handler's body-decode and
// currency-parse branches.
func TestComposeMalformedAndCurrency(t *testing.T) {
	h := newHarness(t)
	resp, data := h.post("/v1/compose", `{"steps":`)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_body")

	badCurrency := map[string]any{"currency": map[string]any{"code": "USD", "decimals": 2, "rounding": "wat"}, "steps": []map[string]any{charge100()}}
	resp, data = h.post("/v1/compose", badCurrency)
	wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
}

// TestComposeEmptySteps: a composition with no steps is a valid empty invoice.
func TestComposeEmptySteps(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{"currency": usd, "steps": []map[string]any{}}
	resp, data := h.post("/v1/compose", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got composeResponse
	decodeInto(t, data, &got)
	if got.Total != 0 || len(got.Steps) != 0 {
		t.Fatalf("empty compose = total %d, %d steps, want 0/0; body: %s", got.Total, len(got.Steps), data)
	}
}
