package tariff

import (
	"errors"
	"math/big"
	"testing"
)

// FuzzAllocate asserts the two properties that make allocation penny-safe: the
// parts always sum exactly to the total, and the split is deterministic.
func FuzzAllocate(f *testing.F) {
	f.Add(int64(100), int64(1), int64(1), int64(1))
	f.Add(int64(7), int64(2), int64(3), int64(5))
	f.Add(int64(0), int64(0), int64(0), int64(0))
	f.Add(int64(9999), int64(11), int64(0), int64(3))

	f.Fuzz(func(t *testing.T, total, a, b, c int64) {
		if total < 0 || a < 0 || b < 0 || c < 0 {
			t.Skip() // Allocate's contract is non-negative inputs.
		}
		ratios := []int64{a, b, c}
		got, err := Allocate(total, ratios)
		if err != nil {
			t.Fatalf("unexpected error for non-negative inputs: %v", err)
		}
		var sum int64
		for _, p := range got {
			if p < 0 {
				t.Fatalf("negative part in %v", got)
			}
			sum += p
		}
		if sum != total {
			t.Fatalf("parts %v sum to %d, want %d", got, sum, total)
		}
		again, err := Allocate(total, ratios)
		if err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if got[i] != again[i] {
				t.Fatalf("non-deterministic: %v vs %v", got, again)
			}
		}
	})
}

// FuzzRateNoPanic asserts that Rate never panics on arbitrary quantity and tier
// inputs, only ever returning a value or one of the documented sentinel errors,
// and that any successful result has line items that reconcile to the total.
func FuzzRateNoPanic(f *testing.F) {
	f.Add(int64(6), int64(5), int64(7), int64(6))
	f.Add(int64(-1), int64(0), int64(-3), int64(0))
	f.Add(int64(1<<40), int64(1), int64(1000000), int64(1))

	f.Fuzz(func(t *testing.T, quantity, bound, num1, num2 int64) {
		var r1, r2 *big.Rat
		if num1 != 0 || bound != 0 { // leave a nil-rate path reachable too
			r1 = big.NewRat(num1, 1)
		}
		r2 = big.NewRat(num2, 1)
		c := Charge{
			Model:    Graduated,
			Currency: USD(RoundHalfUp),
			Tiers: []Tier{
				{UpTo: bound, UnitRate: r1},
				{Last: true, UnitRate: r2},
			},
		}
		res, err := c.Rate(quantity)
		if err != nil {
			// Any error must be one of ours.
			switch {
			case errors.Is(err, ErrNegativeQuantity),
				errors.Is(err, ErrTierOrder),
				errors.Is(err, ErrNoRate),
				errors.Is(err, ErrOverflow):
			default:
				t.Fatalf("undocumented error: %v", err)
			}
			return
		}
		var sum int64
		for _, l := range res.Lines {
			sum += l.Subtotal
		}
		if sum != res.Total {
			t.Fatalf("lines sum to %d, want Total %d (q=%d)", sum, res.Total, quantity)
		}
	})
}

// FuzzGraduatedMonotonic asserts that a graduated total is monotonic
// non-decreasing in quantity for a non-decreasing schedule: adding usage never
// lowers the bill.
func FuzzGraduatedMonotonic(f *testing.F) {
	f.Add(int64(3), int64(4))
	f.Add(int64(0), int64(1000))
	f.Add(int64(5), int64(6))

	// A non-decreasing (in fact increasing) schedule with small rates, so no
	// input in the capped range can overflow.
	c := Charge{
		Model:    Graduated,
		Currency: USD(RoundHalfUp),
		Tiers: []Tier{
			{UpTo: 5, UnitRate: big.NewRat(1, 1)},
			{UpTo: 10, UnitRate: big.NewRat(2, 1)},
			{Last: true, UnitRate: big.NewRat(3, 1)},
		},
	}

	clamp := func(v int64) int64 {
		if v < 0 {
			v = -v
		}
		if v < 0 || v > 1_000_000_000_000 { // handles MinInt64 and caps magnitude
			v = 1_000_000_000_000
		}
		return v
	}

	f.Fuzz(func(t *testing.T, x, y int64) {
		lo, hi := clamp(x), clamp(y)
		if lo > hi {
			lo, hi = hi, lo
		}
		rLo, err := c.Rate(lo)
		if err != nil {
			t.Fatalf("unexpected error at %d: %v", lo, err)
		}
		rHi, err := c.Rate(hi)
		if err != nil {
			t.Fatalf("unexpected error at %d: %v", hi, err)
		}
		if rLo.Total > rHi.Total {
			t.Fatalf("non-monotonic: Rate(%d)=%d > Rate(%d)=%d", lo, rLo.Total, hi, rHi.Total)
		}
	})
}
