// Package boot wires tariffd together and runs it: authenticator, HTTP server,
// graceful shutdown. It is the composition root — everything above it is
// testable without a network, and there is nothing below it but the tariff
// library, which is stateless. No database, no store, no keyring: tariffd
// computes and returns.
package boot

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/zkrebbekx/tariff/tariffd/internal/api"
	"github.com/zkrebbekx/tariff/tariffd/internal/config"
)

// Run starts tariffd and serves until ctx is cancelled — which main wires to
// SIGTERM and SIGINT — then drains in-flight requests for up to the configured
// shutdown timeout.
//
// onListen, when non-nil, is called with the bound address once the listener
// is up; tests use it to find the ephemeral port.
func Run(ctx context.Context, cfg config.Config, logger *slog.Logger, onListen func(net.Addr)) error {
	handler := api.NewHandler(api.Deps{
		Auth:   api.NewAuth(cfg.Tokens),
		Logger: logger,
	})

	srv := &http.Server{
		Handler:      handler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		ErrorLog:     slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s (check %s): %w", cfg.Addr, config.EnvAddr, err)
	}
	if onListen != nil {
		onListen(ln.Addr())
	}
	// "open" advertises the auth posture in one glance: an operator who did not
	// mean to run open sees it in the boot line.
	auth := "token-required"
	if len(cfg.Tokens) == 0 {
		auth = "open"
	}
	logger.Info("tariffd listening",
		"addr", ln.Addr().String(),
		"auth", auth,
		"tokens", len(cfg.Tokens),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down", "drain_timeout", cfg.ShutdownTimeout.String())
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelDrain()
	if err := srv.Shutdown(drainCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("drain: %w", err)
	}
	logger.Info("stopped")
	return nil
}
