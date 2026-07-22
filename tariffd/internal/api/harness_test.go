package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The harness: the full HTTP handler over an httptest server. There is no
// store to wire — tariffd is stateless compute — so a harness is just the
// handler, optionally with a token set, behind a loopback server.

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type harness struct {
	t   *testing.T
	srv *httptest.Server
}

// newHarness builds a handler with the given tokens. No tokens means the /v1
// endpoints are open.
func newHarness(t *testing.T, tokens ...string) *harness {
	t.Helper()
	handler := NewHandler(Deps{
		Auth:   NewAuth(tokens),
		Logger: discardLogger(),
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &harness{t: t, srv: srv}
}

// request performs one HTTP call. body may be nil, a raw string, or a value to
// marshal as JSON. An empty token sends no Authorization header.
func (h *harness) request(method, path, token string, body any) (*http.Response, []byte) {
	h.t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			h.t.Fatalf("marshal request body: %v", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, h.srv.URL+path, rdr)
	if err != nil {
		h.t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.srv.Client().Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		h.t.Fatalf("read response body: %v", err)
	}
	return resp, data
}

// post is the common case: a JSON POST with no token (open harness).
func (h *harness) post(path string, body any) (*http.Response, []byte) {
	h.t.Helper()
	return h.request("POST", path, "", body)
}

func decodeInto(t *testing.T, data []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(data, dst); err != nil {
		t.Fatalf("unmarshal response %q: %v", data, err)
	}
}

func wantStatus(t *testing.T, resp *http.Response, body []byte, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("status = %d, want %d; body: %s", resp.StatusCode, want, body)
	}
}

// wantError asserts the error contract: the given status, the given code, and
// a JSON body carrying both.
func wantError(t *testing.T, resp *http.Response, body []byte, status int, code string) {
	t.Helper()
	wantStatus(t, resp, body, status)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("error Content-Type = %q, want application/json; body: %s", ct, body)
	}
	var eb errorBody
	decodeInto(t, body, &eb)
	if eb.Code != code {
		t.Fatalf("error code = %q, want %q; body: %s", eb.Code, code, body)
	}
	if eb.Error == "" {
		t.Fatalf("error body has no message: %s", body)
	}
}
