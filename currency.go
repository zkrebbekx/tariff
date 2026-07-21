package tariff

import (
	"fmt"
	"math/big"
	"strconv"
)

// maxDecimals bounds a currency's minor-unit scale. No real currency exceeds
// three decimal places; the ceiling keeps 10^Decimals comfortably inside an
// int64 and rejects nonsense configurations early.
const maxDecimals = 18

// RoundingMode selects how an exact amount is rounded to a whole minor unit.
//
// The zero value is [RoundingUnspecified], which is never a valid mode: tariff
// refuses to rate a charge whose currency has not chosen one, because a hidden
// default rounding mode is a silent compliance bug. Different jurisdictions and
// contracts mandate different modes, so the choice is always the caller's.
type RoundingMode uint8

const (
	// RoundingUnspecified is the zero value and is never valid; a currency must
	// choose a real rounding mode.
	RoundingUnspecified RoundingMode = iota
	// RoundHalfUp rounds to the nearest minor unit, breaking ties away from
	// zero (0.5 becomes 1, -0.5 becomes -1). This is the common "round half up"
	// of everyday arithmetic and many billing contracts.
	RoundHalfUp
	// RoundHalfEven rounds to the nearest minor unit, breaking ties toward the
	// even neighbour (0.5 and 1.5 both become their even side). Also known as
	// banker's rounding; it removes the upward bias of RoundHalfUp.
	RoundHalfEven
	// RoundFloor rounds toward negative infinity.
	RoundFloor
	// RoundCeil rounds toward positive infinity.
	RoundCeil
)

// String renders the rounding mode for diagnostics.
func (m RoundingMode) String() string {
	switch m {
	case RoundingUnspecified:
		return "unspecified"
	case RoundHalfUp:
		return "half-up"
	case RoundHalfEven:
		return "half-even"
	case RoundFloor:
		return "floor"
	case RoundCeil:
		return "ceil"
	default:
		return "RoundingMode(" + strconv.Itoa(int(m)) + ")"
	}
}

func (m RoundingMode) valid() bool {
	switch m {
	case RoundHalfUp, RoundHalfEven, RoundFloor, RoundCeil:
		return true
	default:
		return false
	}
}

// Currency describes the money unit a charge is priced in: the number of
// decimal places its minor unit has, and how exact amounts are rounded to it.
// The minor-unit scale is entirely currency-driven — 2 for USD, 0 for JPY, 3
// for KWD — and never hardcoded to cents anywhere in the package.
//
// A Currency is a small immutable value; construct one directly or use [USD],
// [JPY] and [KWD] for the common cases.
type Currency struct {
	// Code is an informational label such as "USD"; it does not affect
	// arithmetic.
	Code string
	// Decimals is the number of minor-unit decimal places: 2 for cents, 0 for
	// whole yen, 3 for fils.
	Decimals int
	// Rounding is the mode used to round an exact amount to a whole minor unit.
	// It must be set explicitly.
	Rounding RoundingMode
}

// USD returns the two-decimal US dollar with the given rounding mode.
func USD(r RoundingMode) Currency { return Currency{Code: "USD", Decimals: 2, Rounding: r} }

// JPY returns the zero-decimal Japanese yen with the given rounding mode.
func JPY(r RoundingMode) Currency { return Currency{Code: "JPY", Decimals: 0, Rounding: r} }

// KWD returns the three-decimal Kuwaiti dinar with the given rounding mode.
func KWD(r RoundingMode) Currency { return Currency{Code: "KWD", Decimals: 3, Rounding: r} }

func (c Currency) validate() error {
	if c.Decimals < 0 || c.Decimals > maxDecimals {
		return fmt.Errorf("%w: decimals %d out of range [0, %d]", ErrBadCurrency, c.Decimals, maxDecimals)
	}
	if !c.Rounding.valid() {
		return fmt.Errorf("%w: rounding mode not set", ErrBadCurrency)
	}
	return nil
}

// scaleRat returns 10^Decimals as an exact rational, the factor that turns an
// amount in major units into an amount in minor units.
func (c Currency) scaleRat() *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(c.Decimals)), nil)
	return new(big.Rat).SetInt(scale)
}

// round converts an exact amount already expressed in minor units to a whole
// int64 count of minor units, applying the currency's rounding mode. It is the
// single point at which exactness gives way to an integer, and it happens once
// per rated total, never mid-computation.
func (c Currency) round(exact *big.Rat) (int64, error) {
	n := exact.Num()
	d := exact.Denom() // always > 0 for a big.Rat

	floor := new(big.Int)
	rem := new(big.Int)
	floor.DivMod(n, d, rem) // floor = floor(n/d); 0 <= rem < d

	var q *big.Int
	switch c.Rounding {
	case RoundFloor:
		q = floor
	case RoundCeil:
		if rem.Sign() == 0 {
			q = floor
		} else {
			q = new(big.Int).Add(floor, big.NewInt(1))
		}
	case RoundHalfUp, RoundHalfEven:
		twoRem := new(big.Int).Lsh(rem, 1) // 2 * rem, compared against d
		switch twoRem.Cmp(d) {
		case -1: // fraction < 1/2, round down toward the floor
			q = floor
		case 1: // fraction > 1/2, round up
			q = new(big.Int).Add(floor, big.NewInt(1))
		default: // exactly 1/2
			q = c.breakTie(n.Sign(), floor)
		}
	default:
		return 0, fmt.Errorf("%w: rounding mode not set", ErrBadCurrency)
	}

	if !q.IsInt64() {
		return 0, fmt.Errorf("%w: %s", ErrOverflow, q.String())
	}
	return q.Int64(), nil
}

// breakTie resolves an exact half. For half-up the tie goes away from zero; for
// half-even it goes to the even neighbour. floor is the value rounded toward
// negative infinity, so floor+1 is the neighbour above it, and sign is the sign
// of the original amount.
func (c Currency) breakTie(sign int, floor *big.Int) *big.Int {
	up := new(big.Int).Add(floor, big.NewInt(1))
	if c.Rounding == RoundHalfEven {
		if floor.Bit(0) == 0 { // floor is even
			return floor
		}
		return up
	}
	// RoundHalfUp: away from zero.
	if sign >= 0 {
		return up
	}
	return floor
}

// Format renders a count of minor units as a plain decimal string with the
// currency's number of fractional digits: Format(4150) is "41.50" for USD,
// "4150" for JPY, and "4.150" for KWD. It is a display convenience and plays no
// part in rating.
func (c Currency) Format(minor int64) string {
	neg := minor < 0
	m := minor
	if neg {
		m = -m
	}
	var out string
	if c.Decimals <= 0 {
		out = strconv.FormatInt(m, 10)
	} else {
		scale := pow10(c.Decimals)
		whole := m / scale
		frac := m % scale
		out = fmt.Sprintf("%d.%0*d", whole, c.Decimals, frac)
	}
	if neg {
		return "-" + out
	}
	return out
}

func pow10(n int) int64 {
	p := int64(1)
	for i := 0; i < n; i++ {
		p *= 10
	}
	return p
}
