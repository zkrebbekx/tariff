// Package tariff is a pure-Go usage-billing rating core: a price-plan component
// plus a usage quantity in, itemized line items out, with exact,
// deterministically-rounded amounts. It has zero dependencies — the standard
// library's math/big is the exact-rate engine.
//
// tariff rates; it does not meter, store, tax, or invoice. It takes a quantity
// already aggregated by whatever the caller uses and turns it into money.
//
//	c := tariff.Charge{
//		Model:    tariff.Graduated,
//		Currency: tariff.USD(tariff.RoundHalfUp),
//		Tiers: []tariff.Tier{
//			{UpTo: 5, UnitRate: big.NewRat(7, 1)},
//			{UpTo: 10, UnitRate: big.NewRat(13, 2)}, // $6.50
//			{Last: true, UnitRate: big.NewRat(6, 1)},
//		},
//	}
//	res, _ := c.Rate(6)
//	fmt.Println(c.Currency.Format(res.Total)) // 41.50
//
// # The exactness discipline
//
// Rates are [math/big.Rat], exact rationals: a per-unit price of $0.0006 is
// exactly 6/10000, not a float. quantity * rate is evaluated entirely in
// math/big and is never a float64. The exact amount is rounded to a whole minor
// unit exactly once, at the line boundary, using a caller-selected rounding
// mode — half-up, half-even (banker's), floor, or ceil. There is no hidden
// default: a [Currency] whose [RoundingMode] is unset is refused, because a
// silent default is a compliance bug.
//
// Amounts out are int64 counts of the currency's minor unit. The scale is
// currency-driven — 2 decimals for USD, 0 for JPY, 3 for KWD — and never
// hardcoded to cents. tariff ships no money type; the caller wraps the int64
// amounts in whatever they like at the boundary.
//
// # Rating models
//
// See [Model]. PerUnit is quantity times a flat rate. Graduated charges each
// tier's units at that tier's rate and sums them (marginal). Volume charges the
// whole quantity at the single rate of the tier it lands in — under a
// decreasing schedule the total can fall as usage grows, which is intended.
// Package rounds up to whole blocks after a free allowance. Stairstep charges a
// flat fee per tier band. A free allowance and an optional fixed flat fee
// compose with every model.
//
// Tiers are half-open on the low side: tier i covers the units in
// (tiers[i-1].UpTo, tiers[i].UpTo], the first covering (0, tiers[0].UpTo], and
// the final tier is unbounded.
//
// # Line-item reconciliation
//
// A graduated charge rates every tier exactly, sums exactly, rounds the total
// once, then allocates that rounded total back across the tier lines with
// [Allocate], so the line subtotals sum to the total with no drift. Rounding
// each line independently would let three lines that each end in half a minor
// unit sum to one unit more than the correctly rounded total; allocation
// prevents that. Across any [Result], the line subtotals sum to Total exactly.
//
// # Allocation
//
// [Allocate] splits a whole amount across parts by ratio, distributing the
// floor of each share and handing the leftover minor units out round-robin from
// the first part. It loses nothing and is deterministic — the property that
// makes reconciliation, and later proration, penny-safe.
//
// # Errors
//
// Failures are typed sentinels matchable with [errors.Is]:
// [ErrNegativeQuantity], [ErrEmptyTiers], [ErrTierOrder], [ErrNoRate],
// [ErrBadPackage], [ErrBadAllowance], [ErrBadCurrency], [ErrBadAllocation],
// [ErrOverflow] and [ErrUnknownModel].
//
// # Deviations from the design sketch
//
// Two intentional refinements over the indicative shape in docs/DESIGN.md,
// recorded there under "Phase 1 as built":
//
//   - Charge gains an optional FlatFee (minor units) so a fixed-plus-usage
//     charge — the most common SaaS shape — is one charge, and so the vendor
//     "$49 = 65000 * $0.0006 + $10" vector is reproduced exactly. It emits its
//     own line and applies even at zero usage.
//   - Rounding is explicit on the Currency (no default), surfaced as [USD],
//     [JPY] and [KWD] constructors that force the choice.
//
// The design's parenthetical that a volume total decreases from quantity 10 to
// 11 under the Stripe golden schedule is inaccurate — there it rises from
// $65.00 to $66.00. The decrease property is real but needs a steeper rate
// drop; see the volume tests. Likewise the correct graduated total for quantity
// 11 under that schedule is $73.50 (5*$7 + 5*$6.50 + 1*$6), not $71.50.
package tariff
