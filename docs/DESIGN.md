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
rounding; the round-robin allocator; ≥95% coverage (99.6%), zero-dependency,
sibling house style. All golden vectors reproduce exactly.

Resolutions and refinements against the sketch above:

- **Open question 2 (line-item reconciliation) — resolved by allocation.**
  A graduated charge rates each tier exactly, sums exactly, rounds the total
  **once**, then allocates the rounded total back across the tier lines with the
  round-robin allocator, so `sum(lines) == Total` exactly. The golden test
  `TestGraduatedReconciliation` uses three tiers of $0.105 / $0.205 / $0.305:
  independent half-up per-line rounding drifts to 63c, the once-rounded total is
  62c, and allocation yields lines `11 + 21 + 30 = 62`.
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
