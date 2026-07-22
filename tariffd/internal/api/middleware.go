package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// maxBodyBytes caps request bodies. A charge spec, a proration or a
// composition is a small document; a body over this size is far more likely a
// mistake or an attack than a price plan.
const maxBodyBytes = 1 << 20 // 1 MiB

// writeJSON renders one response. Every body tariffd produces goes through
// here, so the Content-Type and the encoding cannot drift between handlers.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// Encoding a value built from our own DTOs cannot fail except for a broken
	// connection, which has no useful recovery.
	_ = json.NewEncoder(w).Encode(body)
}

// decodeBody decodes a JSON request body strictly: the size is capped, unknown
// fields are rejected, and trailing garbage is rejected. A rate sent as a JSON
// number lands here as a type error against the string field — the intended
// rejection, since a rate is never a bare number over the wire.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return &Error{Status: http.StatusRequestEntityTooLarge, Code: "body_too_large",
				Message: fmt.Sprintf("request body exceeds %d bytes", maxErr.Limit)}
		}
		return badRequest("invalid_body", "request body is not valid JSON for this endpoint: "+err.Error())
	}
	if dec.More() {
		return badRequest("invalid_body", "request body has trailing data after the JSON value")
	}
	return nil
}

// parseTime parses one RFC 3339 timestamp, naming the field in the error.
func parseTime(field, value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, badRequest("invalid_argument", fmt.Sprintf(
			"%s must be an RFC 3339 timestamp such as 2026-01-01T00:00:00Z, got %q", field, value))
	}
	return t, nil
}

// authenticate guards everything under /v1/ except the always-open openapi
// document (which is routed outside this wrapper). A nil Auth means no tokens
// were configured and the compute endpoints are open — tariffd stores and
// attributes nothing, so an open deployment leaks nothing; it only offers free
// computation, which the README's scope note says to fence with a proxy.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.auth == nil {
			next.ServeHTTP(w, r)
			return
		}
		const scheme = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, scheme) {
			writeJSON(w, http.StatusUnauthorized, errorBody{
				Error: "missing bearer token: send Authorization: Bearer <token>",
				Code:  "unauthorized",
			})
			return
		}
		if !s.auth.Valid(strings.TrimPrefix(header, scheme)) {
			// The presented token is never echoed back — it is a secret, even
			// (especially) a wrong one.
			writeJSON(w, http.StatusUnauthorized, errorBody{
				Error: "unknown token",
				Code:  "unauthorized",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the response status for the request log.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// recovering turns a handler panic into the JSON error contract rather than a
// bare dropped connection. Nothing here is expected to panic; this is a
// backstop that keeps "every error is a JSON body, nothing internal leaks"
// true even for a logic bug, and logs the panic for the operator. The
// recovered value never reaches the client.
func (s *Server) recovering(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.logger.Error("handler panic", "method", r.Method, "path", r.URL.Path, "panic", v)
				if rec, ok := w.(*statusRecorder); !ok || rec.status == 0 {
					writeJSON(w, http.StatusInternalServerError, errorBody{
						Error: "internal error",
						Code:  "internal",
					})
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// logging emits one structured line per request: method, path, status and
// duration.
//
// Request bodies are never logged, at any level. They carry price plans —
// per-unit rates, tier schedules, discounts — which are a business secret, and
// a debug log that duplicated them would be an unversioned second copy of
// exactly the data the caller sent us to keep confidential. The status and
// timing are enough to operate the service.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// muxErrorWriter rewrites http.ServeMux's own plain-text 404 and 405 responses
// into the JSON error contract, so "every error is a JSON body" holds for
// routing failures too. Handler responses pass through untouched — they set an
// application/json Content-Type before writing, which is the discriminator.
type muxErrorWriter struct {
	http.ResponseWriter
	wroteHeader bool
	intercepted bool
}

func (w *muxErrorWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	fromMux := (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) &&
		!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json")
	if !fromMux {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.intercepted = true
	body := errorBody{Error: "no such endpoint", Code: "not_found"}
	if code == http.StatusMethodNotAllowed {
		body = errorBody{Error: "method not allowed for this endpoint", Code: "method_not_allowed"}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.ResponseWriter.WriteHeader(code)
	_ = json.NewEncoder(w.ResponseWriter).Encode(body)
}

func (w *muxErrorWriter) Write(b []byte) (int, error) {
	if w.intercepted {
		// Swallow the mux's text body; the JSON body is already written.
		return len(b), nil
	}
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func jsonMuxErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&muxErrorWriter{ResponseWriter: w}, r)
	})
}
