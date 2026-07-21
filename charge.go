package tariff

import (
	"fmt"
	"math/big"
	"strconv"
)

// Model selects a charge's rating algebra.
type Model uint8

const (
	// PerUnit charges quantity * UnitRate, a single flat rate for every unit.
	PerUnit Model = iota
	// Graduated charges each tier's units at that tier's rate and sums the
	// per-tier subtotals — the marginal, cumulative interpretation of a tiered
	// schedule.
	Graduated
	// Volume charges the whole quantity at the single rate of the one tier the
	// total lands in. The total can fall as usage grows into a cheaper tier.
	Volume
	// Package rounds the chargeable quantity up to whole blocks of PackageSize
	// (after any free allowance) and charges PackagePrice per block.
	Package
	// Stairstep charges a flat fee for landing in a tier, regardless of the
	// exact quantity within it.
	Stairstep
)

// String renders the model name.
func (m Model) String() string {
	switch m {
	case PerUnit:
		return "per-unit"
	case Graduated:
		return "graduated"
	case Volume:
		return "volume"
	case Package:
		return "package"
	case Stairstep:
		return "stairstep"
	default:
		return "Model(" + strconv.Itoa(int(m)) + ")"
	}
}

// Line is one component of a rated charge: the units (or blocks) it covers, the
// exact per-unit rate applied where one applies, and the subtotal in minor
// units. Across a [Result] the line subtotals always sum exactly to the total.
type Line struct {
	// Quantity is the number of units this line rates, or the number of blocks
	// for a package charge. It is zero on a bare flat-fee line.
	Quantity int64
	// Rate is the exact per-unit rate applied, or nil where the model has no
	// per-unit rate (package, stairstep, flat fee). It is a copy the caller may
	// keep or mutate freely.
	Rate *big.Rat
	// Subtotal is this line's amount in minor units.
	Subtotal int64
}

// Result is the outcome of rating a charge: the rounded total in minor units,
// and the line items that reconcile to it exactly.
type Result struct {
	// Total is the amount owed, in the currency's minor units.
	Total int64
	// Lines are the itemized components; their subtotals sum to Total exactly.
	Lines []Line
}

// Charge is a price-plan component: one rating model, a currency, and the
// parameters that model reads. Rate it against a usage quantity with
// [Charge.Rate].
//
// The rate fields are exact rationals (*big.Rat) so that sub-cent prices such
// as $0.0006 never drift; quantity * rate is evaluated entirely in math/big and
// rounded to the minor unit exactly once, at the line boundary, using the
// currency's explicit rounding mode.
type Charge struct {
	// Model selects the rating algebra.
	Model Model
	// Currency fixes the minor-unit scale and rounding mode.
	Currency Currency
	// UnitRate is the exact per-unit price for the tier-less PerUnit model.
	UnitRate *big.Rat
	// Tiers is the price schedule for the Graduated, Volume and Stairstep
	// models.
	Tiers []Tier
	// PackageSize is the block size for the Package model; it must be positive.
	PackageSize int64
	// PackagePrice is the price per block, in minor units, for the Package
	// model.
	PackagePrice int64
	// FreeAllowance is a quantity subtracted before rating (or before the
	// package ceil). It composes with every model.
	FreeAllowance int64
	// FlatFee is an optional fixed amount, in minor units, added to the rated
	// total as its own line — the fixed half of a fixed-plus-usage charge. It
	// applies regardless of quantity, including when usage rates to nothing.
	FlatFee int64
}

// Rate computes the charge for a usage quantity, returning the rounded total in
// minor units and the reconciling line items.
//
// A zero quantity rates the usage to nothing (any FlatFee still applies). A
// negative quantity is an error. The charge's configuration is validated first,
// so a malformed schedule is reported even at zero quantity.
func (c Charge) Rate(quantity int64) (Result, error) {
	if err := c.Currency.validate(); err != nil {
		return Result{}, err
	}
	if quantity < 0 {
		return Result{}, fmt.Errorf("%w: %d", ErrNegativeQuantity, quantity)
	}
	if c.FreeAllowance < 0 {
		return Result{}, fmt.Errorf("%w: %d", ErrBadAllowance, c.FreeAllowance)
	}
	if c.FlatFee < 0 {
		return Result{}, fmt.Errorf("%w: flat fee %d is negative", ErrNoRate, c.FlatFee)
	}

	var (
		res Result
		err error
	)
	switch c.Model {
	case PerUnit:
		res, err = c.ratePerUnit(quantity)
	case Graduated:
		res, err = c.rateGraduated(quantity)
	case Volume:
		res, err = c.rateVolume(quantity)
	case Package:
		res, err = c.ratePackage(quantity)
	case Stairstep:
		res, err = c.rateStairstep(quantity)
	default:
		return Result{}, fmt.Errorf("%w: %d", ErrUnknownModel, c.Model)
	}
	if err != nil {
		return Result{}, err
	}

	if c.FlatFee != 0 {
		total, err := addInt64(res.Total, c.FlatFee)
		if err != nil {
			return Result{}, err
		}
		res.Lines = append(res.Lines, Line{Subtotal: c.FlatFee})
		res.Total = total
	}
	return res, nil
}

// chargeable applies the free allowance, clamping at zero.
func (c Charge) chargeable(quantity int64) int64 {
	q := quantity - c.FreeAllowance
	if q < 0 {
		return 0
	}
	return q
}

func (c Charge) ratePerUnit(quantity int64) (Result, error) {
	if c.UnitRate == nil {
		return Result{}, fmt.Errorf("%w: per-unit charge has no rate", ErrNoRate)
	}
	if c.UnitRate.Sign() < 0 {
		return Result{}, fmt.Errorf("%w: per-unit rate is negative", ErrNoRate)
	}
	q := c.chargeable(quantity)
	if q == 0 {
		return Result{}, nil
	}
	total, err := c.Currency.round(c.scaledAmount(q, c.UnitRate))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Total: total,
		Lines: []Line{{Quantity: q, Rate: cloneRat(c.UnitRate), Subtotal: total}},
	}, nil
}

func (c Charge) rateGraduated(quantity int64) (Result, error) {
	if err := validateTiers(c.Tiers, true, false); err != nil {
		return Result{}, err
	}
	q := c.chargeable(quantity)
	if q == 0 {
		return Result{}, nil
	}

	var (
		lower      int64
		exacts     []*big.Rat
		lines      []Line
		totalExact = new(big.Rat)
	)
	for _, t := range c.Tiers {
		upper := t.UpTo
		if t.Last || upper > q {
			upper = q
		}
		if upper > lower {
			units := upper - lower
			e := c.scaledAmount(units, t.UnitRate)
			totalExact.Add(totalExact, e)
			exacts = append(exacts, e)
			lines = append(lines, Line{Quantity: units, Rate: cloneRat(t.UnitRate)})
		}
		if t.Last || q <= t.UpTo {
			break
		}
		lower = t.UpTo
	}

	// Rate every tier exactly, sum exactly, round the total once, then allocate
	// the rounded total back across the tiers so the line items reconcile to it
	// with no drift.
	total, err := c.Currency.round(totalExact)
	if err != nil {
		return Result{}, err
	}
	shares, err := allocateRat(total, exacts)
	if err != nil {
		return Result{}, err
	}
	for i := range lines {
		lines[i].Subtotal = shares[i]
	}
	return Result{Total: total, Lines: lines}, nil
}

func (c Charge) rateVolume(quantity int64) (Result, error) {
	if err := validateTiers(c.Tiers, true, false); err != nil {
		return Result{}, err
	}
	q := c.chargeable(quantity)
	if q == 0 {
		return Result{}, nil
	}
	t := landingTier(c.Tiers, q)
	total, err := c.Currency.round(c.scaledAmount(q, t.UnitRate))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Total: total,
		Lines: []Line{{Quantity: q, Rate: cloneRat(t.UnitRate), Subtotal: total}},
	}, nil
}

func (c Charge) ratePackage(quantity int64) (Result, error) {
	if c.PackageSize <= 0 {
		return Result{}, fmt.Errorf("%w: package size %d must be positive", ErrBadPackage, c.PackageSize)
	}
	if c.PackagePrice < 0 {
		return Result{}, fmt.Errorf("%w: package price %d is negative", ErrBadPackage, c.PackagePrice)
	}
	q := c.chargeable(quantity)
	if q == 0 {
		return Result{}, nil
	}
	// Free allowance is subtracted before the ceil, so a part-block above the
	// allowance still rounds up to a whole block.
	blocks := q / c.PackageSize
	if q%c.PackageSize != 0 {
		blocks++
	}
	subtotal, err := mulInt64(blocks, c.PackagePrice)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Total: subtotal,
		Lines: []Line{{Quantity: blocks, Subtotal: subtotal}},
	}, nil
}

func (c Charge) rateStairstep(quantity int64) (Result, error) {
	if err := validateTiers(c.Tiers, false, true); err != nil {
		return Result{}, err
	}
	q := c.chargeable(quantity)
	if q == 0 {
		return Result{}, nil
	}
	t := landingTier(c.Tiers, q)
	return Result{
		Total: t.FlatRate,
		Lines: []Line{{Quantity: q, Subtotal: t.FlatRate}},
	}, nil
}

// scaledAmount computes units * rate * 10^Decimals as an exact rational — the
// amount in minor units, before rounding.
func (c Charge) scaledAmount(units int64, rate *big.Rat) *big.Rat {
	out := new(big.Rat).SetInt64(units)
	out.Mul(out, rate)
	out.Mul(out, c.Currency.scaleRat())
	return out
}

func cloneRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}

func mulInt64(a, b int64) (int64, error) {
	z := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if !z.IsInt64() {
		return 0, fmt.Errorf("%w: %s", ErrOverflow, z.String())
	}
	return z.Int64(), nil
}

// addInt64 adds two amounts, reporting ErrOverflow rather than wrapping to a
// wrong-sign total. It is the guard on the one arithmetic step the rate path
// would otherwise do unchecked: folding a flat fee into an already-large rated
// total.
func addInt64(a, b int64) (int64, error) {
	z := new(big.Int).Add(big.NewInt(a), big.NewInt(b))
	if !z.IsInt64() {
		return 0, fmt.Errorf("%w: %d + %d", ErrOverflow, a, b)
	}
	return z.Int64(), nil
}
