package tariff

import (
	"fmt"
	"math/big"
	"strconv"
	"time"
)

// Period is a billing period, the half-open instant range [Start, End). Its
// End must be strictly after its Start. A period is anchored in a
// *time.Location — Start.Location() is the period's location, the frame in
// which a day-based [Basis] counts calendar days and in which cycle boundaries
// are computed. Second-based fractions are location-independent, being real
// elapsed time.
type Period struct {
	// Start is the inclusive lower bound of the period.
	Start time.Time
	// End is the exclusive upper bound of the period; it must be after Start.
	End time.Time
}

func (p Period) validate() error {
	if !p.End.After(p.Start) {
		return fmt.Errorf("%w: end %s is not after start %s", ErrBadPeriod, p.End, p.Start)
	}
	return nil
}

// loc returns the period's frame of reference, the location its Start is
// expressed in. [time.Time.Location] never returns nil (it reports UTC for the
// zero value), so this is always a usable location.
func (p Period) loc() *time.Location {
	return p.Start.Location()
}

// Basis selects how [Period.Fraction] measures elapsed time. The zero value,
// [ProrateBySecond], is the Stripe default and needs no configuration.
type Basis uint8

const (
	// ProrateBySecond measures real elapsed time to the nanosecond. A day that
	// is 23 or 25 hours long across a DST transition contributes its true
	// length, so the fraction is never off by the missing or repeated hour.
	// This is the Stripe default and the zero value of Basis.
	ProrateBySecond Basis = iota
	// ProrateByDay measures whole calendar days in the period's location. Every
	// calendar day counts as exactly one, regardless of a DST transition within
	// it, matching the Chargebee day-based option. The day containing a
	// window's lower bound is floored to that day's start, so used and remaining
	// day counts partition the period exactly.
	ProrateByDay
)

// String renders the basis name for diagnostics.
func (b Basis) String() string {
	switch b {
	case ProrateBySecond:
		return "by-second"
	case ProrateByDay:
		return "by-day"
	default:
		return "Basis(" + strconv.Itoa(int(b)) + ")"
	}
}

// Fraction returns the exact fraction of the period covered by the window
// [from, to), as a *big.Rat that never crosses float64.
//
// The window is clamped to the period before measuring: the covered span is
// [max(from, Start), min(to, End)). A window wider than the period yields
// exactly 1; a window that falls entirely outside the period yields exactly 0;
// an empty window (from == to) yields 0. Only from strictly after to is an
// error ([ErrBadWindow]) — it is not a coherent window. The period itself must
// be valid, or [ErrBadPeriod] is returned.
//
// Under [ProrateBySecond] the fraction is real elapsed nanoseconds of the
// covered span over real elapsed nanoseconds of the whole period, so a DST day
// contributes its true length. Under [ProrateByDay] it is the count of whole
// calendar days in the covered span over the whole period, computed from civil
// dates in the period's location, so a DST day contributes exactly one and the
// result is immune to the 23- or 25-hour day. A day-based fraction requires the
// period to span at least one whole calendar day, or [ErrBadPeriod] is
// returned (its day denominator would be zero).
func (p Period) Fraction(from, to time.Time, b Basis) (*big.Rat, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	if from.After(to) {
		return nil, fmt.Errorf("%w: from %s is after to %s", ErrBadWindow, from, to)
	}

	// Clamp the window to the period. If the clamped span is empty (the window
	// lies wholly outside the period, or is itself empty) the covered fraction
	// is exactly zero.
	lo := maxTime(from, p.Start)
	hi := minTime(to, p.End)
	empty := !hi.After(lo)

	switch b {
	case ProrateBySecond:
		den := nanosBetween(p.Start, p.End) // > 0, End is after Start
		if empty {
			return new(big.Rat), nil
		}
		return new(big.Rat).SetFrac(nanosBetween(lo, hi), den), nil
	case ProrateByDay:
		loc := p.loc()
		term := civilDay(p.End, loc) - civilDay(p.Start, loc)
		if term <= 0 {
			return nil, fmt.Errorf("%w: day basis needs a period spanning a whole day", ErrBadPeriod)
		}
		if empty {
			return new(big.Rat), nil
		}
		covered := civilDay(hi, loc) - civilDay(lo, loc)
		return new(big.Rat).SetFrac(big.NewInt(covered), big.NewInt(term)), nil
	default:
		return nil, fmt.Errorf("%w: %d", ErrBadBasis, b)
	}
}

// Prorate returns amount scaled by frac, rounded once to the currency's minor
// unit with the currency's rounding mode. The amount is already an int64 count
// of minor units; frac is dimensionless, so amount * frac is the prorated
// amount in minor units, rounded through the same path [Charge.Rate] uses. A
// nil frac is [ErrBadWindow]; an unusable currency is [ErrBadCurrency]; a
// result that will not fit an int64 is [ErrOverflow].
func Prorate(amount int64, cur Currency, frac *big.Rat) (int64, error) {
	if err := cur.validate(); err != nil {
		return 0, err
	}
	if frac == nil {
		return 0, fmt.Errorf("%w: nil fraction", ErrBadWindow)
	}
	exact := new(big.Rat).SetInt64(amount)
	exact.Mul(exact, frac)
	return cur.round(exact)
}

// Proration is the outcome of a mid-period plan change: a negative Credit for
// the unused remainder of the old price, a positive Charge for the remaining
// time on the new price, and their Net. It is the verified cross-vendor model —
// Stripe's "−5 USD unused, 10 USD remaining, 5 USD net" and Chargebee's
// credit/charge/net — not a true-forward.
type Proration struct {
	// Credit is the credit for the unused remainder of the old price. It is
	// zero or negative.
	Credit int64
	// Charge is the charge for the remaining time on the new price. It is zero
	// or positive.
	Charge int64
	// Net is Charge + Credit, the amount actually billed for the change.
	Net int64
}

// Change computes the credit, charge and net for a plan change at instant at,
// partway through period p. The remaining fraction is p.Fraction(at, p.End, b);
// Credit is −oldAmount × remaining, Charge is +newAmount × remaining, each
// rounded once, and Net is their sum. A trial-to-paid change falls out with
// oldAmount = 0, giving a zero credit. Errors from the period, window or
// currency propagate.
//
// Both amounts must be non-negative — a plan price is not a debt — otherwise
// [ErrNegativeAmount] is returned. Given that, Credit is always ≤ 0 and Charge
// always ≥ 0; a downgrade (newAmount < oldAmount) yields a negative Net, a
// legitimate refund.
func Change(oldAmount, newAmount int64, cur Currency, p Period, at time.Time, b Basis) (Proration, error) {
	if oldAmount < 0 || newAmount < 0 {
		return Proration{}, fmt.Errorf("%w: old %d, new %d", ErrNegativeAmount, oldAmount, newAmount)
	}
	remaining, err := p.Fraction(at, p.End, b)
	if err != nil {
		return Proration{}, err
	}

	// Prorate validates the currency (so an unusable currency surfaces here)
	// and rounds once. Credit is the negative of the unused old amount:
	// round(−oldAmount × remaining), taken via the negated fraction so a
	// math.MinInt64 amount is never itself negated. Because remaining is in
	// [0, 1], neither prorated amount can exceed its input in magnitude, so the
	// charge and net arithmetic cannot overflow — the guards are defensive.
	credit, err := Prorate(oldAmount, cur, new(big.Rat).Neg(remaining))
	if err != nil {
		return Proration{}, err
	}
	charge, err := Prorate(newAmount, cur, remaining)
	if err != nil {
		return Proration{}, err
	}
	net, err := addInt64(charge, credit)
	if err != nil {
		return Proration{}, err
	}
	return Proration{Credit: credit, Charge: charge, Net: net}, nil
}

// CycleUnit is the step of a billing cycle: [Monthly] or [Yearly]. Monthly is
// the zero value.
type CycleUnit uint8

const (
	// Monthly steps one calendar month at a time. It is the zero value.
	Monthly CycleUnit = iota
	// Yearly steps one calendar year at a time.
	Yearly
)

// String renders the cycle unit for diagnostics.
func (u CycleUnit) String() string {
	switch u {
	case Monthly:
		return "monthly"
	case Yearly:
		return "yearly"
	default:
		return "CycleUnit(" + strconv.Itoa(int(u)) + ")"
	}
}

// NextBoundary returns the first anniversary cycle boundary strictly after
// from, for a cycle of the given unit anchored at anchor. Boundaries are
// anchor ± k units for integer k, all in the anchor's location and at the
// anchor's time of day.
//
// The month-end rule is the delicate part, and it is handled without drift: a
// boundary's day is the anchor's day of month clamped to the last valid day of
// the target month, always measured from the original anchor day. An anchor of
// January 31 therefore steps to February 28 (or 29 in a leap year) and then
// back to March 31, never sticking at the 28th; a February 29 anchor steps to
// February 28 in common years and returns to February 29 the next leap year.
func NextBoundary(anchor, from time.Time, unit CycleUnit) time.Time {
	// Estimate the step count in the anchor's frame: the count of calendar
	// months (or years) between anchor and from. That estimate lands the
	// boundary in from's own month/year, so the boundary one step earlier is
	// always before from and the estimate is at worst an undershoot — correct
	// it by walking forward to the first boundary strictly after from. This
	// keeps NextBoundary O(1) rather than iterating a boundary at a time.
	k := estimateSteps(anchor, from, unit)
	for !addCycles(anchor, k, unit).After(from) {
		k++
	}
	return addCycles(anchor, k, unit)
}

// NextCalendarBoundary returns the next calendar-aligned cycle boundary
// strictly after from: the first of the next month for [Monthly], or the first
// January for [Yearly], at midnight in from's location. It is the thin wrapper
// over [NextBoundary] with the anchor pinned to the first of the period —
// calendar-aligned cycles are just anniversary cycles anchored on the 1st.
func NextCalendarBoundary(from time.Time, unit CycleUnit) time.Time {
	loc := from.Location()
	y, m, _ := from.Date()
	var anchor time.Time
	switch unit {
	case Yearly:
		anchor = time.Date(y, time.January, 1, 0, 0, 0, 0, loc)
	default:
		anchor = time.Date(y, m, 1, 0, 0, 0, 0, loc)
	}
	return NextBoundary(anchor, from, unit)
}

// estimateSteps approximates how many units separate anchor from `from`,
// measured in the anchor's location so the correction loops in NextBoundary
// have to move at most a step or two.
func estimateSteps(anchor, from time.Time, unit CycleUnit) int {
	f := from.In(anchor.Location())
	ay, am, _ := anchor.Date()
	fy, fm, _ := f.Date()
	if unit == Yearly {
		return fy - ay
	}
	return (fy-ay)*12 + int(fm-am)
}

// addCycles returns the boundary k units from anchor, clamping the day of month
// to the target month's last valid day (measured from the original anchor day,
// so there is no permanent drift) and preserving the anchor's time of day and
// location.
func addCycles(anchor time.Time, k int, unit CycleUnit) time.Time {
	y, m, _ := anchor.Date()
	day := anchor.Day()
	hh, mm, ss := anchor.Clock()
	loc := anchor.Location()

	var ny int
	var nm time.Month
	if unit == Yearly {
		ny, nm = y+k, m
	} else {
		// Normalize month index with floored division so negative k works too.
		total := int(m) - 1 + k
		ny = y + floorDiv(total, 12)
		nm = time.Month(floorMod(total, 12) + 1)
	}

	if dim := daysInMonth(ny, nm); day > dim {
		day = dim
	}
	return time.Date(ny, nm, day, hh, mm, ss, anchor.Nanosecond(), loc)
}

// daysInMonth returns the number of days in month m of year ny, leap years
// included. Day 0 of the following month is the last day of month m.
func daysInMonth(ny int, m time.Month) int {
	return time.Date(ny, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func floorMod(a, b int) int {
	m := a % b
	if m != 0 && (m < 0) != (b < 0) {
		m += b
	}
	return m
}

// nanosBetween returns to − from in nanoseconds as an exact big.Int. It is
// computed from Unix seconds so it neither overflows for far-apart instants nor
// depends on wall-clock day length: the difference is real elapsed time, which
// is exactly what a second-based proration must measure across a DST change.
func nanosBetween(from, to time.Time) *big.Int {
	secs := new(big.Int).Sub(big.NewInt(to.Unix()), big.NewInt(from.Unix()))
	secs.Mul(secs, big.NewInt(int64(time.Second)))
	return secs.Add(secs, big.NewInt(int64(to.Nanosecond())-int64(from.Nanosecond())))
}

// civilDay returns the count of days from a fixed epoch to t's civil date in
// loc — a serial day number. Differences of two such numbers count whole
// calendar days exactly, independent of wall-clock day length, so day-based
// proration is immune to DST. It uses Howard Hinnant's days_from_civil
// algorithm (public domain), valid across the full range of representable
// years.
func civilDay(t time.Time, loc *time.Location) int64 {
	y, m, d := t.In(loc).Date()
	yi := int64(y)
	mi := int64(m)
	if mi <= 2 {
		yi--
	}
	var era int64
	if yi >= 0 {
		era = yi / 400
	} else {
		era = (yi - 399) / 400
	}
	yoe := yi - era*400
	var mp int64
	if mi > 2 {
		mp = mi - 3
	} else {
		mp = mi + 9
	}
	doy := (153*mp+2)/5 + int64(d) - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	return era*146097 + doe - 719468
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
