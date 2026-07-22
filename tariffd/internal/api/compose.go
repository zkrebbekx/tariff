package api

import (
	"fmt"
	"math/big"
	"net/http"

	"github.com/zkrebbekx/tariff"
)

// composeStepDTO is one step of an invoice composition. The set is a tagged
// union keyed on "type"; each type reads only its own fields:
//
//	charge       → charge (a charge spec) + quantity
//	percent_off  → pct (a fraction string in [0,1], "1/10" or "0.1" for 10%) + label
//	amount_off   → minor (minor units off) + label
//	minimum      → minor (the floor in minor units) + label
//	credit       → balance (prepaid balance in minor units) + label
//	commitment   → balance (spend-commitment balance) + label
type composeStepDTO struct {
	Type     string      `json:"type"`
	Charge   *chargeSpec `json:"charge,omitempty"`
	Quantity int64       `json:"quantity"`
	Pct      string      `json:"pct"`
	Minor    int64       `json:"minor"`
	Balance  int64       `json:"balance"`
	Label    string      `json:"label"`
}

// composeRequest is the body of POST /v1/compose: the invoice currency and the
// steps to fold over it, IN THE GIVEN ORDER — which is the whole point, since a
// discount before or after a minimum yields different totals.
type composeRequest struct {
	Currency currencyDTO      `json:"currency"`
	Steps    []composeStepDTO `json:"steps"`
}

// composeStepEffectDTO echoes one step with the amount it moved the running
// total by (its effect). For credit and commitment steps it also carries the
// post-draw balance: the caller passed the balance by value in JSON, so the
// service returns what the balance became after the draw.
type composeStepEffectDTO struct {
	Type             string `json:"type"`
	Label            string `json:"label,omitempty"`
	Effect           int64  `json:"effect"`
	EffectFormatted  string `json:"effectFormatted"`
	Balance          *int64 `json:"balance,omitempty"`
	BalanceFormatted string `json:"balanceFormatted,omitempty"`
}

// composeResponse is the computed invoice: the gross subtotal from the charges,
// the net total after every step, the reconciling lines, and the per-step
// effects with post-draw balances.
type composeResponse struct {
	Subtotal          int64                  `json:"subtotal"`
	SubtotalFormatted string                 `json:"subtotalFormatted"`
	Total             int64                  `json:"total"`
	TotalFormatted    string                 `json:"totalFormatted"`
	Lines             []lineDTO              `json:"lines"`
	Steps             []composeStepEffectDTO `json:"steps"`
}

// parsedStep is a step after string parsing, ready to be turned into a
// tariff.Step. Keeping the parsed form lets the effect pass rebuild fresh
// tariff.Step values — and fresh balance pointers — for each prefix without
// re-parsing.
type parsedStep struct {
	kind    string
	charge  tariff.Charge
	qty     int64
	pct     *big.Rat
	minor   int64
	balance int64
	label   string
}

// parseSteps validates and parses each wire step. Rate/percentage strings are
// parsed here (a malformed one is invalid_argument); structural validity — a
// nil percentage, a negative floor, a mismatched charge currency — is left to
// the library so the response carries its exact sentinel.
func parseSteps(dtos []composeStepDTO) ([]parsedStep, error) {
	out := make([]parsedStep, 0, len(dtos))
	for i, d := range dtos {
		p := parsedStep{kind: d.Type, label: d.Label}
		switch d.Type {
		case "charge":
			if d.Charge == nil {
				return nil, badRequest("invalid_argument",
					fmt.Sprintf("steps[%d]: a charge step requires a \"charge\" object", i))
			}
			charge, err := d.Charge.toCharge()
			if err != nil {
				return nil, err
			}
			p.charge = charge
			p.qty = d.Quantity
		case "percent_off":
			pct, err := ratString(fmt.Sprintf("steps[%d].pct", i), d.Pct)
			if err != nil {
				return nil, err
			}
			p.pct = pct
		case "amount_off", "minimum":
			p.minor = d.Minor
		case "credit", "commitment":
			p.balance = d.Balance
		default:
			return nil, badRequest("invalid_argument", fmt.Sprintf(
				"steps[%d].type must be one of charge, percent_off, amount_off, minimum, credit, commitment; got %q",
				i, d.Type))
		}
		out = append(out, p)
	}
	return out, nil
}

// buildSteps turns parsed steps into tariff.Step values. It returns, per index,
// a balance pointer for credit and commitment steps (nil for the rest) so the
// caller can read the post-draw balance after Compose mutates it. Each call
// allocates fresh balances, so a prefix run never disturbs another's draws.
func buildSteps(parsed []parsedStep) ([]tariff.Step, []*int64) {
	steps := make([]tariff.Step, len(parsed))
	balances := make([]*int64, len(parsed))
	for i, p := range parsed {
		switch p.kind {
		case "charge":
			steps[i] = tariff.Charged(p.charge, p.qty)
		case "percent_off":
			steps[i] = tariff.PercentOff(p.pct, p.label)
		case "amount_off":
			steps[i] = tariff.AmountOff(p.minor, p.label)
		case "minimum":
			steps[i] = tariff.MinimumCharge(p.minor, p.label)
		case "credit":
			b := p.balance
			balances[i] = &b
			steps[i] = tariff.DrawCredit(&b, p.label)
		case "commitment":
			b := p.balance
			balances[i] = &b
			steps[i] = tariff.DrawCommitment(&b, p.label)
		}
	}
	return steps, balances
}

// stepEffects computes each step's effect on the running total by composing
// growing prefixes and differencing their totals: effect(k) = Total(steps up to
// k) − Total(steps up to k−1). Every step's validation is independent of the
// running total, so if the full composition succeeded every prefix does too.
// The service is stateless and step counts are small, so the O(n²) recompute is
// cheap and, unlike attributing invoice lines back to steps, unambiguous when
// two steps share a label.
func stepEffects(cur tariff.Currency, parsed []parsedStep) ([]int64, error) {
	effects := make([]int64, len(parsed))
	var prev int64
	for k := 1; k <= len(parsed); k++ {
		steps, _ := buildSteps(parsed[:k])
		inv, err := tariff.Compose(cur, steps...)
		if err != nil {
			return nil, err
		}
		effects[k-1] = inv.Total - prev
		prev = inv.Total
	}
	return effects, nil
}

// handleCompose is POST /v1/compose.
func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	var req composeRequest
	if err := decodeBody(w, r, &req); err != nil {
		s.respondError(w, r, err)
		return
	}
	cur, err := req.Currency.toCurrency()
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	parsed, err := parseSteps(req.Steps)
	if err != nil {
		s.respondError(w, r, err)
		return
	}

	steps, balances := buildSteps(parsed)
	inv, err := tariff.Compose(cur, steps...)
	if err != nil {
		s.respondError(w, r, err)
		return
	}

	effects, err := stepEffects(cur, parsed)
	if err != nil {
		s.respondError(w, r, err)
		return
	}

	resp := composeResponse{
		Subtotal:          inv.Subtotal,
		SubtotalFormatted: cur.Format(inv.Subtotal),
		Total:             inv.Total,
		TotalFormatted:    cur.Format(inv.Total),
		Lines:             toLineDTOs(cur, inv.Lines),
		Steps:             make([]composeStepEffectDTO, 0, len(parsed)),
	}
	for i, p := range parsed {
		sd := composeStepEffectDTO{
			Type:            p.kind,
			Label:           p.label,
			Effect:          effects[i],
			EffectFormatted: cur.Format(effects[i]),
		}
		if balances[i] != nil {
			bal := *balances[i]
			sd.Balance = &bal
			sd.BalanceFormatted = cur.Format(bal)
		}
		resp.Steps = append(resp.Steps, sd)
	}
	writeJSON(w, http.StatusOK, resp)
}
