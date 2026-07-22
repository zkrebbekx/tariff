package boot

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zkrebbekx/tariff/tariffd/internal/config"
)

// TestRunServesAndShutsDown boots the real server on an ephemeral port, serves
// a live request end to end, then cancels the context and asserts a clean
// graceful shutdown.
func TestRunServesAndShutsDown(t *testing.T) {
	cfg := config.Config{
		Addr:            "127.0.0.1:0",
		ReadTimeout:     5 * time.Second,
		WriteTimeout:    5 * time.Second,
		IdleTimeout:     30 * time.Second,
		ShutdownTimeout: 5 * time.Second,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	ctx, cancel := context.WithCancel(context.Background())
	addrCh := make(chan net.Addr, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg, logger, func(a net.Addr) { addrCh <- a })
	}()

	var addr net.Addr
	select {
	case addr = <-addrCh:
	case <-time.After(5 * time.Second):
		t.Fatal("server never reported its listen address")
	}

	base := "http://" + addr.String()

	// Liveness.
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}

	// A real compute request (open, since no tokens configured).
	rateBody := `{"model":"per_unit","currency":{"code":"USD","decimals":2,"rounding":"half_up"},"unitRate":"0.0006","flatFee":1000,"quantity":65000}`
	resp, err = http.Post(base+"/v1/rate", "application/json", strings.NewReader(rateBody))
	if err != nil {
		t.Fatalf("POST /v1/rate: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("rate status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"total":4900`) {
		t.Fatalf("rate body missing total 4900 ($49 exact): %s", body)
	}

	// Cancel and expect a clean shutdown.
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

// TestRunBadAddr: an unbindable address fails fast with an actionable error.
func TestRunBadAddr(t *testing.T) {
	cfg := config.Config{Addr: "256.256.256.256:99999", ShutdownTimeout: time.Second}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := Run(context.Background(), cfg, logger, nil)
	if err == nil {
		t.Fatal("Run with bad addr: want error, got nil")
	}
	if !strings.Contains(err.Error(), config.EnvAddr) {
		t.Errorf("error should name %s for the operator: %v", config.EnvAddr, err)
	}
}
