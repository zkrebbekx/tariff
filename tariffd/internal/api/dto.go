package api

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/zkrebbekx/tariff"
)

// This file holds the wire types shared across the compute endpoints and the
// translation between JSON and tariff's exact-rational core.
//
// The exactness contract, stated once:
//
//   - Rates and percentages are math/big.Rat — exact rationals. JSON has no
//     rational type, and a rate of $0.0006 as a JSON *number* is a float64
//     that has already lost the value. So every rate crosses the wire as a
//     STRING and is parsed with big.Rat.SetString, which reads both decimal
//     ("0.0006", "6.5") and fraction ("6/10000", "13/2") forms exactly and
//     equally — "0.0006" and "6/10000" parse to the identical rate. A JSON
//     number in a rate field fails to decode into the string field, which is
//     the intended rejection: a rate is never a number here.
//
//   - Amounts are int64 counts of the currency's minor unit and marshal as
//     JSON numbers without loss. Each amount is also echoed as a formatted
//     string ("41.50") for display, using the currency's decimals — but the
//     int64 minor-unit value is always the authoritative one.

// ratString parses a decimal-or-fraction rate string into an exact *big.Rat.
// The field name rides into the error so a caller knows which rate was
// malformed. An empty string yields a nil rate with no error: some rate fields
// are optional (a graduated schedule has no top-level unitRate), and letting
// the nil reach the library surfaces the precise sentinel (ErrNoRate) rather
// than a guessed one here.
// maxRateLen bounds a rate string's length. A real price needs a handful of
// characters; the cap stops an explicit fraction with a giant numerator or
// denominator from forcing an astronomical big.Rat allocation before we can
// inspect it.
const maxRateLen = 64

// maxRateExp bounds the magnitude of a scientific-notation exponent, which
// big.Rat.SetString accepts. Without it a 9-byte string like "1e1000000"
// denotes 10^1000000 — a ~415 KB integer — so a sub-kilobyte request could
// exhaust memory. A price with more than a thousand decimal places does not
// exist; anything beyond that is an attack, not a rate.
const maxRateExp = 1000

func ratString(field, s string) (*big.Rat, error) {
	if s == "" {
		return nil, nil
	}
	bad := func() (*big.Rat, error) {
		return nil, badRequest("invalid_argument", fmt.Sprintf(
			"%s must be an exact rate as a string — a decimal like \"0.0006\" or a fraction like \"6/10000\" — got %q",
			field, s))
	}
	// Bound the input before SetString builds anything from it: a long string,
	// or a scientific exponent past maxRateExp, is rejected without allocating a
	// huge rational. Real rates are short; these caps never reject one.
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

// currencyDTO is the wire shape of a currency: an informational code, the
// number of minor-unit decimal places, and the rounding mode — which tariff
// requires to be chosen explicitly, because a hidden default is a compliance
// bug.
type currencyDTO struct {
	Code string `json:"code"`
	// Decimals is a pointer so an ABSENT field is distinguishable from an
	// explicit 0. A missing decimals is not silently treated as a zero-decimal
	// (JPY-scale) currency — that would return a silently wrong monetary total
	// on a 200 — it is rejected, exactly as an absent rounding mode is. A real
	// zero-decimal currency still sends decimals: 0 explicitly.
	Decimals *int   `json:"decimals"`
	Rounding string `json:"rounding"`
}

// toCurrency maps the wire currency to tariff.Currency. The rounding string is
// the one field with a fixed vocabulary; an unknown value is rejected here
// with the list of valid modes rather than left to surface as a less specific
// library error. An empty rounding is passed through as RoundingUnspecified so
// the library returns ErrBadCurrency — the same "you must choose a mode" the
// library enforces everywhere.
func (c currencyDTO) toCurrency() (tariff.Currency, error) {
	if c.Decimals == nil {
		return tariff.Currency{}, badRequest("invalid_argument",
			"currency.decimals is required (2 for cents, 0 for a zero-decimal currency like JPY, 3 for KWD); "+
				"omitting it would silently rate at zero decimals")
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
		return 0, badRequest("invalid_argument", fmt.Sprintf(
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
		return 0, badRequest("invalid_argument", fmt.Sprintf(
			"model must be one of per_unit, graduated, volume, package, stairstep; got %q", s))
	}
}

// tierDTO is one band of a tiered schedule. unitRate is a rate string (empty
// where the model does not use it); flatRate is minor units for the stairstep
// model.
type tierDTO struct {
	UpTo     int64  `json:"upTo"`
	Last     bool   `json:"last"`
	UnitRate string `json:"unitRate"`
	FlatRate int64  `json:"flatRate"`
}

// chargeSpec is the wire shape of a tariff.Charge. It carries every model's
// parameters; which fields matter depends on model, exactly as in the library.
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

// toCharge builds a tariff.Charge from the spec, parsing every rate string
// exactly. Structural validity (tier ordering, missing rates, bad currency) is
// left to the library so the response carries the library's own sentinel.
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

// lineDTO is one rated or adjustment line. Rate is the exact per-unit rate as
// a string in tariff's canonical rational form ("13/2", "7", "3/5000") so it
// round-trips through ratString without loss; it is omitted where the model
// has no per-unit rate (package, stairstep, flat fee, and adjustment lines).
// Label is present only on composed adjustment lines.
type lineDTO struct {
	Quantity          int64  `json:"quantity"`
	Rate              string `json:"rate,omitempty"`
	Subtotal          int64  `json:"subtotal"`
	SubtotalFormatted string `json:"subtotalFormatted"`
	Label             string `json:"label,omitempty"`
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
