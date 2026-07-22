package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestCurrencyDecimalsRequired pins that an omitted currency.decimals is
// rejected rather than silently rated at zero decimals — the silent
// wrong-invoice class the service otherwise forbids for the rounding mode.
func TestCurrencyDecimalsRequired(t *testing.T) {
	h := newHarness(t)

	t.Run("Given a rate request whose currency omits decimals", func(t *testing.T) {
		body := `{"model":"per_unit","currency":{"code":"USD","rounding":"half_up"},"unitRate":"0.0006","flatFee":1000,"quantity":65000}`
		resp, data := h.post("/v1/rate", body)
		t.Run("Then it is rejected, not silently rated at 0 decimals", func(t *testing.T) {
			wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
		})
	})

	t.Run("Given a currency with an explicit decimals of 0 (a real JPY)", func(t *testing.T) {
		body := `{"model":"per_unit","currency":{"code":"JPY","decimals":0,"rounding":"half_up"},"unitRate":"150","quantity":3}`
		resp, data := h.post("/v1/rate", body)
		t.Run("Then it is accepted", func(t *testing.T) {
			wantStatus(t, resp, data, http.StatusOK)
		})
	})
}

// TestRateStringMagnitudeBounded pins that a rate string cannot force an
// astronomical big.Rat allocation: a scientific exponent past the cap, or an
// over-long string, is a clean 400 with a small response, not a memory blow-up.
func TestRateStringMagnitudeBounded(t *testing.T) {
	h := newHarness(t)

	t.Run("Given a rate with a huge scientific exponent", func(t *testing.T) {
		body := `{"model":"per_unit","currency":{"code":"USD","decimals":2,"rounding":"half_up"},"unitRate":"1e1000000","quantity":1}`
		resp, data := h.post("/v1/rate", body)
		t.Run("Then it is a small 400, not a giant allocation echoed back", func(t *testing.T) {
			wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
			if len(data) > 1000 {
				t.Fatalf("response is %d bytes; a rejected rate must not echo a huge value", len(data))
			}
		})
	})

	t.Run("Given a rate string past the length cap", func(t *testing.T) {
		body := `{"model":"per_unit","currency":{"code":"USD","decimals":2,"rounding":"half_up"},"unitRate":"1/` +
			strings.Repeat("9", 100) + `","quantity":1}`
		resp, data := h.post("/v1/rate", body)
		t.Run("Then it is rejected", func(t *testing.T) {
			wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
		})
	})

	t.Run("Given a modest scientific rate within the cap", func(t *testing.T) {
		body := `{"model":"per_unit","currency":{"code":"USD","decimals":2,"rounding":"half_up"},"unitRate":"1e-4","flatFee":0,"quantity":10000}`
		resp, data := h.post("/v1/rate", body)
		t.Run("Then it is accepted and exact ($1.00)", func(t *testing.T) {
			wantStatus(t, resp, data, http.StatusOK)
			var got struct {
				Total int64 `json:"total"`
			}
			decodeInto(t, data, &got)
			if got.Total != 100 {
				t.Fatalf("total = %d, want 100 (10000 x $0.0001)", got.Total)
			}
		})
	})
}

// TestComposeStepCountBounded pins that a composition past the step cap is
// rejected before the O(n^2) effect pass, so a hostile body cannot burn CPU.
func TestComposeStepCountBounded(t *testing.T) {
	h := newHarness(t)

	build := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"currency":{"code":"USD","decimals":2,"rounding":"half_up"},"steps":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`{"type":"minimum","minor":0,"label":"m"}`)
		}
		b.WriteString(`]}`)
		return b.String()
	}

	t.Run("Given a composition past the step cap", func(t *testing.T) {
		resp, data := h.post("/v1/compose", build(maxComposeSteps+1))
		t.Run("Then it is rejected with invalid_argument", func(t *testing.T) {
			wantError(t, resp, data, http.StatusBadRequest, "invalid_argument")
		})
	})

	t.Run("Given a composition at the step cap", func(t *testing.T) {
		resp, data := h.post("/v1/compose", build(maxComposeSteps))
		t.Run("Then it is accepted", func(t *testing.T) {
			wantStatus(t, resp, data, http.StatusOK)
		})
	})
}
