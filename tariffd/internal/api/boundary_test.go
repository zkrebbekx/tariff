package api

import (
	"net/http"
	"testing"
)

// TestBoundaryMonthEnd walks the classic month-end trap over HTTP: a Jan 31
// anniversary anchor steps to Feb 28 (2026 is not a leap year) and then, from
// Feb 28, back to Mar 31 — never sticking at the 28th, because the clamp is
// always measured from the original anchor day.
func TestBoundaryMonthEnd(t *testing.T) {
	h := newHarness(t)

	step := func(from string) string {
		body := map[string]any{
			"anchor":   "2026-01-31T00:00:00Z",
			"from":     from,
			"unit":     "monthly",
			"calendar": false,
		}
		resp, data := h.post("/v1/boundary", body)
		wantStatus(t, resp, data, http.StatusOK)
		var got boundaryResponse
		decodeInto(t, data, &got)
		return got.Next
	}

	if next := step("2026-01-31T00:00:00Z"); next != "2026-02-28T00:00:00Z" {
		t.Fatalf("Jan 31 → %q, want 2026-02-28T00:00:00Z", next)
	}
	if next := step("2026-02-28T00:00:00Z"); next != "2026-03-31T00:00:00Z" {
		t.Fatalf("Feb 28 → %q, want 2026-03-31T00:00:00Z (must not stick at the 28th)", next)
	}
}

// TestBoundaryCalendarAligned: calendar-aligned monthly boundaries land on the
// 1st, and the anchor is not needed.
func TestBoundaryCalendarAligned(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"from":     "2026-01-15T00:00:00Z",
		"unit":     "monthly",
		"calendar": true,
	}
	resp, data := h.post("/v1/boundary", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got boundaryResponse
	decodeInto(t, data, &got)
	if got.Next != "2026-02-01T00:00:00Z" {
		t.Fatalf("calendar monthly from Jan 15 → %q, want 2026-02-01T00:00:00Z", got.Next)
	}
}

// TestBoundaryYearly: an anniversary yearly step.
func TestBoundaryYearly(t *testing.T) {
	h := newHarness(t)
	body := map[string]any{
		"anchor": "2024-02-29T00:00:00Z",
		"from":   "2026-01-01T00:00:00Z",
		"unit":   "yearly",
	}
	resp, data := h.post("/v1/boundary", body)
	wantStatus(t, resp, data, http.StatusOK)
	var got boundaryResponse
	decodeInto(t, data, &got)
	// From 2026-01-01, the next Feb-29 anchor anniversary is 2026-02-28 (common year).
	if got.Next != "2026-02-28T00:00:00Z" {
		t.Fatalf("Feb 29 anchor, yearly, from 2026-01-01 → %q, want 2026-02-28T00:00:00Z", got.Next)
	}
}

func TestBoundaryErrors(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{
			name: "anniversary without anchor",
			body: map[string]any{"from": "2026-01-31T00:00:00Z", "unit": "monthly"},
			code: "invalid_argument",
		},
		{
			name: "unknown unit",
			body: map[string]any{"anchor": "2026-01-31T00:00:00Z", "from": "2026-01-31T00:00:00Z", "unit": "weekly"},
			code: "invalid_argument",
		},
		{
			name: "bad from",
			body: map[string]any{"anchor": "2026-01-31T00:00:00Z", "from": "soon", "unit": "monthly"},
			code: "invalid_argument",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, data := h.post("/v1/boundary", tc.body)
			wantError(t, resp, data, http.StatusBadRequest, tc.code)
		})
	}
}
