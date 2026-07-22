package api

import (
	"net/http"
	"time"

	"github.com/zkrebbekx/tariff"
)

// periodDTO is a billing period on the wire: two RFC 3339 instants forming the
// half-open range [start, end). The period is anchored in start's location,
// which is the frame a day-based basis counts calendar days in.
type periodDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

func (p periodDTO) toPeriod() (tariff.Period, error) {
	start, err := parseTime("period.start", p.Start)
	if err != nil {
		return tariff.Period{}, err
	}
	end, err := parseTime("period.end", p.End)
	if err != nil {
		return tariff.Period{}, err
	}
	return tariff.Period{Start: start, End: end}, nil
}

// parseBasis maps the wire basis to tariff.Basis. An empty basis defaults to
// by-second, which is tariff's zero value and the Stripe default; an unknown
// value is rejected with the valid set.
func parseBasis(s string) (tariff.Basis, error) {
	switch s {
	case "", "second":
		return tariff.ProrateBySecond, nil
	case "day":
		return tariff.ProrateByDay, nil
	default:
		return 0, badRequest("invalid_argument",
			"basis must be \"second\" or \"day\", got \""+s+"\"")
	}
}

// prorationRequest is the body of POST /v1/proration: the old and new plan
// prices in minor units, the currency, the billing period, the instant of the
// change, and the basis. Maps to tariff.Change.
type prorationRequest struct {
	OldAmount int64       `json:"oldAmount"`
	NewAmount int64       `json:"newAmount"`
	Currency  currencyDTO `json:"currency"`
	Period    periodDTO   `json:"period"`
	At        string      `json:"at"`
	Basis     string      `json:"basis"`
}

// prorationResponse is the cross-vendor proration: a non-positive credit for
// the unused old price, a non-negative charge for the remaining time on the
// new price, and their net.
type prorationResponse struct {
	Credit          int64  `json:"credit"`
	CreditFormatted string `json:"creditFormatted"`
	Charge          int64  `json:"charge"`
	ChargeFormatted string `json:"chargeFormatted"`
	Net             int64  `json:"net"`
	NetFormatted    string `json:"netFormatted"`
}

// handleProration is POST /v1/proration.
func (s *Server) handleProration(w http.ResponseWriter, r *http.Request) {
	var req prorationRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.respondError(w, r, err)
		return
	}
	cur, err := req.Currency.toCurrency()
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	period, err := req.Period.toPeriod()
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	at, err := parseTime("at", req.At)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	basis, err := parseBasis(req.Basis)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	pr, err := tariff.Change(req.OldAmount, req.NewAmount, cur, period, at, basis)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, prorationResponse{
		Credit:          pr.Credit,
		CreditFormatted: cur.Format(pr.Credit),
		Charge:          pr.Charge,
		ChargeFormatted: cur.Format(pr.Charge),
		Net:             pr.Net,
		NetFormatted:    cur.Format(pr.Net),
	})
}

// fractionRequest is the body of POST /v1/proration/fraction: the period, the
// window [from, to), and the basis. Maps to Period.Fraction.
type fractionRequest struct {
	Period periodDTO `json:"period"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Basis  string    `json:"basis"`
}

// fractionResponse returns the exact fraction as a canonical "num/den" string
// (or a bare integer for a whole fraction) alongside an approximate decimal for
// display. The string is authoritative and exact; the decimal is lossy and for
// human eyes only.
type fractionResponse struct {
	Fraction        string  `json:"fraction"`
	FractionDecimal float64 `json:"fractionDecimal"`
}

// handleFraction is POST /v1/proration/fraction.
func (s *Server) handleFraction(w http.ResponseWriter, r *http.Request) {
	var req fractionRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.respondError(w, r, err)
		return
	}
	period, err := req.Period.toPeriod()
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	from, err := parseTime("from", req.From)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	to, err := parseTime("to", req.To)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	basis, err := parseBasis(req.Basis)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	frac, err := period.Fraction(from, to, basis)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	dec, _ := frac.Float64()
	writeJSON(w, http.StatusOK, fractionResponse{
		Fraction:        frac.RatString(),
		FractionDecimal: dec,
	})
}

// parseUnit maps the wire cycle unit to tariff.CycleUnit. An empty unit
// defaults to monthly, the zero value; an unknown value is rejected.
func parseUnit(s string) (tariff.CycleUnit, error) {
	switch s {
	case "", "monthly":
		return tariff.Monthly, nil
	case "yearly":
		return tariff.Yearly, nil
	default:
		return 0, badRequest("invalid_argument",
			"unit must be \"monthly\" or \"yearly\", got \""+s+"\"")
	}
}

// boundaryRequest is the body of POST /v1/boundary: the anchor of an
// anniversary cycle, the instant to step from, the cycle unit, and whether the
// cycle is calendar-aligned (anchored on the 1st) rather than anniversary. Maps
// to NextBoundary / NextCalendarBoundary.
type boundaryRequest struct {
	Anchor   string `json:"anchor"`
	From     string `json:"from"`
	Unit     string `json:"unit"`
	Calendar bool   `json:"calendar"`
}

// boundaryResponse is the next cycle boundary strictly after from, as RFC 3339.
type boundaryResponse struct {
	Next string `json:"next"`
}

// handleBoundary is POST /v1/boundary. It demonstrates the month-end trap over
// HTTP: a Jan 31 anchor steps to Feb 28 and back to Mar 31 without drift.
func (s *Server) handleBoundary(w http.ResponseWriter, r *http.Request) {
	var req boundaryRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.respondError(w, r, err)
		return
	}
	from, err := parseTime("from", req.From)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	unit, err := parseUnit(req.Unit)
	if err != nil {
		s.respondError(w, r, err)
		return
	}

	var next time.Time
	if req.Calendar {
		// Calendar-aligned cycles are anchored on the 1st; the anchor field is
		// unused and ignored if sent.
		next = tariff.NextCalendarBoundary(from, unit)
	} else {
		if req.Anchor == "" {
			s.respondError(w, r, badRequest("invalid_argument",
				"anchor is required for an anniversary cycle; set calendar:true for a calendar-aligned one"))
			return
		}
		anchor, err := parseTime("anchor", req.Anchor)
		if err != nil {
			s.respondError(w, r, err)
			return
		}
		next = tariff.NextBoundary(anchor, from, unit)
	}
	writeJSON(w, http.StatusOK, boundaryResponse{Next: next.Format(time.RFC3339)})
}
