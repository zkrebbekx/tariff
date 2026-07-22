# tariff — design

A pure-Go **rating core**: a price-plan spec plus a usage quantity in,
itemized line items out. Zero dependencies.

Status: Phase 1 (this doc's rating core) implemented — see "Phase 1 as built".

## Why this exists

Every usage-billing product reimplements the same rating algebra, and the OSS
options are all the wrong shape for embedding. Verified 2026-07-21 against the
live GitHub API and vendor docs:

- Lago (10.2k★) — Ruby engine, AGPL-3.0, Docker Compose service.
- flexprice (3.6k★) — Go, but a service needing Postgres + Kafka + ClickHouse +
  Temporal, AGPL-3.0.
- OpenMeter (2.1k★) — service requiring ClickHouse + Kafka + Postgres; the Go
  path is an HTTP client only. Apache-2.0, still beta.
- Kill Bill (5.6k★) — Java platform, Apache-2.0; its docs tell integrators to
  *supply their own metering*.
- Meteroid (1.2k★) — Rust, AGPL-3.0, pre-1.0.

Two facts make the gap worth filling. First, **every embeddable-language OSS
engine is AGPL**, so none can be a dependency inside proprietary code — a
permissive pure-Go rating library is unoccupied. Second, the paid tier is
priced as a **percentage of your billed revenue** (Stripe Billing 0.7%,
Metronome 0.8%, Chargebee 0.75%), so the cost scales with success — the
condition that pushes teams to self-host.

## Scope — what tariff is, and is not

tariff is the **rating calculator Kill Bill tells you to supply yourself**, not
a billing platform.

**In scope (this is the whole library):**
- Rating models: per-unit, graduated (tiered), volume, package/block,
  stairstep/flat-per-tier, with free allowances.
- A price-plan spec → itemized line items with exact, deterministically-rounded
  amounts.
- Proration / billing-period calendar (phase 2).
- Interaction order of charges, discounts, minimums, credits and commitments —
  as explicit caller-controlled policy (phase 2).

**Explicitly NOT in scope — and the exclusions are what keep tariff embeddable:**
- **Metering / aggregation / event ingestion.** Deduplication, idempotency
  keys, late-and-out-of-order events, sum/max/unique-count — that is the
  upstream concern that forces ClickHouse and Kafka on every incumbent. tariff
  takes a *quantity*, already aggregated by whatever the caller uses, and rates
  it. This boundary is the entire reason tariff can be a zero-infra library.
- **Tax.** The moat there is ~12k US jurisdiction *data*, not code. tariff
  emits pre-tax line items; a tax layer is the caller's problem.
- **Persistence, subscriptions lifecycle, invoicing, dunning, payments.** A
  rating core computes; it stores nothing.

## Money: exact rates, integer minor units, no dependency

Two numeric facts drive the whole design.

1. **Rates are sub-cent and must be exact.** A per-unit rate of `$0.0006` is an
   exact rational, not a float. `quantity × rate` is computed in `math/big`
   (`*big.Rat`, stdlib) so it never drifts, and is rounded to the currency's
   minor unit only at the line-item boundary. This is the same exact-rational
   discipline `quanta` uses for unit conversion, and for the same reason: float
   cents silently lose money.
2. **Amounts are integers in the currency's smallest unit.** A line item's
   amount is an `int64` count of minor units (cents, pence, or — for
   zero-decimal currencies like JPY — whole units). This is the Fowler Money
   pattern's core, minus the type: tariff has no `Money` type, it has amounts.

**tariff takes no money-library dependency.** The research pointed at
`go-money`'s `Allocate` for penny-safe remainder splitting, but the only thing
tariff would borrow is a ~10-line round-robin allocator — not a money type.
Staying zero-dependency, like every sibling library, is worth more than reusing
those lines, so tariff ships its own tested allocator (see *Allocation*). The
caller is free to wrap tariff's `int64` amounts in `go-money`, `bojanz/currency`
or anything else at the boundary.

Rounding mode is **caller-selected and explicit** — half-up, half-even
(banker's), floor, ceil — because different jurisdictions and contracts mandate
different modes, and a hidden default is a silent compliance bug. Rounding
happens once per line item, never mid-computation.

## The rating models — definitions locked to primary vendor docs

These are pinned verbatim against Stripe and Lago (two independent vendors that
agree), with golden test vectors that go straight into the suite. Getting
graduated-vs-volume wrong ships silently incorrect invoices; these vectors are
the test that justifies the library.

### per-unit
`amount = quantity × unitRate`, rounded once. Trivial. The degenerate
single-tier case of graduated.

### graduated (a.k.a. tiered) — cumulative
Each tier's units are charged at *that tier's* rate, and the per-tier totals
are **summed**. Marginal.

> Golden (Stripe): tiers 1–5 @ $7, 6–10 @ $6.50, 11+ @ $6. Quantity **6** →
> **$41.50** = 5×$7 + 1×$6.50.
> Golden (Lago): 250 units across $1.00 / $0.50 / $0.10 tiers → **$155.00**.

Line items: **one per tier touched**, each carrying its tier's quantity, rate
and subtotal. Classic bugs to avoid: which tier the marginal unit lands in,
off-by-one at tier boundaries (tiers are `(lastUpTo, upTo]` — half-open on the
low side), and confusing this with volume.

### volume — single rate for the whole quantity
The **entire** quantity is charged at the single rate of the one tier the total
lands in. The total can **decrease** as usage grows (crossing into a cheaper
tier), which is intended, not a bug.

> Golden (Stripe): same tiers, quantity **6** → **$39.00** = 6 × $6.50.

One line item. The bug to avoid is computing this cumulatively (that's
graduated).

### package / block
Round the chargeable quantity **up** to the next whole block of size *N* (after
subtracting any free allowance), then charge per block.
`blocks = ceil((quantity − free) / N)`, `amount = blocks × blockPrice`.

> Golden (Lago): $5 per 100-unit package, 100 free units. **201 units** →
> **$10** = $0 (first 100 free) + $5 (units 101–200) + $5 (unit 201, rounds a
> whole block up).

Bugs to avoid: rounding direction (must be **up**, `ceil`, never truncate), and
applying the free allowance *before* the `ceil`, not after.

### stairstep / flat-per-tier
A flat fee for landing in a tier, regardless of exact quantity within it. One
line item, the tier's flat amount. Low difficulty.

### free allowances
A quantity subtracted before rating (per-unit / graduated / volume) or before
the block ceil (package). Composes with every model.

## Allocation

When a single amount must be split across parts (proration remainders, or
splitting a rounded total back across tiers so the line items sum to the
rounded whole), tariff distributes the floor to each part and hands the leftover
minor units to the parts with the **largest fractional remainder** (the
largest-remainder / Hamilton method), ties broken by position. Nothing is lost,
the result is deterministic, and — unlike a round-robin-from-first split — no
part receives a penny it did not round up: a zero-rate (free) tier stays at
zero and an exactly-whole tier keeps its exact amount. Property under test: the
parts always sum **exactly** to the input, for any ratios and any remainder,
and a zero weight receives zero.

> **Correction, found in phase-1 review.** The first implementation handed the
> leftover minor units out round-robin from the first part (the `go-money`
> `Allocate` semantics). That keeps the sum exact but *misattributes* the
> pennies: a free tier showed a 1¢ charge, and an exact $35.00 tier showed
> $35.01, because the earliest lines always absorbed the remainder regardless
> of whether they had rounded up. Largest-remainder fixes the attribution while
> preserving the exact-sum invariant. This deliberately departs from the
> `go-money` semantics the money layer documents — for a rating library the
> per-line amount must not lie, even when the total is right.

## Shape (indicative — the review will pressure-test it)

```go
type Model uint8
const ( PerUnit Model = iota; Graduated; Volume; Package; Stairstep )

type Tier struct {
    UpTo      int64    // inclusive upper bound; 0 with Last=true means unbounded
    Last      bool
    UnitRate  *big.Rat // per unit within this tier (per-unit/graduated/volume)
    FlatRate  int64    // minor units, for stairstep
}

type Charge struct {
    Model         Model
    Currency      Currency // decimal places + rounding mode
    Tiers         []Tier
    UnitRate      *big.Rat // for the tier-less per-unit case
    PackageSize   int64
    PackagePrice  int64    // minor units per block
    FreeAllowance int64
}

func (c Charge) Rate(quantity int64) (Result, error)

type Result struct {
    Total int64        // minor units, post-rounding
    Lines []Line       // one per tier touched (or one), each with quantity, rate, subtotal
}
```

Errors are typed sentinels (`ErrNegativeQuantity`, `ErrEmptyTiers`,
`ErrTierOrder`, `ErrNoRate`, `ErrBadPackage`) matchable with `errors.Is`.

## Phasing

- **Phase 1 (now):** the rating models above + exact rate arithmetic + rounding
  + allocation, against the golden vectors. Zero-dep, ≥95% coverage, the sibling
  house style.
- **Phase 2:** proration / billing-period calendar (credit-unused + charge-new
  + net, day- and second-based, DST- and month-end-safe — the cross-vendor
  standard from Stripe and Chargebee), and the interaction-order policy
  (discount / minimum / credit / commitment ordering) as *explicit configurable
  composition*, because public vendor docs under-specify the order and a baked
  default would be a guess.
- **Phase 3+:** optional REST service, console, WASM playground — the
  flexitype / chronicle pattern.

## Open questions

1. Rate representation: `*big.Rat` is exact but a pointer; a value decimal type
   would be nicer to pass around. Phase 1 uses `*big.Rat` for correctness and
   revisits ergonomics only if the review shows a clean value alternative.
2. Do line items round independently and risk summing to ≠ the rounded total,
   or is the total rounded once and allocated back across lines? Lean: rate each
   tier exactly, sum exactly, round the total once, allocate the rounded total
   back so lines reconcile — but this needs a golden test proving reconciliation
   for a case where independent rounding would drift.
3. Zero-decimal currencies (JPY) and three-decimal (KWD, BHD): the minor-unit
   scale is per-currency; verify the rounding boundary is currency-driven, not
   hardcoded to cents.

## Phase 1 as built

Implemented and shipped: per-unit, graduated, volume, package/block, stairstep,
free allowances; exact `*big.Rat` rate arithmetic; explicit per-currency
rounding; the largest-remainder allocator; ≥95% coverage (99.6%),
zero-dependency, sibling house style. All golden vectors reproduce exactly.

Resolutions and refinements against the sketch above:

- **Open question 2 (line-item reconciliation) — resolved by allocation.**
  A graduated charge rates each tier exactly, sums exactly, rounds the total
  **once**, then allocates the rounded total back across the tier lines with the
  largest-remainder allocator, so `sum(lines) == Total` exactly *and* no line is
  credited a penny it did not round up. The golden test
  `TestGraduatedReconciliation` uses three tiers of $0.105 / $0.205 / $0.305:
  independent half-up per-line rounding drifts to 63c, the once-rounded total is
  62c, and allocation yields lines `10 + 21 + 31 = 62` (the leftover lands on the
  two larger tiers by proportional remainder — see the Allocation Correction).
- **Open question 3 (minor-unit scale) — confirmed currency-driven.** `Currency`
  carries `Decimals`; the scale is `10^Decimals` as a `*big.Rat`, exercised by
  JPY (0) and KWD (3) tests. Never hardcoded to cents.
- **Open question 1 (rate representation).** Kept `*big.Rat` for correctness, as
  planned; no clean value alternative surfaced.
- **Rounding is explicit on the currency.** `RoundingMode`'s zero value is
  `RoundingUnspecified` and is refused (`ErrBadCurrency`); `USD`/`JPY`/`KWD`
  constructors force the choice. A hidden default would be a silent compliance
  bug.
- **Addition: `Charge.FlatFee`** (minor units) — a fixed component added to any
  model as its own line, so a fixed-plus-usage charge is one `Charge`. This is
  how the Lago `$49 = 65000 × $0.0006 + $10` vector is reproduced. It applies
  even at zero usage.

### Corrections to the spec

- **Graduated quantity 11 is $73.50, not $71.50.** Under `1–5 @ $7, 6–10 @
  $6.50, 11+ @ $6`, quantity 11 is `5×$7 + 5×$6.50 + 1×$6 = $73.50`. The
  `$71.50` figure circulated for this vector is arithmetically wrong; the test
  pins `$73.50`.
- **The volume "can decrease" example needs a steeper drop than the Stripe
  schedule.** Crossing 10→11 on `6–10 @ $6.50, 11+ @ $6` the volume total
  *rises* from $65.00 to $66.00, because `11×6 > 10×6.5`. The decrease property
  is real but requires the rate to fall faster than the quantity grows; the test
  demonstrates it with `1–10 @ $10, 11+ @ $1` (10→11 falls $100.00 → $11.00).

### Corrections found in phase-1 review

- **The flat-fee add was the one unguarded arithmetic step.** Every multiply and
  round in the rate path checks for int64 overflow, but folding `FlatFee` into
  the rated total did not — a total near `MaxInt64` plus any fee wrapped to a
  negative invoice with no error. Now an `addInt64` guard returns `ErrOverflow`
  like the rest of the path.
- **Round-robin allocation misattributed pennies.** See the Correction under
  *Allocation* above: replaced with largest-remainder so free and exact tiers
  keep their true amounts and a zero-ratio `Allocate` part receives zero.

## Phase 2 — proration and composition

Two additions, both grounded in verified cross-vendor mechanics (Stripe and
Chargebee agree on the proration model; vendor docs deliberately under-specify
the composition order, which is why tariff makes it explicit).

### Proration

The question: a subscription priced per billing period changes mid-period —
what is charged? The cross-vendor standard is **credit-unused + charge-new +
net**, not true-forward:

> Stripe (verbatim): "the customer is billed an additional 5 USD: −5 USD for
> unused time on the initial price, and 10 USD for the remaining time on the new
> price." Default `proration_behavior=create_prorations`, prorated **to the
> second**.
> Chargebee (verbatim): credit `= (Old plan amount / term days) × remaining
> days`; charge `= (New plan amount / term days) × remaining days`; net `=
> charge − credit`.

The core is a **fraction of a billing period**, computed exactly:

```go
type Period struct { Start, End time.Time }        // half-open [Start, End)
type Basis uint8   // ProrateBySecond (default), ProrateByDay

// Fraction returns the exact fraction of p covered by [from, to), as a *big.Rat.
func (p Period) Fraction(from, to time.Time, b Basis) (*big.Rat, error)

// Prorate returns amount × fraction, rounded once to the currency's minor unit.
func Prorate(amount int64, cur Currency, frac *big.Rat) (int64, error)

// Change computes the credit, charge and net for a mid-period plan change.
func Change(oldAmount, newAmount int64, cur Currency, p Period, at time.Time, b Basis) (Proration, error)
type Proration struct { Credit, Charge, Net int64 }  // Credit is negative
```

The fraction is a `*big.Rat` for the same reason rates are: `remaining /
termDays` must not drift. Rounding happens once, at the money boundary.

Difficulty is in the **calendar**, not the arithmetic. The traps, each with a
test:

- **Basis.** Second-based (Stripe default) vs day-based (Chargebee option). A
  `Period` of one month has a different denominator under each; support both,
  default to second.
- **Timezone / DST.** A period is anchored in a `*time.Location`. A day that is
  23 or 25 hours long (DST transition) must not make the fraction wrong. The
  second-based basis uses real elapsed time; the day-based basis counts
  calendar days in the period's location. Test a period spanning a DST change in
  both bases.
- **Month-end anniversary.** Anniversary cycles anchored on the 31st: the next
  period end is Feb 28/29, then back to Mar 31. Cycle-boundary computation
  (`NextBoundary(anchor, from, unit)`) must clamp to the last valid day of the
  target month, and must not drift the anchor permanently to the 28th. This is
  the classic bug; test `Jan 31 → Feb 28 → Mar 31` explicitly.
- **Anniversary vs calendar-aligned cycles.** Anniversary: period boundaries are
  N months from a signup anchor. Calendar-aligned: boundaries are the 1st of the
  month. Both are just different `NextBoundary` policies over the same fraction
  math.
- **Trial → paid.** A zero-amount period transitioning to paid is a plan change
  from 0; the credit half is zero. Falls out of `Change` with `oldAmount=0`.

`Allocate` must be **extended to signed totals** for proration credits (a credit
splits a negative amount across lines). Phase 1 refuses a negative total; phase 2
lifts that, preserving the exact-sum and largest-remainder properties with the
sign carried through. This is a documented phase-1 limitation now closed.

### Interaction-order composition

Charges, discounts, minimum charges, prepaid credits and spend commitments must
combine, and **the order is where real systems disagree and litigate** — does a
percentage discount apply before or after a minimum? Are credits drawn down
before or after usage is rated? Public vendor docs under-specify this; the one
concrete rule found is Stripe's "proration line items are `discountable=false`."

So tariff does **not** bake an order. It exposes the operations as composable
steps the caller sequences explicitly, each producing a labeled adjustment line
so the final invoice is fully auditable:

```go
type Invoice struct { Lines []Line; Subtotal, Total int64; Currency Currency }

type Step interface{ apply(*Invoice) error }  // sealed set

// Steps (each a labeled line on the invoice):
func Charged(c Charge, quantity int64) Step   // rate a charge, add its lines
func PercentOff(pct *big.Rat, label string) Step
func AmountOff(minor int64, label string) Step
func MinimumCharge(floor int64, label string) Step   // top up to floor if below
func DrawCredit(balance *int64, label string) Step   // prepaid credit drawdown, mutates balance
func DrawCommitment(balance *int64, label string) Step

// Compose runs the steps in the given order over an empty invoice.
func Compose(cur Currency, steps ...Step) (Invoice, error)
```

The order is the caller's, and it is *visible* — `Compose(cur, Charged(...),
PercentOff(...), MinimumCharge(...))` discounts before the floor;
reordering the two steps floors before discounting, and the invoice lines record
which happened. tariff's job is that each step is individually correct and
exactly-rounded, not to decide the sequence. Each step is a small, separately
tested unit; the composition is just left-fold over the invoice.

Scope guard: this is invoice *composition*, still not persistence, tax, or
subscription lifecycle. An `Invoice` is a computed value, stored by no one.

### Phase 2 non-goals

- No tax step (data moat, unchanged).
- No automatic order — refusing to pick one is the design.
- No metering — quantities still arrive pre-aggregated.

## Phase 2 as built

Implemented and shipped: signed `Allocate`; the proration calendar (`Period`,
`Basis`, `Period.Fraction`, `Prorate`, `Change`/`Proration`, `NextBoundary`,
`NextCalendarBoundary`, `CycleUnit`); and interaction-order composition
(`Invoice`, the sealed `Step` set, `Compose`). Exact `*big.Rat` throughout — no
fraction ever crosses `float64` — int64 minor units, one rounding per money
boundary via the phase-1 `currency.round`, zero-dependency, sibling house style.
Combined coverage 98.7%. All golden vectors reproduce exactly: the Stripe
`−$5 / +$10 / $5 net` upgrade to the second, and the Chargebee day-based
`credit −$16 / charge $32 / net $16` over a 31-day term.

**Part A — signed allocation (the phase-1 limitation closed).** `Allocate` and
the internal `allocate`/`allocateRat` now accept a negative total. It is split
on its magnitude and the sign is reattached to every share, so the parts sum
*exactly* to the (negative) total, the negative leftover lands on the same
largest-remainder parts a positive leftover would, and a zero weight still
receives exactly zero. Ratios remain non-negative. Working on the absolute value
in `big.Int` also sidesteps the overflow of negating `math.MinInt64`.

**Month-end boundary rule, stated precisely.** A boundary `k` steps from an
anchor is `time.Date(anchorYear, anchorMonth ± k, D, …)` where `D` is
`min(anchorDay, daysInMonth(targetYear, targetMonth))`. The clamp is always
measured from the **original** anchor day, never a previously-clamped one, so
there is no permanent drift: `Jan 31 → Feb 28` (or `Feb 29` in a leap year)
`→ Mar 31 → Apr 30 → May 31 → …`, and a `Feb 29` anchor yields `Feb 28` in
common years but returns to `Feb 29` the next leap year. `NextBoundary` returns
the first boundary **strictly after** `from`; it estimates the step count in the
anchor's location (an at-worst undershoot, since the boundary one step earlier
always precedes `from`) and walks forward, so it is O(1) with no decrement pass.

**DST / basis interaction, stated precisely.** `ProrateBySecond` measures real
elapsed nanoseconds, computed from Unix seconds (`nanosBetween`) so it is both
overflow-safe and genuinely real elapsed time: a 23-hour spring-forward day
counts as 23 hours, a 25-hour fall-back day as 25. `ProrateByDay` counts whole
civil days via a serial day-number (`civilDay`, Hinnant's `days_from_civil`)
computed on the civil date in the period's location, so **every** calendar day
counts as exactly one regardless of its wall-clock length — a DST day is one
day, never 23/24 or 25/24. The two bases therefore give different,
individually-correct fractions for the same window (tested at `335/743` by
second vs `14/31` by day across the spring-forward, and `337/721` vs `14/30`
across the fall-back).

**Change / Proration signs.** `remaining = Fraction(at, End, basis)`. `Credit =
round(−oldAmount × remaining)` — the negative exact rounded **once** (taken via
the negated fraction, so a `MinInt64` amount is never itself negated), so under
floor/ceil the credit is one honest rounding of the negative amount rather than
the negation of a separately-rounded positive. `Charge = round(newAmount ×
remaining)`, `Net = Charge + Credit`. `Credit ≤ 0`, `Charge ≥ 0`. Trial→paid is
`oldAmount = 0 ⇒ Credit = 0`. Because `remaining ∈ [0, 1]`, neither prorated
amount can exceed its input in magnitude, so the charge and net arithmetic
cannot overflow — those guards are defensive.

**Credit / commitment drawdown cap, stated precisely.** `DrawCredit` and
`DrawCommitment` are **mechanically identical** — they differ only in the audit
label and intent. The draw is `min(max(runningTotal, 0), balance)`: capped at
the running total (a zero or negative running total draws nothing, so a credit
never *adds* to what is owed) and at the balance (never overdrawn), and never
negative. The draw is subtracted from the running total and from the caller's
balance (mutated in place). A `nil` balance pointer, or a negative balance, is
`ErrBadBalance` — a negative balance is refused rather than silently floored to
zero.

### Corrections and clarifications found in phase-2 build

- **A window outside the period is not an error — it clamps to zero.** The spec
  said `Fraction` should "error if `from > to` or the window is outside the
  period in a way that can't be sane." On implementation, a window disjoint from
  the period is perfectly sane: it covers none of it, so the fraction is exactly
  `0`. The **only** error is `from > to` (an incoherent window). The covered span
  is `[max(from, Start), min(to, End))`; wider-than-period clamps to `1`, empty
  (`from == to`) is `0`.
- **Day basis needs a whole-day period, and floors the window to day
  boundaries.** Two things the spec left open. First, a period spanning **no**
  whole calendar day (e.g. 08:00–20:00 the same date) has a zero day-denominator
  and no day-based fraction — it returns `ErrBadPeriod` (second basis is fine).
  Second, the day count floors each window bound to its civil midnight in the
  period's location, which makes `used(Start, at) + remaining(at, End) = term`
  hold **exactly** for any `at`, with the partial change-day attributed to the
  remaining (new-plan) side. This is the deliberate, documented disambiguation
  of "counts calendar days."
- **`PercentOff` takes a fraction, not a whole percent.** The sketch wrote
  `PercentOff(pct *big.Rat, …)` with examples like "10%". `pct` is the fraction
  of the running total to remove — `big.NewRat(1, 10)` is 10% — validated to
  `[0, 1]`; a nil, negative, or above-one percentage is `ErrBadDiscount`. The
  discount is the exact `runningTotal × pct` rounded once via the currency.
- **`Charged` guards the invoice currency.** Not in the sketch: a `Charged`
  step whose charge currency does not share the invoice currency's code and
  minor-unit scale is `ErrCurrencyMismatch`, since summing incomparable minor
  units would silently corrupt the invoice. The rounding **mode** may differ —
  per-charge rounding is a legitimate caller choice.
- **A zero-effect step adds no line.** `PercentOff`/`AmountOff` of zero, a
  `MinimumCharge` at or above the floor, and an exhausted draw each leave the
  invoice unchanged and append nothing, keeping the lines free of `0`-value
  clutter while preserving `sum(lines) == Total`.
- **`Invoice` gains an audit `Subtotal`; `Line` gains a `Label`.** `Subtotal` is
  the gross of the charge lines (before adjustments); `Total` is the net after
  every step. `Line` grew an optional `Label` (empty on rating lines) so charges
  and adjustments share one line type — a keyed-field append, backward
  compatible with phase 1.
