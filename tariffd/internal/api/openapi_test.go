package api

import (
	"net/http"
	"strings"
	"testing"
)

// TestOpenAPIDocumentsEveryRoute asserts the embedded OpenAPI description names
// every routed path. It is the coupling that keeps the hand-written spec honest
// as routes are added: a new endpoint with no documentation fails this test.
func TestOpenAPIDocumentsEveryRoute(t *testing.T) {
	h := newHarness(t)
	resp, data := h.request("GET", "/v1/openapi.yaml", "", nil)
	wantStatus(t, resp, data, http.StatusOK)
	spec := string(data)
	for _, path := range []string{
		"/v1/rate:",
		"/v1/proration:",
		"/v1/proration/fraction:",
		"/v1/compose:",
		"/v1/boundary:",
		"/v1/openapi.yaml:",
		"/healthz:",
	} {
		if !strings.Contains(spec, path) {
			t.Errorf("openapi.yaml does not document %s", strings.TrimSuffix(path, ":"))
		}
	}
}
