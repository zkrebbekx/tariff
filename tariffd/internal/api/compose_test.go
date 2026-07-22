package api

import (
	"net/http"
	"testing"
)

// charge100 is a per-unit charge of $100 (one unit at a $100 rate), used as the
// gross line in the composition tests.
func charge100() map[string]any {
	return map[string]any{
		"type":     "charge",
		"charge":   map[string]any{"model": "per_unit", "currency": usd, "unitRate": "100"},
		"quantity": 1,
	}
}

// TestComposeOrderMatters is the headline composition property over HTTP: the
// SAME three operations in two orders yield different totals. Discount then
// floor: $100 → $90 → floored up to $95. Floor then discount: $100 clears the
// $95 floor untouched, then 10% off → $90.
func TestComposeOrderMatters(t *testing.T) {
	h := newHarness(t)

	discountThenFloor := map[string]any{
		"currency": usd,
		"steps": []map[string]any{
			charge100(),
			{"type": "percent_off", "pct": "1/10", "label": "10% off"},
			{"type": "minimum", "minor": 9500, "label": "minimum $95"},
		},
	}
	resp, data := h.post("/v1/compose", discountThenFloor)
	wantStatus(t, resp, data, http.StatusOK)
	var got composeResponse
	decodeInto(t, data, &got)
	if got.Total != 9500 || got.TotalFormatted != "95.00" {
		t.Fatalf("discount-then-floor total = %d (%q), want 9500 (\"95.00\"); body: %s", got.Total, got.TotalFormatted, data)
	}
	if got.Subtotal != 10000 {
		t.Fatalf("subtotal = %d, want 10000 (the gross charge)", got.Subtotal)
	}

	floorThenDiscount := map[string]any{
		"currency": usd,
		"steps": []map[string]any{
			charge100(),
			{"type": "minimum", "minor": 9500, "label": "minimum $95"},
			{"type": "percent_off", "pct": "1/10", "label": "10% off"},
		},
	}
	resp, data = h.post("/v1/compose", floorThenDiscount)
	wantStatus(t, resp, data, http.StatusOK)
	decodeInto(t, data, &got)
	if got.Total != 9000 || got.TotalFormatted != "90.00" {
		t.Fatalf("floor-then-discount total = %d (%q), want 9000 (\"90.00\"); body: %s", got.Total, got.TotalFormatted, data)
	}
}

// TestComposeStepEffects checks the per-step effect array: each step's signed
// contribution to the running total, in order.
func TestComposeStepEffects(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"currency": usd,
		"steps": []map[string]any{
			charge100(),
			{"type": "percent_off", "pct": "1/10", "label": "10% off"},
			{"type": "minimum", "minor": 9500, "label": "minimum $95"},
		},
	}
	resp, data := h.post("/v1/compose", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got composeResponse
	decodeInto(t, data, &got)
	if len(got.Steps) != 3 {
		t.Fatalf("steps = %d, want 3; body: %s", len(got.Steps), data)
	}
	wantEffects := []int64{10000, -1000, 500}
	for i, e := range wantEffects {
		if got.Steps[i].Effect != e {
			t.Fatalf("step %d effect = %d, want %d; body: %s", i, got.Steps[i].Effect, e, data)
		}
	}
	if got.Steps[1].EffectFormatted != "-10.00" {
		t.Fatalf("discount effectFormatted = %q, want \"-10.00\"", got.Steps[1].EffectFormatted)
	}
}

// TestComposeCreditBalanceEcho proves the post-draw balance is returned: the
// caller passes a prepaid balance by value, and the response says what it
// became. A $100 charge with a $300 credit draws $100, leaving $200.
func TestComposeCreditBalanceEcho(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"currency": usd,
		"steps": []map[string]any{
			charge100(),
			{"type": "credit", "balance": 30000, "label": "prepaid"},
		},
	}
	resp, data := h.post("/v1/compose", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got composeResponse
	decodeInto(t, data, &got)
	if got.Total != 0 {
		t.Fatalf("total = %d, want 0 (credit covers the whole charge); body: %s", got.Total, data)
	}
	credit := got.Steps[1]
	if credit.Balance == nil || *credit.Balance != 20000 {
		t.Fatalf("post-draw balance = %v, want 20000; body: %s", credit.Balance, data)
	}
	if credit.BalanceFormatted != "200.00" {
		t.Fatalf("post-draw balanceFormatted = %q, want \"200.00\"", credit.BalanceFormatted)
	}
	if credit.Effect != -10000 {
		t.Fatalf("credit effect = %d, want -10000 (the drawn amount)", credit.Effect)
	}
	// The charge step carries no balance.
	if got.Steps[0].Balance != nil {
		t.Fatalf("charge step should carry no balance, got %v", got.Steps[0].Balance)
	}
}

// TestComposeCommitmentCapsAtTotal: a commitment draw never takes more than the
// running total, so a $500 commitment against a $100 charge draws $100 and
// leaves $400.
func TestComposeCommitmentCapsAtTotal(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"currency": usd,
		"steps": []map[string]any{
			charge100(),
			{"type": "commitment", "balance": 50000, "label": "annual commitment"},
		},
	}
	resp, data := h.post("/v1/compose", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got composeResponse
	decodeInto(t, data, &got)
	if got.Total != 0 {
		t.Fatalf("total = %d, want 0; body: %s", got.Total, data)
	}
	if got.Steps[1].Balance == nil || *got.Steps[1].Balance != 40000 {
		t.Fatalf("post-draw commitment balance = %v, want 40000; body: %s", got.Steps[1].Balance, data)
	}
}

func TestComposeErrors(t *testing.T) {
	h := newHarness(t)
	jpy := map[string]any{"code": "JPY", "decimals": 0, "rounding": "half_even"}
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{
			name: "bad discount (pct above one)",
			body: map[string]any{"currency": usd, "steps": []map[string]any{
				charge100(), {"type": "percent_off", "pct": "2", "label": "200%?"},
			}},
			code: "bad_discount",
		},
		{
			name: "bad floor (negative minimum)",
			body: map[string]any{"currency": usd, "steps": []map[string]any{
				charge100(), {"type": "minimum", "minor": -1, "label": "floor"},
			}},
			code: "bad_floor",
		},
		{
			name: "bad balance (negative credit)",
			body: map[string]any{"currency": usd, "steps": []map[string]any{
				charge100(), {"type": "credit", "balance": -1, "label": "credit"},
			}},
			code: "bad_balance",
		},
		{
			name: "currency mismatch",
			body: map[string]any{"currency": usd, "steps": []map[string]any{
				{"type": "charge", "charge": map[string]any{"model": "per_unit", "currency": jpy, "unitRate": "100"}, "quantity": 1},
			}},
			code: "currency_mismatch",
		},
		{
			name: "unknown step type",
			body: map[string]any{"currency": usd, "steps": []map[string]any{
				{"type": "surge_pricing"},
			}},
			code: "invalid_argument",
		},
		{
			name: "charge step without charge object",
			body: map[string]any{"currency": usd, "steps": []map[string]any{
				{"type": "charge", "quantity": 1},
			}},
			code: "invalid_argument",
		},
		{
			name: "bad currency on invoice",
			body: map[string]any{"currency": map[string]any{"code": "USD", "decimals": 2}, "steps": []map[string]any{charge100()}},
			code: "bad_currency",
		},
		{
			name: "unparseable pct",
			body: map[string]any{"currency": usd, "steps": []map[string]any{
				charge100(), {"type": "percent_off", "pct": "half", "label": "x"},
			}},
			code: "invalid_argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, data := h.post("/v1/compose", tc.body)
			wantError(t, resp, data, http.StatusBadRequest, tc.code)
		})
	}
}
