package tariff

import "errors"

// Sentinel errors returned (usually wrapped with context) by this package.
// Match them with [errors.Is] rather than by comparing error strings.
var (
	// ErrNegativeQuantity is returned by [Charge.Rate] when the quantity is
	// negative. A zero quantity is valid and rates to nothing.
	ErrNegativeQuantity = errors.New("tariff: negative quantity")

	// ErrNegativeAmount is returned by [Change] when a plan amount is negative.
	// Proration's Credit-is-negative / Charge-is-positive result only holds for
	// non-negative plan prices; a negative price is rejected rather than
	// silently inverting those signs.
	ErrNegativeAmount = errors.New("tariff: negative amount")

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
	// parts, or with a negative ratio. The total may be any sign — a negative
	// total is a proration credit split across lines.
	ErrBadAllocation = errors.New("tariff: invalid allocation")

	// ErrOverflow is returned when an exact amount, or an allocated share, does
	// not fit in an int64 count of minor units.
	ErrOverflow = errors.New("tariff: amount overflows int64 minor units")

	// ErrUnknownModel is returned when a charge names a rating model that this
	// package does not implement.
	ErrUnknownModel = errors.New("tariff: unknown rating model")

	// ErrBadPeriod is returned when a billing [Period] is unusable: its End is
	// not strictly after its Start, or a day-based [Basis] is applied to a
	// period that spans no whole calendar day (so the day denominator is zero).
	ErrBadPeriod = errors.New("tariff: invalid billing period")

	// ErrBadWindow is returned by [Period.Fraction] when the window is
	// incoherent — from is after to — or by [Prorate] when the fraction is nil.
	// A window that simply falls outside the period is not an error; it clamps
	// to a zero fraction.
	ErrBadWindow = errors.New("tariff: invalid proration window")

	// ErrBadBasis is returned by [Period.Fraction] when the proration [Basis]
	// is not one this package implements.
	ErrBadBasis = errors.New("tariff: unknown proration basis")

	// ErrBadDiscount is returned by a composition step when a discount is
	// malformed: a nil percentage, a percentage outside [0, 1], or a negative
	// fixed amount off.
	ErrBadDiscount = errors.New("tariff: invalid discount")

	// ErrBadFloor is returned by [MinimumCharge] when the floor is negative.
	ErrBadFloor = errors.New("tariff: invalid minimum charge")

	// ErrBadBalance is returned by [DrawCredit] or [DrawCommitment] when the
	// balance pointer is nil or the balance it points to is negative.
	ErrBadBalance = errors.New("tariff: invalid balance")

	// ErrCurrencyMismatch is returned by [Compose] when a [Charged] step's
	// currency does not share the invoice currency's code and minor-unit scale,
	// so its amounts cannot be summed with the rest of the invoice.
	ErrCurrencyMismatch = errors.New("tariff: currency mismatch")

	// ErrNilStep is returned by [Compose] when one of the steps is nil.
	ErrNilStep = errors.New("tariff: nil step")
)
