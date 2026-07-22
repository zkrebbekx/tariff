package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/zkrebbekx/tariff"
)

// TestErrorMappingTable asserts every tariff sentinel maps to its documented
// 400 code, whether it arrives bare or wrapped — the whole taxonomy in one
// place, including the sentinels (bad_basis, bad_window, nil_step, ...) that
// the handlers cannot easily provoke over HTTP but the contract still promises.
func TestErrorMappingTable(t *testing.T) {
	cases := []struct {
		sentinel error
		code     string
	}{
		{tariff.ErrBadDiscount, "bad_discount"},
		{tariff.ErrNegativeAmount, "negative_amount"},
		{tariff.ErrNegativeQuantity, "negative_quantity"},
		{tariff.ErrBadPeriod, "bad_period"},
		{tariff.ErrBadWindow, "bad_window"},
		{tariff.ErrBadBasis, "bad_basis"},
		{tariff.ErrBadFloor, "bad_floor"},
		{tariff.ErrBadBalance, "bad_balance"},
		{tariff.ErrCurrencyMismatch, "currency_mismatch"},
		{tariff.ErrNilStep, "nil_step"},
		{tariff.ErrBadCurrency, "bad_currency"},
		{tariff.ErrBadAllowance, "bad_allowance"},
		{tariff.ErrNoRate, "no_rate"},
		{tariff.ErrEmptyTiers, "empty_tiers"},
		{tariff.ErrTierOrder, "tier_order"},
		{tariff.ErrUnknownModel, "unknown_model"},
		{tariff.ErrBadPackage, "bad_package"},
		{tariff.ErrOverflow, "overflow"},
		{tariff.ErrBadAllocation, "bad_allocation"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			// Bare.
			status, code, ok := mapError(tc.sentinel)
			if !ok || status != http.StatusBadRequest || code != tc.code {
				t.Fatalf("mapError(%v) = %d/%q/%v, want 400/%q/true", tc.sentinel, status, code, ok, tc.code)
			}
			// Wrapped, as the library returns them in practice.
			wrapped := fmt.Errorf("rating charge: %w", tc.sentinel)
			status, code, ok = mapError(wrapped)
			if !ok || status != http.StatusBadRequest || code != tc.code {
				t.Fatalf("mapError(wrapped %v) = %d/%q/%v, want 400/%q/true", tc.sentinel, status, code, ok, tc.code)
			}
		})
	}
}

// TestMapErrorUnknown: an error outside the taxonomy is the server's problem —
// a generic 500 internal, never leaking the underlying message.
func TestMapErrorUnknown(t *testing.T) {
	status, code, ok := mapError(errors.New("driver: connection reset by peer"))
	if ok || status != http.StatusInternalServerError || code != "internal" {
		t.Fatalf("mapError(unknown) = %d/%q/%v, want 500/internal/false", status, code, ok)
	}
}

// TestEveryTableCodeIsDistinct guards against a copy-paste giving two sentinels
// the same code.
func TestEveryTableCodeIsDistinct(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range errorTable {
		if seen[m.code] {
			t.Fatalf("duplicate code in errorTable: %q", m.code)
		}
		seen[m.code] = true
	}
}

// TestMux404 and TestMux405: routing failures are JSON, not the mux's plain
// text.
func TestMux404(t *testing.T) {
	h := newHarness(t)
	resp, data := h.request("GET", "/v1/does-not-exist", "", nil)
	wantError(t, resp, data, http.StatusNotFound, "not_found")
}

func TestMux405(t *testing.T) {
	h := newHarness(t)
	// /v1/rate is POST-only; a GET matches the path but not the method.
	resp, data := h.request("GET", "/v1/rate", "", nil)
	wantError(t, resp, data, http.StatusMethodNotAllowed, "method_not_allowed")
}

// TestBodyTooLarge: an over-cap body is a 413 with the body_too_large code.
func TestBodyTooLarge(t *testing.T) {
	h := newHarness(t)
	big := make([]byte, maxBodyBytes+1024)
	for i := range big {
		big[i] = 'a'
	}
	// A valid-JSON string longer than the cap, so the size limit fires before
	// any decode error.
	body := `{"model":"per_unit","currency":{"code":"USD","decimals":2,"rounding":"half_up"},"unitRate":"` + string(big) + `"}`
	resp, data := h.post("/v1/rate", body)
	wantError(t, resp, data, http.StatusRequestEntityTooLarge, "body_too_large")
}
