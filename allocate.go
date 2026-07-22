package tariff

import (
	"fmt"
	"math/big"
	"sort"
)

// Allocate splits a total across len(ratios) parts in proportion to the given
// ratios, losing nothing. Each part receives the floor (toward zero) of its
// exact share, then the leftover minor units — always fewer than the number of
// parts — are handed to the parts whose exact share had the largest fractional
// remainder (the largest-remainder, or Hamilton, method), ties broken by
// position. The result sums exactly to total for any ratios and any remainder,
// is deterministic, and — unlike a round-robin-from-the-first split — never
// hands a penny to a part that did not round up: a zero ratio receives zero,
// and a part whose exact share is already whole keeps it.
//
// The total may be negative: a proration credit is a negative amount split
// across lines. A negative total is allocated as the exact mirror of its
// magnitude — every share is non-positive, the sum is exactly the (negative)
// total, and the negative leftover lands on the same largest-remainder parts a
// positive leftover would. A zero ratio still receives exactly zero. (Phase 1
// refused a negative total; that limitation is now lifted.)
//
// This is the penny-safe remainder split that makes line-item reconciliation
// and proration exact, without misrepresenting what any one line charged.
// tariff uses the same routine internally to distribute a rounded tiered total
// back across its tier lines. If every ratio is zero the split is made evenly.
//
// Every ratio must be non-negative; a negative ratio, or no parts at all,
// returns an error wrapping [ErrBadAllocation]. The total is unrestricted in
// sign.
func Allocate(total int64, ratios []int64) ([]int64, error) {
	if len(ratios) == 0 {
		return nil, fmt.Errorf("%w: no parts", ErrBadAllocation)
	}
	weights := make([]*big.Int, len(ratios))
	for i, r := range ratios {
		if r < 0 {
			return nil, fmt.Errorf("%w: ratio %d is negative", ErrBadAllocation, r)
		}
		weights[i] = big.NewInt(r)
	}
	return allocate(total, weights)
}

// allocate is the shared core: a largest-remainder split of total across
// non-negative big.Int weights. It underpins both the public [Allocate] and the
// rational-weighted reconciliation in allocateRat. The total may be negative —
// it is split on its magnitude and the sign is carried back onto every share —
// so a proration credit divides across lines exactly.
func allocate(total int64, weights []*big.Int) ([]int64, error) {
	n := len(weights)
	if n == 0 {
		return nil, fmt.Errorf("%w: no parts", ErrBadAllocation)
	}

	sum := new(big.Int)
	for _, w := range weights {
		if w.Sign() < 0 {
			return nil, fmt.Errorf("%w: negative weight", ErrBadAllocation)
		}
		sum.Add(sum, w)
	}

	// Degenerate all-zero weights: fall back to an even split so nothing is
	// lost and the result is still deterministic.
	eff := weights
	if sum.Sign() == 0 {
		eff = make([]*big.Int, n)
		for i := range eff {
			eff[i] = big.NewInt(1)
		}
		sum = big.NewInt(int64(n))
	}

	// Split on the magnitude of the total, then reattach the sign at the end.
	// A negative total is the exact mirror of its magnitude: the negative
	// leftover lands on the same largest-remainder parts, and a zero weight
	// still gets zero. Working on the absolute value in big.Int also sidesteps
	// the int64 overflow that negating math.MinInt64 would hit.
	neg := total < 0
	absTotal := new(big.Int).Abs(big.NewInt(total))

	out := make([]*big.Int, n)
	rem := make([]*big.Int, n)
	allocated := new(big.Int)
	for i, w := range eff {
		// share = floor(absTotal * w / sum), rem = (absTotal * w) mod sum.
		num := new(big.Int).Mul(absTotal, w)
		share := new(big.Int)
		r := new(big.Int)
		share.QuoRem(num, sum, r)
		out[i] = share
		rem[i] = r
		allocated.Add(allocated, share)
	}

	// The leftover is the magnitude minus the summed floors: an integer in
	// [0, n). Hand each unit to the part with the next-largest fractional
	// remainder, ties broken by position — the largest-remainder method. The
	// count of parts with a non-zero remainder is always at least the leftover,
	// so a zero-remainder part (a zero weight, or an exactly-whole share) never
	// receives one.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return rem[order[a]].Cmp(rem[order[b]]) > 0
	})
	leftover := new(big.Int).Sub(absTotal, allocated).Int64() // in [0, n)
	one := big.NewInt(1)
	for k := int64(0); k < leftover; k++ {
		out[order[k]].Add(out[order[k]], one)
	}

	result := make([]int64, n)
	for i, share := range out {
		if neg {
			share = new(big.Int).Neg(share) // negate before Int64 so MinInt64 fits
		}
		result[i] = share.Int64()
	}
	return result, nil
}

// allocateRat distributes a rounded integer total across parts weighted by
// exact rational amounts — the tier subtotals of a graduated charge. It scales
// the rationals onto a common denominator so their integer numerators preserve
// the exact proportions, then defers to allocate.
func allocateRat(total int64, weights []*big.Rat) ([]int64, error) {
	commonDen := big.NewInt(1)
	for _, w := range weights {
		commonDen.Mul(commonDen, w.Denom())
	}
	ints := make([]*big.Int, len(weights))
	for i, w := range weights {
		scaled := new(big.Int).Mul(w.Num(), commonDen)
		scaled.Div(scaled, w.Denom())
		ints[i] = scaled
	}
	return allocate(total, ints)
}
