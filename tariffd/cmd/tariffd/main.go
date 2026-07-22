// Command tariffd is tariff as a standalone REST service: the deployment shape
// for polyglot shops that cannot import the Go library. It is stateless compute
// — request in, computed amounts out — with no database and no persistence.
// Configuration is environment-only; authentication is an optional static
// bearer-token set. See the repository README's "Running the service" section
// and /v1/openapi.yaml.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zkrebbekx/tariff/tariffd/internal/boot"
	"github.com/zkrebbekx/tariff/tariffd/internal/config"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tariffd:", err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	if err := serve(cfg, logger); err != nil {
		logger.Error("tariffd failed", "err", err.Error())
		os.Exit(1)
	}
}

// serve runs the service until SIGTERM or SIGINT, then drains. It exists so
// that main's os.Exit calls sit outside any function with defers.
func serve(cfg config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return boot.Run(ctx, cfg, logger, nil)
}
