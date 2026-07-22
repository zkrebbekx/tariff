// Package api is tariffd's HTTP surface: a thin, JSON-speaking layer over the
// tariff rating library. tariff is a stateless compute core — it stores
// nothing, meters nothing, and attributes nothing — so this package is a pure
// request → compute → response mapping with no database, no persistence and no
// per-caller state. What it holds the line on is the one thing HTTP would
// erode: tariff's rates are exact rationals, so every rate crosses the wire as
// a string and is parsed exactly, never as a lossy JSON number. See dto.go.
package api

import (
	"log/slog"
	"net/http"
)

// Deps is everything the HTTP layer needs. It is deliberately tiny: a stateless
// compute service has no store, no keyring, no clock to inject.
type Deps struct {
	// Auth gates the /v1 compute endpoints. Nil means no tokens were
	// configured and those endpoints are open (see config and the README's
	// scope note). /healthz and the openapi document are always open.
	Auth *Auth
	// Logger receives one line per request. Never request bodies — they carry
	// price plans, which are confidential.
	Logger *slog.Logger
}

// Server carries the dependencies into the handlers.
type Server struct {
	auth   *Auth
	logger *slog.Logger
}

// NewHandler builds the complete tariffd handler: routing, optional
// authentication, request logging, panic recovery, and JSON-shaped routing
// errors.
func NewHandler(d Deps) http.Handler {
	s := &Server{auth: d.Auth, logger: d.Logger}

	// The compute endpoints. Go 1.22 pattern routing pins each to its method
	// and exact path; nothing here overlaps.
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/rate", s.handleRate)
	api.HandleFunc("POST /v1/proration", s.handleProration)
	api.HandleFunc("POST /v1/proration/fraction", s.handleFraction)
	api.HandleFunc("POST /v1/compose", s.handleCompose)
	api.HandleFunc("POST /v1/boundary", s.handleBoundary)

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", s.handleHealthz)
	// The openapi document is always open, so it is registered on the root mux
	// outside the auth wrapper. Its exact path is more specific than the "/v1/"
	// subtree below, so it wins for GET /v1/openapi.yaml while every other
	// /v1/* request falls through to the authenticated compute mux.
	root.HandleFunc("GET /v1/openapi.yaml", s.handleOpenAPI)
	root.Handle("/v1/", s.authenticate(api))

	// logging installs the status recorder; recovering sits just inside it so a
	// panic is caught, logged, and rendered as JSON through the same recorder
	// the logger reads.
	return s.logging(s.recovering(jsonMuxErrors(root)))
}

// handleHealthz is liveness: the process is up and serving. No auth — a
// probe carries no token — and no information beyond "up". A stateless compute
// service has no backing dependency to be un-ready against, so there is no
// separate readiness probe.
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
