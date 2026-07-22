//go:build js && wasm

// Command tariff-wasm is the browser playground's engine: the tariff rating
// core compiled to WebAssembly, exposing a small JSON API on the global
// "tariff" object. It has no backend — every call runs in the visitor's tab
// over the pure library, and nothing is stored.
//
// The exactness discipline the library enforces is carried across the JS
// boundary faithfully: rates and percentages arrive as STRINGS and are parsed
// with big.Rat.SetString ("0.0006" and "6/10000" parse to the identical exact
// rate), because a JSON number is a float64 that has already lost a sub-cent
// price. Amounts cross as int64 minor units — lossless — echoed with a
// formatted display string, and the integer is always authoritative.
//
// This file carries the js && wasm build constraint so the native toolchain —
// go build ./..., go vet ./..., go test ./..., golangci-lint — never tries to
// compile it (syscall/js exists only under GOOS=js). The root module stays
// zero-dependency: syscall/js is stdlib and adds nothing to go.sum, and this
// command imports only the tariff library beside it.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	"github.com/zkrebbekx/tariff"
)

func main() {
	obj := map[string]any{}
	register(obj, "rate", handleRate)
	register(obj, "proration", handleProration)
	register(obj, "compose", handleCompose)
	register(obj, "boundary", handleBoundary)
	register(obj, "fraction", handleFraction)
	register(obj, "loadScenario", handleLoadScenario)
	obj["scenarios"] = toJS(scenarioList())

	js.Global().Set("tariff", js.ValueOf(obj))
	js.Global().Set("__tariffReady", js.ValueOf(true))
	if cb := js.Global().Get("__tariffOnReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}

	// Keep the Go runtime alive for the lifetime of the page.
	select {}
}

// register wraps a handler so every call recovers from a panic into a returned
// {error} object rather than tearing down the wasm instance, and marshals the
// result to a real JS object the page can read without parsing.
func register(obj map[string]any, name string, fn func([]byte) (any, error)) {
	obj[name] = js.FuncOf(func(_ js.Value, args []js.Value) (result any) {
		defer func() {
			if r := recover(); r != nil {
				result = toJS(map[string]any{"error": fmt.Sprintf("panic: %v", r), "code": "panic"})
			}
		}()
		out, err := fn(input(args))
		if err != nil {
			return toJS(errObj(err))
		}
		return toJS(out)
	})
}

// input normalises the first argument to JSON bytes. A JS object is
// stringified; a bare string is passed through verbatim (loadScenario accepts
// one); anything absent becomes an empty object.
func input(args []js.Value) []byte {
	if len(args) == 0 {
		return []byte("{}")
	}
	a := args[0]
	switch a.Type() {
	case js.TypeString:
		return []byte(a.String())
	case js.TypeUndefined, js.TypeNull:
		return []byte("{}")
	default:
		return []byte(js.Global().Get("JSON").Call("stringify", a).String())
	}
}

// toJS marshals a Go value and parses it back into a native JS object so the
// page receives structured data rather than a string to parse itself.
func toJS(v any) js.Value {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"error": err.Error(), "code": "marshal"})
	}
	return js.Global().Get("JSON").Call("parse", string(b))
}

// --- error contract ---------------------------------------------------------

// errObj renders an error as the page's {error, code} shape.
func errObj(err error) map[string]any {
	return map[string]any{"error": err.Error(), "code": errCode(err)}
}

// argErr is a bridge-minted validation error (a malformed rate string, an
// unknown enum) distinct from a library sentinel. It carries a stable code.
type argErr struct {
	code string
	msg  string
}

func (e *argErr) Error() string { return e.msg }

func badArg(code, msg string) error { return &argErr{code: code, msg: msg} }

// errCode maps an error to a stable string code: a bridge argErr keeps its own
// code, and every tariff sentinel is mirrored onto its name (the same taxonomy
// tariffd uses over HTTP). An unrecognised error is "error".
func errCode(err error) string {
	var a *argErr
	if errors.As(err, &a) {
		return a.code
	}
	switch {
	case errors.Is(err, tariff.ErrBadDiscount):
		return "bad_discount"
	case errors.Is(err, tariff.ErrNegativeAmount):
		return "negative_amount"
	case errors.Is(err, tariff.ErrNegativeQuantity):
		return "negative_quantity"
	case errors.Is(err, tariff.ErrBadPeriod):
		return "bad_period"
	case errors.Is(err, tariff.ErrBadWindow):
		return "bad_window"
	case errors.Is(err, tariff.ErrBadBasis):
		return "bad_basis"
	case errors.Is(err, tariff.ErrBadFloor):
		return "bad_floor"
	case errors.Is(err, tariff.ErrBadBalance):
		return "bad_balance"
	case errors.Is(err, tariff.ErrCurrencyMismatch):
		return "currency_mismatch"
	case errors.Is(err, tariff.ErrNilStep):
		return "nil_step"
	case errors.Is(err, tariff.ErrBadCurrency):
		return "bad_currency"
	case errors.Is(err, tariff.ErrBadAllowance):
		return "bad_allowance"
	case errors.Is(err, tariff.ErrNoRate):
		return "no_rate"
	case errors.Is(err, tariff.ErrEmptyTiers):
		return "empty_tiers"
	case errors.Is(err, tariff.ErrTierOrder):
		return "tier_order"
	case errors.Is(err, tariff.ErrUnknownModel):
		return "unknown_model"
	case errors.Is(err, tariff.ErrBadPackage):
		return "bad_package"
	case errors.Is(err, tariff.ErrOverflow):
		return "overflow"
	case errors.Is(err, tariff.ErrBadAllocation):
		return "bad_allocation"
	default:
		return "error"
	}
}

// --- exact-rate parsing (mirrors tariffd's guard) ---------------------------

// maxRateLen bounds a rate string's length. A real price needs a handful of
// characters; the cap stops an explicit fraction with a giant numerator or
// denominator from forcing an astronomical big.Rat allocation in the tab.
const maxRateLen = 64

// maxRateExp bounds a scientific-notation exponent, which big.Rat.SetString
// accepts. Without it a 9-byte "1e1000000" denotes a ~415 KB integer, so a
// tiny input could exhaust the tab's memory. A price with more than a thousand
// decimal places does not exist; anything beyond that is abuse, not a rate.
const maxRateExp = 1000

// ratString parses a decimal-or-fraction rate string into an exact *big.Rat.
// An empty string yields a nil rate with no error: some rate fields are
// optional, and letting the nil reach the library surfaces the precise
// sentinel (ErrNoRate) rather than a guessed one here.
func ratString(field, s string) (*big.Rat, error) {
	if s == "" {
		return nil, nil
	}
	bad := func() (*big.Rat, error) {
		return nil, badArg("invalid_argument", fmt.Sprintf(
			"%s must be an exact rate as a string — a decimal like \"0.0006\" or a fraction like \"6/10000\" — got %q",
			field, s))
	}
	if len(s) > maxRateLen {
		return bad()
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exp := s[i+1:]
		exp = strings.TrimPrefix(strings.TrimPrefix(exp, "+"), "-")
		if exp == "" || len(exp) > 4 {
			return bad()
		}
		n, err := strconv.Atoi(exp)
		if err != nil || n > maxRateExp {
			return bad()
		}
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return bad()
	}
	return r, nil
}

// --- currency / model / tiers -----------------------------------------------

type currencyDTO struct {
	Code     string `json:"code"`
	Decimals *int   `json:"decimals"`
	Rounding string `json:"rounding"`
}

func (c currencyDTO) toCurrency() (tariff.Currency, error) {
	if c.Decimals == nil {
		return tariff.Currency{}, badArg("invalid_argument",
			"currency.decimals is required (2 for cents, 0 for JPY, 3 for KWD)")
	}
	rounding, err := parseRounding(c.Rounding)
	if err != nil {
		return tariff.Currency{}, err
	}
	return tariff.Currency{Code: c.Code, Decimals: *c.Decimals, Rounding: rounding}, nil
}

func parseRounding(s string) (tariff.RoundingMode, error) {
	switch s {
	case "":
		return tariff.RoundingUnspecified, nil
	case "half_up":
		return tariff.RoundHalfUp, nil
	case "half_even":
		return tariff.RoundHalfEven, nil
	case "floor":
		return tariff.RoundFloor, nil
	case "ceil":
		return tariff.RoundCeil, nil
	default:
		return 0, badArg("invalid_argument", fmt.Sprintf(
			"currency.rounding must be one of half_up, half_even, floor, ceil; got %q", s))
	}
}

func parseModel(s string) (tariff.Model, error) {
	switch s {
	case "per_unit":
		return tariff.PerUnit, nil
	case "graduated":
		return tariff.Graduated, nil
	case "volume":
		return tariff.Volume, nil
	case "package":
		return tariff.Package, nil
	case "stairstep":
		return tariff.Stairstep, nil
	default:
		return 0, badArg("invalid_argument", fmt.Sprintf(
			"model must be one of per_unit, graduated, volume, package, stairstep; got %q", s))
	}
}

type tierDTO struct {
	UpTo     int64  `json:"upTo"`
	Last     bool   `json:"last"`
	UnitRate string `json:"unitRate"`
	FlatRate int64  `json:"flatRate"`
}

type chargeSpec struct {
	Model         string      `json:"model"`
	Currency      currencyDTO `json:"currency"`
	Tiers         []tierDTO   `json:"tiers"`
	UnitRate      string      `json:"unitRate"`
	PackageSize   int64       `json:"packageSize"`
	PackagePrice  int64       `json:"packagePrice"`
	FreeAllowance int64       `json:"freeAllowance"`
	FlatFee       int64       `json:"flatFee"`
}

func (spec chargeSpec) toCharge() (tariff.Charge, error) {
	model, err := parseModel(spec.Model)
	if err != nil {
		return tariff.Charge{}, err
	}
	cur, err := spec.Currency.toCurrency()
	if err != nil {
		return tariff.Charge{}, err
	}
	unitRate, err := ratString("unitRate", spec.UnitRate)
	if err != nil {
		return tariff.Charge{}, err
	}
	tiers := make([]tariff.Tier, 0, len(spec.Tiers))
	for i, t := range spec.Tiers {
		rate, err := ratString(fmt.Sprintf("tiers[%d].unitRate", i), t.UnitRate)
		if err != nil {
			return tariff.Charge{}, err
		}
		tiers = append(tiers, tariff.Tier{
			UpTo:     t.UpTo,
			Last:     t.Last,
			UnitRate: rate,
			FlatRate: t.FlatRate,
		})
	}
	return tariff.Charge{
		Model:         model,
		Currency:      cur,
		UnitRate:      unitRate,
		Tiers:         tiers,
		PackageSize:   spec.PackageSize,
		PackagePrice:  spec.PackagePrice,
		FreeAllowance: spec.FreeAllowance,
		FlatFee:       spec.FlatFee,
	}, nil
}

// lineDTO is one rated or adjustment line. Rate is the exact per-unit rate as a
// canonical rational string ("13/2"), omitted where the model has no per-unit
// rate; rateDecimal is a lossy float for display convenience.
type lineDTO struct {
	Quantity          int64   `json:"quantity"`
	Rate              string  `json:"rate,omitempty"`
	RateDecimal       float64 `json:"rateDecimal,omitempty"`
	Subtotal          int64   `json:"subtotal"`
	SubtotalFormatted string  `json:"subtotalFormatted"`
	Label             string  `json:"label,omitempty"`
}

func toLineDTO(cur tariff.Currency, l tariff.Line) lineDTO {
	dto := lineDTO{
		Quantity:          l.Quantity,
		Subtotal:          l.Subtotal,
		SubtotalFormatted: cur.Format(l.Subtotal),
		Label:             l.Label,
	}
	if l.Rate != nil {
		dto.Rate = l.Rate.RatString()
		dto.RateDecimal, _ = l.Rate.Float64()
	}
	return dto
}

func toLineDTOs(cur tariff.Currency, lines []tariff.Line) []lineDTO {
	out := make([]lineDTO, 0, len(lines))
	for _, l := range lines {
		out = append(out, toLineDTO(cur, l))
	}
	return out
}

// --- rate -------------------------------------------------------------------

type rateRequest struct {
	chargeSpec
	Quantity int64 `json:"quantity"`
}

type rateResponse struct {
	Total          int64     `json:"total"`
	TotalFormatted string    `json:"totalFormatted"`
	Lines          []lineDTO `json:"lines"`
}

func handleRate(in []byte) (any, error) {
	var req rateRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	charge, err := req.toCharge()
	if err != nil {
		return nil, err
	}
	res, err := charge.Rate(req.Quantity)
	if err != nil {
		return nil, err
	}
	cur := charge.Currency
	return rateResponse{
		Total:          res.Total,
		TotalFormatted: cur.Format(res.Total),
		Lines:          toLineDTOs(cur, res.Lines),
	}, nil
}

// --- proration --------------------------------------------------------------

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

func parseBasis(s string) (tariff.Basis, error) {
	switch s {
	case "", "second":
		return tariff.ProrateBySecond, nil
	case "day":
		return tariff.ProrateByDay, nil
	default:
		return 0, badArg("invalid_argument", fmt.Sprintf("basis must be \"second\" or \"day\", got %q", s))
	}
}

func parseTime(field, value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, badArg("invalid_argument", fmt.Sprintf(
		"%s must be an RFC 3339 timestamp such as 2026-01-01T00:00:00Z, got %q", field, value))
}

type prorationRequest struct {
	OldAmount int64       `json:"oldAmount"`
	NewAmount int64       `json:"newAmount"`
	Currency  currencyDTO `json:"currency"`
	Period    periodDTO   `json:"period"`
	At        string      `json:"at"`
	Basis     string      `json:"basis"`
}

type prorationResponse struct {
	Credit          int64  `json:"credit"`
	CreditFormatted string `json:"creditFormatted"`
	Charge          int64  `json:"charge"`
	ChargeFormatted string `json:"chargeFormatted"`
	Net             int64  `json:"net"`
	NetFormatted    string `json:"netFormatted"`
	// Fraction is the exact remaining fraction of the period [at, End), the
	// scalar both prorated amounts are computed from — echoed as a canonical
	// "num/den" string (authoritative, exact) plus a lossy decimal for display.
	Fraction        string  `json:"fraction"`
	FractionDecimal float64 `json:"fractionDecimal"`
}

func handleProration(in []byte) (any, error) {
	var req prorationRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	cur, err := req.Currency.toCurrency()
	if err != nil {
		return nil, err
	}
	period, err := req.Period.toPeriod()
	if err != nil {
		return nil, err
	}
	at, err := parseTime("at", req.At)
	if err != nil {
		return nil, err
	}
	basis, err := parseBasis(req.Basis)
	if err != nil {
		return nil, err
	}
	pr, err := tariff.Change(req.OldAmount, req.NewAmount, cur, period, at, basis)
	if err != nil {
		return nil, err
	}
	// The remaining fraction is the scalar the credit and charge are scaled by;
	// surface it so the page can show exactly what drove the proration.
	fracStr, fracDec := "", 0.0
	if frac, ferr := period.Fraction(at, period.End, basis); ferr == nil {
		fracStr = frac.RatString()
		fracDec, _ = frac.Float64()
	}
	return prorationResponse{
		Credit:          pr.Credit,
		CreditFormatted: cur.Format(pr.Credit),
		Charge:          pr.Charge,
		ChargeFormatted: cur.Format(pr.Charge),
		Net:             pr.Net,
		NetFormatted:    cur.Format(pr.Net),
		Fraction:        fracStr,
		FractionDecimal: fracDec,
	}, nil
}

// --- fraction (period demonstrations) ---------------------------------------

type fractionRequest struct {
	Period periodDTO `json:"period"`
	From   string    `json:"from"`
	To     string    `json:"to"`
	Basis  string    `json:"basis"`
}

type fractionResponse struct {
	Fraction        string  `json:"fraction"`
	FractionDecimal float64 `json:"fractionDecimal"`
}

func handleFraction(in []byte) (any, error) {
	var req fractionRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	period, err := req.Period.toPeriod()
	if err != nil {
		return nil, err
	}
	from, err := parseTime("from", req.From)
	if err != nil {
		return nil, err
	}
	to, err := parseTime("to", req.To)
	if err != nil {
		return nil, err
	}
	basis, err := parseBasis(req.Basis)
	if err != nil {
		return nil, err
	}
	frac, err := period.Fraction(from, to, basis)
	if err != nil {
		return nil, err
	}
	dec, _ := frac.Float64()
	return fractionResponse{Fraction: frac.RatString(), FractionDecimal: dec}, nil
}

// --- boundary ---------------------------------------------------------------

func parseUnit(s string) (tariff.CycleUnit, error) {
	switch s {
	case "", "monthly":
		return tariff.Monthly, nil
	case "yearly":
		return tariff.Yearly, nil
	default:
		return 0, badArg("invalid_argument", fmt.Sprintf("unit must be \"monthly\" or \"yearly\", got %q", s))
	}
}

type boundaryRequest struct {
	Anchor   string `json:"anchor"`
	From     string `json:"from"`
	Unit     string `json:"unit"`
	Calendar bool   `json:"calendar"`
}

type boundaryResponse struct {
	Next string `json:"next"`
}

func handleBoundary(in []byte) (any, error) {
	var req boundaryRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	from, err := parseTime("from", req.From)
	if err != nil {
		return nil, err
	}
	unit, err := parseUnit(req.Unit)
	if err != nil {
		return nil, err
	}
	var next time.Time
	if req.Calendar {
		next = tariff.NextCalendarBoundary(from, unit)
	} else {
		if req.Anchor == "" {
			return nil, badArg("invalid_argument",
				"anchor is required for an anniversary cycle; set calendar:true for a calendar-aligned one")
		}
		anchor, err := parseTime("anchor", req.Anchor)
		if err != nil {
			return nil, err
		}
		next = tariff.NextBoundary(anchor, from, unit)
	}
	return boundaryResponse{Next: next.Format(time.RFC3339)}, nil
}

// --- compose ----------------------------------------------------------------

// composeStepDTO is one step of a composition, a tagged union keyed on "type":
//
//	charge      → charge (a charge spec) + quantity
//	percent_off → pct (a fraction string in [0,1], "1/10" or "0.1" for 10%) + label
//	amount_off  → minor (minor units off) + label
//	minimum     → minor (the floor in minor units) + label
//	credit      → balance (prepaid balance in minor units) + label
//	commitment  → balance (spend-commitment balance) + label
type composeStepDTO struct {
	Type     string      `json:"type"`
	Charge   *chargeSpec `json:"charge,omitempty"`
	Quantity int64       `json:"quantity"`
	Pct      string      `json:"pct"`
	Minor    int64       `json:"minor"`
	Balance  int64       `json:"balance"`
	Label    string      `json:"label"`
}

type composeRequest struct {
	Currency currencyDTO      `json:"currency"`
	Steps    []composeStepDTO `json:"steps"`
}

// parsedStep is a step after string parsing, ready to become a tariff.Step.
// Keeping the parsed form lets the effect pass rebuild fresh steps (and fresh
// balance pointers) for each prefix without re-parsing.
type parsedStep struct {
	kind    string
	charge  tariff.Charge
	qty     int64
	pct     *big.Rat
	minor   int64
	balance int64
	label   string
}

// maxComposeSteps bounds the number of steps in one composition. The effect
// pass is O(n²); this keeps a hostile input from turning that into seconds of
// CPU in the tab. Far more than any real invoice needs.
const maxComposeSteps = 512

func parseSteps(dtos []composeStepDTO) ([]parsedStep, error) {
	out := make([]parsedStep, 0, len(dtos))
	for i, d := range dtos {
		p := parsedStep{kind: d.Type, label: d.Label}
		switch d.Type {
		case "charge":
			if d.Charge == nil {
				return nil, badArg("invalid_argument", fmt.Sprintf("steps[%d]: a charge step requires a \"charge\" object", i))
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
			return nil, badArg("invalid_argument", fmt.Sprintf(
				"steps[%d].type must be one of charge, percent_off, amount_off, minimum, credit, commitment; got %q",
				i, d.Type))
		}
		out = append(out, p)
	}
	return out, nil
}

// buildSteps turns parsed steps into tariff.Step values, returning per index a
// balance pointer for credit and commitment steps (nil for the rest) so the
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
// growing prefixes and differencing their totals: effect(k) = Total(prefix k) −
// Total(prefix k−1). Every step's validation is independent of the running
// total, so if the whole composition succeeded every prefix does too.
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

type composeStepEffectDTO struct {
	Type             string `json:"type"`
	Label            string `json:"label,omitempty"`
	Effect           int64  `json:"effect"`
	EffectFormatted  string `json:"effectFormatted"`
	Running          int64  `json:"running"`
	RunningFormatted string `json:"runningFormatted"`
	Balance          *int64 `json:"balance,omitempty"`
	BalanceFormatted string `json:"balanceFormatted,omitempty"`
}

type composeResponse struct {
	Subtotal          int64                  `json:"subtotal"`
	SubtotalFormatted string                 `json:"subtotalFormatted"`
	Total             int64                  `json:"total"`
	TotalFormatted    string                 `json:"totalFormatted"`
	Lines             []lineDTO              `json:"lines"`
	Steps             []composeStepEffectDTO `json:"steps"`
}

func handleCompose(in []byte) (any, error) {
	var req composeRequest
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	if len(req.Steps) > maxComposeSteps {
		return nil, badArg("invalid_argument", fmt.Sprintf(
			"a composition may have at most %d steps; got %d", maxComposeSteps, len(req.Steps)))
	}
	cur, err := req.Currency.toCurrency()
	if err != nil {
		return nil, err
	}
	parsed, err := parseSteps(req.Steps)
	if err != nil {
		return nil, err
	}
	steps, balances := buildSteps(parsed)
	inv, err := tariff.Compose(cur, steps...)
	if err != nil {
		return nil, err
	}
	effects, err := stepEffects(cur, parsed)
	if err != nil {
		return nil, err
	}
	resp := composeResponse{
		Subtotal:          inv.Subtotal,
		SubtotalFormatted: cur.Format(inv.Subtotal),
		Total:             inv.Total,
		TotalFormatted:    cur.Format(inv.Total),
		Lines:             toLineDTOs(cur, inv.Lines),
		Steps:             make([]composeStepEffectDTO, 0, len(parsed)),
	}
	var running int64
	for i, p := range parsed {
		running += effects[i]
		sd := composeStepEffectDTO{
			Type:             p.kind,
			Label:            p.label,
			Effect:           effects[i],
			EffectFormatted:  cur.Format(effects[i]),
			Running:          running,
			RunningFormatted: cur.Format(running),
		}
		if balances[i] != nil {
			bal := *balances[i]
			sd.Balance = &bal
			sd.BalanceFormatted = cur.Format(bal)
		}
		resp.Steps = append(resp.Steps, sd)
	}
	return resp, nil
}
