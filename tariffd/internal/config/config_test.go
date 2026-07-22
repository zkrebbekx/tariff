package config

import (
	"log/slog"
	"testing"
	"time"
)

// envMap returns a getenv function backed by a map, for hermetic config tests.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(envMap(nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if len(cfg.Tokens) != 0 {
		t.Errorf("Tokens = %v, want none (open)", cfg.Tokens)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 15s", cfg.ShutdownTimeout)
	}
}

func TestLoadTokens(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ,, c ", []string{"a", "b", "c"}}, // trimmed, empties dropped
		{",", nil},
	}
	for _, tc := range cases {
		cfg, err := Load(envMap(map[string]string{EnvTokens: tc.in}))
		if err != nil {
			t.Fatalf("Load(%q): %v", tc.in, err)
		}
		if len(cfg.Tokens) != len(tc.want) {
			t.Fatalf("Load(%q) tokens = %v, want %v", tc.in, cfg.Tokens, tc.want)
		}
		for i := range tc.want {
			if cfg.Tokens[i] != tc.want[i] {
				t.Fatalf("Load(%q) tokens = %v, want %v", tc.in, cfg.Tokens, tc.want)
			}
		}
	}
}

func TestLoadAddrOverride(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{EnvAddr: ":9000"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9000" {
		t.Errorf("Addr = %q, want :9000", cfg.Addr)
	}
}

func TestLoadLogLevel(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{EnvLogLevel: "debug"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if _, err := Load(envMap(map[string]string{EnvLogLevel: "loud"})); err == nil {
		t.Error("Load with bad log level: want error, got nil")
	}
}

func TestLoadTimeouts(t *testing.T) {
	cfg, err := Load(envMap(map[string]string{EnvReadTimeout: "5s", EnvShutdownTimeout: "30s"}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("ReadTimeout = %v, want 5s", cfg.ReadTimeout)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %v, want 30s", cfg.ShutdownTimeout)
	}
}

func TestLoadTimeoutErrors(t *testing.T) {
	if _, err := Load(envMap(map[string]string{EnvWriteTimeout: "soon"})); err == nil {
		t.Error("Load with unparseable duration: want error")
	}
	if _, err := Load(envMap(map[string]string{EnvIdleTimeout: "-1s"})); err == nil {
		t.Error("Load with non-positive duration: want error")
	}
}
