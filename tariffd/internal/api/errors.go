package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/zkrebbekx/tariff"
)

// errorBody is the wire shape of every error tariffd returns. The code is a
// stable string mirroring tariff's sentinel taxonomy; clients switch on it,
// never on the human-readable message.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// Error is an error minted by a handler itself — a parse failure, an auth
// failure, an unknown enum value — carrying its own status and code.
type Error struct {
	Status  int
	Code    string
	Message string
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Message }

func badRequest(code, msg string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Message: msg}
}

// mapping ties one tariff sentinel to its HTTP rendering.
type mapping struct {
	sentinel error
	status   int
	code     string
}

// errorTable maps tariff's errors.Is taxonomy onto HTTP. Every tariff
// validation sentinel is a 400 — they all describe a request the caller can
// fix (a bad rate, an empty tier schedule, a currency with no rounding mode,
// an out-of-range discount). Overflow is included: it means an amount the
// caller supplied does not fit int64 minor units, which is the caller's number
// to shrink, not the server's bug.
//
// The order is not significant: tariff's sentinels are disjoint, so no single
// error matches two rows.
var errorTable = []mapping{
	{tariff.ErrBadDiscount, http.StatusBadRequest, "bad_discount"},
	{tariff.ErrNegativeAmount, http.StatusBadRequest, "negative_amount"},
	{tariff.ErrNegativeQuantity, http.StatusBadRequest, "negative_quantity"},
	{tariff.ErrBadPeriod, http.StatusBadRequest, "bad_period"},
	{tariff.ErrBadWindow, http.StatusBadRequest, "bad_window"},
	{tariff.ErrBadBasis, http.StatusBadRequest, "bad_basis"},
	{tariff.ErrBadFloor, http.StatusBadRequest, "bad_floor"},
	{tariff.ErrBadBalance, http.StatusBadRequest, "bad_balance"},
	{tariff.ErrCurrencyMismatch, http.StatusBadRequest, "currency_mismatch"},
	{tariff.ErrNilStep, http.StatusBadRequest, "nil_step"},
	{tariff.ErrBadCurrency, http.StatusBadRequest, "bad_currency"},
	{tariff.ErrBadAllowance, http.StatusBadRequest, "bad_allowance"},
	{tariff.ErrNoRate, http.StatusBadRequest, "no_rate"},
	{tariff.ErrEmptyTiers, http.StatusBadRequest, "empty_tiers"},
	{tariff.ErrTierOrder, http.StatusBadRequest, "tier_order"},
	{tariff.ErrUnknownModel, http.StatusBadRequest, "unknown_model"},
	{tariff.ErrBadPackage, http.StatusBadRequest, "bad_package"},
	{tariff.ErrOverflow, http.StatusBadRequest, "overflow"},
	{tariff.ErrBadAllocation, http.StatusBadRequest, "bad_allocation"},
}

// mapError resolves an error from the tariff library to its HTTP status and
// code. The bool reports whether the error was recognised; an unrecognised
// error is the server's problem, not the caller's, and becomes a generic 500
// so that no internal error string leaks into a response body.
func mapError(err error) (status int, code string, ok bool) {
	for _, m := range errorTable {
		if errors.Is(err, m.sentinel) {
			return m.status, m.code, true
		}
	}
	return http.StatusInternalServerError, "internal", false
}

// respondError renders any error as the JSON error contract. Handler-minted
// *Error values pass through as-is; tariff errors go through the mapping
// table; anything unrecognised — and anything mapped to a 5xx — is logged
// server-side and reported as a generic "internal" without the underlying
// message, so a stray library string can never reach a client.
func (s *Server) respondError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		writeJSON(w, apiErr.Status, errorBody{Error: apiErr.Message, Code: apiErr.Code})
		return
	}
	status, code, known := mapError(err)
	if !known || status >= http.StatusInternalServerError {
		s.logger.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err.Error())
		msg := "internal error"
		if status != http.StatusInternalServerError {
			msg = strings.ToLower(http.StatusText(status))
		}
		writeJSON(w, status, errorBody{Error: msg, Code: code})
		return
	}
	// A recognised 4xx: the tariff message is safe and useful to return — it
	// names the field, not an internal detail.
	writeJSON(w, status, errorBody{Error: err.Error(), Code: code})
}
