// Package config loads tariffd's configuration from the environment and fails
// fast, so a misconfigured service never boots into a state it cannot serve
// from. Every error names the variable it is about and shows a value that
// would have worked.
//
// There is no configuration library and no file format: the environment is
// the interface, which is the twelve-factor shape every orchestrator already
// speaks. tariff is a stateless compute library, so there is nothing here
// about databases, migrations or persistence — only the listen address, the
// optional token table, the log level, and the HTTP timeouts.
package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Environment variable names, collected so the docs, the errors and the code
// cannot drift apart.
const (
	EnvAddr            = "TARIFFD_ADDR"
	EnvTokens          = "TARIFFD_TOKENS"
	EnvLogLevel        = "TARIFFD_LOG_LEVEL"
	EnvReadTimeout     = "TARIFFD_READ_TIMEOUT"
	EnvWriteTimeout    = "TARIFFD_WRITE_TIMEOUT"
	EnvIdleTimeout     = "TARIFFD_IDLE_TIMEOUT"
	EnvShutdownTimeout = "TARIFFD_SHUTDOWN_TIMEOUT"
)

// Config is everything tariffd needs to run.
type Config struct {
	// Addr is the TCP listen address, e.g. ":8080".
	Addr string
	// Tokens is the set of accepted static bearer tokens. Empty means the /v1
	// compute endpoints are OPEN — tariffd stores nothing and attributes
	// nothing, so an open deployment leaks no data; it only offers free
	// computation. Put a proxy in front for anything public. See the README's
	// scope note.
	Tokens   []string
	LogLevel slog.Level

	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
}

// Load reads configuration through getenv (os.Getenv in production, a map in
// tests). It validates everything it can and returns the first problem it
// finds with the variable name and an example of a value that would have
// worked.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:            ":8080",
		LogLevel:        slog.LevelInfo,
		ReadTimeout:     10 * time.Second,
		WriteTimeout:    15 * time.Second,
		IdleTimeout:     120 * time.Second,
		ShutdownTimeout: 15 * time.Second,
	}

	if v := getenv(EnvAddr); v != "" {
		cfg.Addr = v
	}

	cfg.Tokens = parseTokens(getenv(EnvTokens))

	switch v := getenv(EnvLogLevel); v {
	case "", "info":
		cfg.LogLevel = slog.LevelInfo
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("%s must be one of debug, info, warn, error; got %q", EnvLogLevel, v)
	}

	for _, d := range []struct {
		env string
		dst *time.Duration
	}{
		{EnvReadTimeout, &cfg.ReadTimeout},
		{EnvWriteTimeout, &cfg.WriteTimeout},
		{EnvIdleTimeout, &cfg.IdleTimeout},
		{EnvShutdownTimeout, &cfg.ShutdownTimeout},
	} {
		v := getenv(d.env)
		if v == "" {
			continue
		}
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("%s must be a Go duration such as \"15s\", got %q", d.env, v)
		}
		if parsed <= 0 {
			return Config{}, fmt.Errorf("%s must be positive, got %q", d.env, v)
		}
		*d.dst = parsed
	}

	return cfg, nil
}

// parseTokens splits a comma-separated token list, trimming surrounding
// whitespace and dropping empty entries, so "a, b ," yields ["a", "b"] and ""
// yields none. Deduplication is not required: the authenticator scans a small
// static set and a repeated token authenticates the same as a single one.
func parseTokens(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			tokens = append(tokens, t)
		}
	}
	return tokens
}
