package tariff

import "errors"

// Sentinel errors returned (usually wrapped with context) by this package.
// Match them with [errors.Is] rather than by comparing error strings.
var (
	// ErrNegativeQuantity is returned by [Charge.Rate] when the quantity is
	// negative. A zero quantity is valid and rates to nothing.
	ErrNegativeQuantity = errors.New("tariff: negative quantity")

	// ErrEmptyTiers is returned when a tiered model (graduated, volume,
	// stairstep) is rated with no tiers at all.
	ErrEmptyTiers = errors.New("tariff: no tiers")

	// ErrTierOrder is returned when a tier schedule is malformed: upper bounds
	// that do not strictly increase, a non-positive bound, a non-final tier
	// marked unbounded, or a final tier that is not unbounded. Tiers are
	// half-open on the low side — tier i covers the units in (tiers[i-1].UpTo,
	// tiers[i].UpTo] — and the final tier must be unbounded so that every
	// quantity is covered.
	ErrTierOrder = errors.New("tariff: tiers out of order")

	// ErrNoRate is returned when a required per-unit or flat rate is missing or
	// negative: a per-unit charge with a nil rate, a graduated or volume tier
	// with a nil or negative rate, a stairstep tier with a negative flat rate,
	// or a negative flat fee on the charge.
	ErrNoRate = errors.New("tariff: missing or invalid rate")

	// ErrBadPackage is returned when a package charge is misconfigured: a
	// package size that is not positive, or a negative package price.
	ErrBadPackage = errors.New("tariff: invalid package configuration")

	// ErrBadAllowance is returned when a charge's free allowance is negative.
	ErrBadAllowance = errors.New("tariff: negative free allowance")

	// ErrBadCurrency is returned when a currency is unusable: negative or
	// implausibly large decimal places, or an unset rounding mode. A charge
	// must always choose its rounding mode explicitly, because a hidden default
	// is a silent compliance bug.
	ErrBadCurrency = errors.New("tariff: invalid currency")

	// ErrBadAllocation is returned by [Allocate] when asked to split across no
	// parts, or with a negative total or a negative ratio.
	ErrBadAllocation = errors.New("tariff: invalid allocation")

	// ErrOverflow is returned when an exact amount, or an allocated share, does
	// not fit in an int64 count of minor units.
	ErrOverflow = errors.New("tariff: amount overflows int64 minor units")

	// ErrUnknownModel is returned when a charge names a rating model that this
	// package does not implement.
	ErrUnknownModel = errors.New("tariff: unknown rating model")
)
