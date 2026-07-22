package api

import (
	"net/http"
	"strings"
	"testing"
)

const testToken = "s3cret-token"

func validRateBody() map[string]any {
	return map[string]any{"model": "per_unit", "currency": usd, "unitRate": "1", "quantity": 1}
}

// TestAuthOpenWhenUnconfigured: with no tokens configured, /v1 is open — a
// compute request with no Authorization header succeeds. This is the
// documented stateless-compute posture (front it with a proxy for production).
func TestAuthOpenWhenUnconfigured(t *testing.T) {
	h := newHarness(t) // no tokens
	resp, data := h.request("POST", "/v1/rate", "", validRateBody())
	wantStatus(t, resp, data, http.StatusOK)
}

// TestAuthRequiredWhenConfigured: with a token configured, /v1 needs a valid
// bearer token — missing and wrong are 401, correct is 200.
func TestAuthRequiredWhenConfigured(t *testing.T) {
	h := newHarness(t, testToken)

	resp, data := h.request("POST", "/v1/rate", "", validRateBody())
	wantError(t, resp, data, http.StatusUnauthorized, "unauthorized")

	resp, data = h.request("POST", "/v1/rate", "wrong-token", validRateBody())
	wantError(t, resp, data, http.StatusUnauthorized, "unauthorized")

	resp, data = h.request("POST", "/v1/rate", testToken, validRateBody())
	wantStatus(t, resp, data, http.StatusOK)
}

// TestAuthNeverEchoesToken: a wrong token must not appear in the response body.
func TestAuthNeverEchoesToken(t *testing.T) {
	h := newHarness(t, testToken)
	resp, data := h.request("POST", "/v1/rate", "super-secret-wrong", validRateBody())
	wantStatus(t, resp, data, http.StatusUnauthorized)
	if strings.Contains(string(data), "super-secret-wrong") {
		t.Fatalf("response echoed the presented token: %s", data)
	}
}

// TestHealthzAlwaysOpen: /healthz needs no token, even when tokens are set.
func TestHealthzAlwaysOpen(t *testing.T) {
	h := newHarness(t, testToken)
	resp, data := h.request("GET", "/healthz", "", nil)
	wantStatus(t, resp, data, http.StatusOK)
	var got map[string]string
	decodeInto(t, data, &got)
	if got["status"] != "ok" {
		t.Fatalf("healthz = %s, want status ok", data)
	}
}

// TestOpenAPIAlwaysOpen: the openapi document needs no token, even when tokens
// are set — the contract of a stateless compute service is not a secret.
func TestOpenAPIAlwaysOpen(t *testing.T) {
	h := newHarness(t, testToken)
	resp, data := h.request("GET", "/v1/openapi.yaml", "", nil)
	wantStatus(t, resp, data, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); ct != "application/yaml" {
		t.Fatalf("Content-Type = %q, want application/yaml", ct)
	}
	if len(data) == 0 {
		t.Fatal("openapi.yaml body is empty")
	}
}
