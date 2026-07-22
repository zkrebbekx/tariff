package tariff

import (
	"fmt"
	"math/big"
)

// Invoice is a computed invoice: the currency it is priced in, the ordered
// adjustment and charge lines, the gross Subtotal from the charges, and the
// running Total after every step. It is a value, computed by [Compose] and
// stored by no one — composition is not persistence.
//
// The line subtotals always sum exactly to Total: every step appends the exact
// line it applied and moves Total by that same amount.
type Invoice struct {
	// Currency fixes the minor-unit scale and the rounding mode used by the
	// discount steps.
	Currency Currency
	// Lines are the invoice lines in application order: charge lines followed
	// or interleaved with labeled adjustment lines.
	Lines []Line
	// Subtotal is the sum of the charge lines — the gross before adjustments.
	Subtotal int64
	// Total is the net amount after every step, in minor units. It equals the
	// sum of all line subtotals.
	Total int64

	// draws holds the caller-balance decrements a successful compose will apply.
	// A draw step records its mutation here rather than performing it, so a
	// Compose that later errors — and returns this invoice discarded — leaves
	// every caller balance untouched. Committed and cleared by Compose only
	// after the whole fold succeeds.
	draws []pendingDraw
}

// pendingDraw is a deferred decrement of a caller-owned balance.
type pendingDraw struct {
	balance *int64
	amount  int64
}

// Step is one operation in an invoice composition: a charge, a discount, a
// minimum, or a credit or commitment draw. The set is sealed — only this
// package implements it — so [Compose] over the constructors below is the only
// way to build an [Invoice]. Each step appends the labeled line(s) it applies
// and moves the running total by exactly that amount.
type Step interface {
	apply(*Invoice) error
}

// Compose folds the steps left to right over an empty invoice in the given
// order, returning the resulting [Invoice]. The order is the caller's and it is
// visible in the result: composing a percentage discount before a minimum
// charge floors after discounting, and swapping the two floors before
// discounting — tariff computes each order faithfully rather than imposing one.
//
// The currency is validated up front; a nil step is [ErrNilStep]; any step's
// own validation error (a bad discount, floor, balance, or a charge whose
// currency does not match the invoice) propagates unwrapped-through as a
// sentinel matchable with [errors.Is].
func Compose(cur Currency, steps ...Step) (Invoice, error) {
	if err := cur.validate(); err != nil {
		return Invoice{}, err
	}
	inv := Invoice{Currency: cur}
	for i, s := range steps {
		if s == nil {
			return Invoice{}, fmt.Errorf("%w: step %d", ErrNilStep, i)
		}
		if err := s.apply(&inv); err != nil {
			// Nothing is committed: the draws recorded so far are never applied,
			// so a failed compose leaves every caller balance as it found it.
			return Invoice{}, err
		}
	}
	// The whole fold succeeded — now, and only now, decrement the caller
	// balances the draw steps recorded, then drop the record from the result.
	for _, d := range inv.draws {
		*d.balance -= d.amount
	}
	inv.draws = nil
	return inv, nil
}

// addLine appends a labeled line and moves the running total by its subtotal,
// guarding the running total against int64 overflow.
func (inv *Invoice) addLine(l Line) error {
	total, err := addInt64(inv.Total, l.Subtotal)
	if err != nil {
		return err
	}
	inv.Lines = append(inv.Lines, l)
	inv.Total = total
	return nil
}

// chargedStep rates a phase-1 [Charge] and appends its lines.
type chargedStep struct {
	charge   Charge
	quantity int64
}

// Charged rates the charge for the given quantity and appends its line items to
// the invoice, adding its total to both the gross subtotal and the running
// total. The charge's currency must share the invoice currency's code and
// minor-unit scale (its rounding mode may differ — that is a legitimate
// per-charge choice); otherwise [Compose] returns [ErrCurrencyMismatch].
func Charged(c Charge, quantity int64) Step { return chargedStep{charge: c, quantity: quantity} }

func (s chargedStep) apply(inv *Invoice) error {
	if s.charge.Currency.Code != inv.Currency.Code || s.charge.Currency.Decimals != inv.Currency.Decimals {
		return fmt.Errorf("%w: charge %s/%ddp vs invoice %s/%ddp",
			ErrCurrencyMismatch, s.charge.Currency.Code, s.charge.Currency.Decimals,
			inv.Currency.Code, inv.Currency.Decimals)
	}
	res, err := s.charge.Rate(s.quantity)
	if err != nil {
		return err
	}
	subtotal, err := addInt64(inv.Subtotal, res.Total)
	if err != nil {
		return err
	}
	for _, l := range res.Lines {
		if err := inv.addLine(l); err != nil {
			return err
		}
	}
	inv.Subtotal = subtotal
	return nil
}

// percentOffStep removes a fraction of the running total.
type percentOffStep struct {
	pct   *big.Rat
	label string
}

// PercentOff removes pct of the running total as a discount, rounded once with
// the invoice currency's rounding mode. pct is a fraction, not a whole percent:
// big.NewRat(1, 10) is 10% off. It must be non-nil and in [0, 1]; a nil,
// negative or above-one pct is [ErrBadDiscount].
func PercentOff(pct *big.Rat, label string) Step { return percentOffStep{pct: pct, label: label} }

func (s percentOffStep) apply(inv *Invoice) error {
	if s.pct == nil {
		return fmt.Errorf("%w: nil percentage", ErrBadDiscount)
	}
	if s.pct.Sign() < 0 || s.pct.Cmp(big.NewRat(1, 1)) > 0 {
		return fmt.Errorf("%w: percentage %s not in [0, 1]", ErrBadDiscount, s.pct.RatString())
	}
	// A percentage of a zero-or-negative running total is not a discount: scaling
	// a negative total by a positive fraction would append a positive line — a
	// surcharge wearing a discount's label. There is nothing to take off, so
	// no-op, matching how a credit draw skips a non-positive total.
	if inv.Total <= 0 {
		return nil
	}
	// Exact fraction of the running total, rounded once via the currency.
	exact := new(big.Rat).SetInt64(inv.Total)
	exact.Mul(exact, s.pct)
	discount, err := inv.Currency.round(exact)
	if err != nil {
		return err
	}
	if discount == 0 {
		return nil
	}
	return inv.addLine(Line{Label: s.label, Subtotal: -discount})
}

// amountOffStep removes a fixed amount.
type amountOffStep struct {
	minor int64
	label string
}

// AmountOff removes a fixed amount of minor units from the running total as a
// discount. The amount must be non-negative; a negative amount is
// [ErrBadDiscount]. It may drive the running total below zero — pair it with a
// [MinimumCharge] if a floor is wanted.
func AmountOff(minor int64, label string) Step { return amountOffStep{minor: minor, label: label} }

func (s amountOffStep) apply(inv *Invoice) error {
	if s.minor < 0 {
		return fmt.Errorf("%w: negative amount off %d", ErrBadDiscount, s.minor)
	}
	if s.minor == 0 {
		return nil
	}
	return inv.addLine(Line{Label: s.label, Subtotal: -s.minor})
}

// minimumChargeStep tops the running total up to a floor.
type minimumChargeStep struct {
	floor int64
	label string
}

// MinimumCharge tops the running total up to floor if it is currently below it,
// as a single positive adjustment line; if the running total is already at or
// above the floor it does nothing. The floor must be non-negative; a negative
// floor is [ErrBadFloor].
func MinimumCharge(floor int64, label string) Step {
	return minimumChargeStep{floor: floor, label: label}
}

func (s minimumChargeStep) apply(inv *Invoice) error {
	if s.floor < 0 {
		return fmt.Errorf("%w: negative floor %d", ErrBadFloor, s.floor)
	}
	if inv.Total >= s.floor {
		return nil
	}
	topUp, err := subInt64(s.floor, inv.Total)
	if err != nil {
		return err
	}
	return inv.addLine(Line{Label: s.label, Subtotal: topUp})
}

// drawStep draws a caller-owned balance down against the running total. It backs
// both DrawCredit and DrawCommitment, which are mechanically identical — a
// draw capped at the running total and the balance, never negative — differing
// only in the audit label the caller gives them.
type drawStep struct {
	balance *int64
	label   string
	kind    string
}

// DrawCredit draws a prepaid credit balance down against the running total,
// mutating the caller's balance. The draw is capped at both the running total
// and the balance and is never negative, so the running total cannot go below
// zero from a credit and the balance cannot be overdrawn. A nil pointer or a
// negative balance is [ErrBadBalance].
func DrawCredit(balance *int64, label string) Step {
	return drawStep{balance: balance, label: label, kind: "credit"}
}

// DrawCommitment draws a spend-commitment balance down against the running
// total, with the same capping and mutation semantics as [DrawCredit]; the two
// differ only in intent and the audit label. A nil pointer or a negative
// balance is [ErrBadBalance].
func DrawCommitment(balance *int64, label string) Step {
	return drawStep{balance: balance, label: label, kind: "commitment"}
}

func (s drawStep) apply(inv *Invoice) error {
	if s.balance == nil {
		return fmt.Errorf("%w: nil %s balance", ErrBadBalance, s.kind)
	}
	if *s.balance < 0 {
		return fmt.Errorf("%w: negative %s balance %d", ErrBadBalance, s.kind, *s.balance)
	}
	// The available balance is the caller's current value less any draws already
	// pending against the SAME pointer in this compose — because those draws are
	// deferred until Compose commits, the raw pointer still reads its original
	// value, so two draws on one balance must net against each other here or the
	// second would over-draw.
	avail := *s.balance
	for _, d := range inv.draws {
		if d.balance == s.balance {
			avail -= d.amount
		}
	}
	draw := avail
	if inv.Total < draw {
		draw = inv.Total
	}
	if draw <= 0 {
		return nil
	}
	if err := inv.addLine(Line{Label: s.label, Subtotal: -draw}); err != nil {
		return err
	}
	// Record the balance decrement rather than performing it: Compose commits
	// it only if every remaining step also succeeds, so a later error cannot
	// leave the caller's balance drawn against a discarded invoice.
	inv.draws = append(inv.draws, pendingDraw{balance: s.balance, amount: draw})
	return nil
}

// subInt64 subtracts two amounts, reporting [ErrOverflow] rather than wrapping.
func subInt64(a, b int64) (int64, error) {
	z := new(big.Int).Sub(big.NewInt(a), big.NewInt(b))
	if !z.IsInt64() {
		return 0, fmt.Errorf("%w: %d - %d", ErrOverflow, a, b)
	}
	return z.Int64(), nil
}
