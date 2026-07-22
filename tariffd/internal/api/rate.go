package api

import "net/http"

// rateRequest is the body of POST /v1/rate: a charge spec plus the usage
// quantity to rate it against. The charge-spec fields are promoted from the
// embedded chargeSpec, so the body is one flat object — {model, currency,
// tiers, ..., quantity} — exactly as documented.
type rateRequest struct {
	chargeSpec
	Quantity int64 `json:"quantity"`
}

// rateResponse is the rated charge: the authoritative int64 total in minor
// units, its formatted display string, and the reconciling line items whose
// subtotals sum to the total exactly.
type rateResponse struct {
	Total          int64     `json:"total"`
	TotalFormatted string    `json:"totalFormatted"`
	Lines          []lineDTO `json:"lines"`
}

// handleRate is POST /v1/rate. It maps the body to a tariff.Charge, rates it,
// and returns the total and lines. Every failure — a malformed rate string, an
// empty tier schedule, a currency with no rounding mode, a negative quantity —
// surfaces as the JSON error contract with the mirrored sentinel code.
func (s *Server) handleRate(w http.ResponseWriter, r *http.Request) {
	var req rateRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.respondError(w, r, err)
		return
	}
	charge, err := req.toCharge()
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	res, err := charge.Rate(req.Quantity)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	cur := charge.Currency
	writeJSON(w, http.StatusOK, rateResponse{
		Total:          res.Total,
		TotalFormatted: cur.Format(res.Total),
		Lines:          toLineDTOs(cur, res.Lines),
	})
}
