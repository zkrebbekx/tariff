# tariff

A pure-Go usage-billing **rating core** — a price-plan component plus a usage
quantity in, itemized line items out, with exact, deterministically-rounded
amounts. Zero dependencies.

Every usage-billing product reimplements the same rating algebra, and the OSS
options are the wrong shape for embedding — services that need ClickHouse and
Kafka, or AGPL engines that can't sit inside proprietary code. `tariff` is just
the calculator: it takes a quantity you already aggregated and turns it into
money. It meters nothing, stores nothing, and taxes nothing.

The interesting part is what happens to the fractions of a cent. Whether a
per-unit rate of `$0.0006` is an exact rational or a drifting float, whether
three tier lines still sum to the invoice total after rounding, and whether the
yen (no decimals) and the dinar (three) round on their own minor unit rather
than a hardcoded cent — `tariff` takes those three questions seriously.

- **Exact rates, no float drift.** Rates are `math/big.Rat`; `quantity × rate`
  is evaluated entirely in `math/big` and rounded to the minor unit exactly
  once, at the line boundary. `$1.005` rounds to `$1.01`, not the `$1.00` a
  float64 quietly produces.
- **Rounding is explicit.** Half-up, half-even (banker's), floor or ceil — the
  caller chooses on the currency. There is no default, because a hidden one is a
  compliance bug.
- **Lines reconcile.** A graduated charge rates each tier exactly, sums exactly,
  rounds the total once, then allocates it back across the tier lines so the
  subtotals sum to the total with no drift.
- **Currency-driven minor units.** 2 places for USD, 0 for JPY, 3 for KWD —
  never hardcoded to cents.
- **Five rating models.** Per-unit, graduated (tiered), volume, package/block,
  and stairstep, all with optional free allowances and a fixed fee.
- **Proration, DST- and month-end-safe.** Credit-unused + charge-new + net, to
  the second or by whole day, with cycle boundaries that clamp `Jan 31 → Feb 28`
  and back to `Mar 31` without drifting.
- **Interaction order is the caller's.** Charges, discounts, minimums, credits
  and commitments compose as explicit, ordered, labeled steps — tariff computes
  each order faithfully instead of guessing one.
- **Typed errors**, matchable with `errors.Is`.
- **Zero dependencies.** Go 1.23+.

```go
import "github.com/zkrebbekx/tariff"
```

## Example

```go
c := tariff.Charge{
    Model:    tariff.Graduated,
    Currency: tariff.USD(tariff.RoundHalfUp),
    Tiers: []tariff.Tier{
        {UpTo: 5, UnitRate: big.NewRat(7, 1)},
        {UpTo: 10, UnitRate: big.NewRat(13, 2)}, // $6.50
        {Last: true, UnitRate: big.NewRat(6, 1)},
    },
}

res, _ := c.Rate(6)
fmt.Println(c.Currency.Format(res.Total)) // 41.50
// res.Lines: 5 @ $7 = $35.00, 1 @ $6.50 = $6.50
```

## The rating models

Definitions are pinned to Stripe and Lago, with their golden vectors baked into
the test suite.

- **Per-unit** — `quantity × rate`, one line.
- **Graduated (tiered)** — each tier's units at that tier's rate, summed. The
  Stripe schedule `1–5 @ $7, 6–10 @ $6.50, 11+ @ $6` rates quantity 6 to
  **$41.50** and quantity 11 to **$73.50**. One line per tier touched.
- **Volume** — the whole quantity at the single rate of the tier it lands in.
  The same schedule rates quantity 6 to **$39.00**. Under a steeply decreasing
  schedule the total can *fall* as usage grows into a cheaper tier — intended,
  not a bug.
- **Package / block** — round up to whole blocks of size N after the free
  allowance: `$5 per 100-unit block, 100 free, 201 units → $10.00`. The
  allowance is subtracted *before* the ceil.
- **Stairstep** — a flat fee for landing in a tier band, regardless of position
  within it.

Tiers are half-open on the low side: tier *i* covers the units in
`(tiers[i-1].UpTo, tiers[i].UpTo]`, the first covering `(0, tiers[0].UpTo]`, and
the final tier is unbounded (`Last: true`).

## Exactness and reconciliation

Rating each tier exactly and rounding the *total* once — then allocating that
total back across the lines — is what keeps an invoice's line items summing to
its total. Three tiers that each end in half a cent would drift to one cent too
many if rounded independently (`11 + 21 + 31 = 63`); `tariff` rounds the sum
(`62`) and allocates it (`10 + 21 + 31`), so `sum(lines) == total`, exactly,
always.

```go
shares, _ := tariff.Allocate(100, []int64{1, 1, 1}) // [34 33 33]
```

`Allocate` distributes the floor of each share and hands the leftover minor
units to the parts with the largest fractional remainder (the Hamilton method),
so a zero-ratio part receives zero and an exact part keeps its amount. It loses
nothing and is deterministic. The total may be negative — a proration credit
splits across lines with the sign carried through — the property that makes
reconciliation and proration penny-safe.

## Currencies and rounding

```go
tariff.USD(tariff.RoundHalfUp)   // 2 decimals
tariff.JPY(tariff.RoundHalfEven) // 0 decimals
tariff.KWD(tariff.RoundFloor)    // 3 decimals
```

Amounts out are `int64` counts of the currency's minor unit. `tariff` ships no
money type; wrap the amounts in whatever you like at the boundary.

## Proration

A plan that changes mid-period is rated with the verified cross-vendor model —
credit the unused old price, charge the new price for the remaining time, net
them — not a true-forward.

```go
p := tariff.Period{Start: start, End: end} // half-open [Start, End)
pr, _ := tariff.Change(1000, 2000, usd, p, at, tariff.ProrateBySecond)
// pr.Credit -500, pr.Charge 1000, pr.Net 500  ($10 → $20 upgrade at the midpoint)
```

`Period.Fraction` returns the exact fraction of a period a window covers, as a
`*big.Rat`, under one of two bases:

- **`ProrateBySecond`** (the default) measures real elapsed time. A day that is
  23 or 25 hours long across a DST change contributes its true length, so the
  fraction is never off by the missing or repeated hour.
- **`ProrateByDay`** counts whole calendar days in the period's location, so a
  DST day is exactly one day.

Cycle boundaries handle the month-end trap without drift: `NextBoundary` clamps
a `Jan 31` anchor to `Feb 28` (or `29` in a leap year) and then back to
`Mar 31`, always measuring from the original anchor day. `NextCalendarBoundary`
is the calendar-aligned wrapper (anchored on the 1st). Credits are negative
amounts, and `Allocate` splits them across lines with the sign carried through.

## Composition

Charges, discounts, minimums, prepaid credits and spend commitments must
combine, and **the order is where real billing systems disagree** — does a
percentage discount apply before or after a minimum? Public vendor docs
under-specify it, so `tariff` does not bake an order: it exposes the operations
as composable steps you sequence explicitly, each a labeled, auditable line.

```go
inv, _ := tariff.Compose(usd,
    tariff.Charged(c, 1),                              // rate a charge
    tariff.PercentOff(big.NewRat(1, 10), "10% off"),   // fraction, not whole percent
    tariff.MinimumCharge(9500, "minimum $95"),         // top up to the floor
)
// discount before minimum: $100 → $90 → floored to $95
// swap the two steps and the $100 clears the floor untouched, ending at $90
```

The steps are `Charged`, `PercentOff`, `AmountOff`, `MinimumCharge`,
`DrawCredit` and `DrawCommitment`. Discounts round once via the currency; credit
and commitment draws are capped at both the running total and the balance and
never go negative. `tariff`'s job is that each step is individually correct and
exactly rounded — not to decide the sequence.

## Errors

Failures are typed sentinels matchable with `errors.Is`: `ErrNegativeQuantity`,
`ErrEmptyTiers`, `ErrTierOrder`, `ErrNoRate`, `ErrBadPackage`,
`ErrBadAllowance`, `ErrBadCurrency`, `ErrBadAllocation`, `ErrOverflow`,
`ErrUnknownModel`, `ErrBadPeriod`, `ErrBadWindow`, `ErrBadBasis`,
`ErrBadDiscount`, `ErrBadFloor`, `ErrBadBalance`, `ErrCurrencyMismatch` and
`ErrNilStep`. A zero quantity is valid and rates to nothing; a negative one is
an error.

## Non-goals

- **No metering.** Deduplication, idempotency, late events, aggregation — that
  is the upstream concern that forces ClickHouse and Kafka on the incumbents.
  `tariff` takes an already-aggregated quantity.
- **No tax.** The moat there is jurisdiction data, not code. `tariff` emits
  pre-tax line items.
- **No persistence, subscriptions, invoicing, dunning or payments.** A rating
  core computes; it stores nothing.
- **No money type.** `tariff` has amounts — `int64` minor units — not a `Money`.

## License

MIT
