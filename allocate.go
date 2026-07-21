package tariff

import (
	"fmt"
	"math/big"
	"sort"
)

// Allocate splits a whole total across len(ratios) parts in proportion to the
// given ratios, losing nothing. Each part receives the floor of its exact
// share, then the leftover minor units — always fewer than the number of parts
// — are handed to the parts whose exact share had the largest fractional
// remainder (the largest-remainder, or Hamilton, method), ties broken by
// position. The result sums exactly to total for any ratios and any remainder,
// is deterministic, and — unlike a round-robin-from-the-first split — never
// hands a penny to a part that did not round up: a zero ratio receives zero,
// and a part whose exact share is already whole keeps it.
//
// This is the penny-safe remainder split that makes line-item reconciliation
// (and, later, proration) exact, without misrepresenting what any one line
// charged. tariff uses the same routine internally to distribute a rounded
// tiered total back across its tier lines. If every ratio is zero the split is
// made evenly.
//
// total and every ratio must be non-negative; otherwise Allocate returns an
// error wrapping [ErrBadAllocation].
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
// rational-weighted reconciliation in allocateRat.
func allocate(total int64, weights []*big.Int) ([]int64, error) {
	n := len(weights)
	if n == 0 {
		return nil, fmt.Errorf("%w: no parts", ErrBadAllocation)
	}
	if total < 0 {
		return nil, fmt.Errorf("%w: total %d is negative", ErrBadAllocation, total)
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

	out := make([]int64, n)
	rem := make([]*big.Int, n)
	bigTotal := big.NewInt(total)
	allocated := int64(0)
	for i, w := range eff {
		// share = floor(total * w / sum), rem = (total * w) mod sum. Since
		// 0 <= w <= sum and total fits an int64, the quotient is in [0, total]
		// and always fits an int64 too.
		num := new(big.Int).Mul(bigTotal, w)
		share := new(big.Int)
		r := new(big.Int)
		share.QuoRem(num, sum, r)
		out[i] = share.Int64()
		rem[i] = r
		allocated += out[i]
	}

	// The leftover is total minus the summed floors: an integer in [0, n).
	// Hand each unit to the part with the next-largest fractional remainder,
	// ties broken by position — the largest-remainder method. The count of
	// parts with a non-zero remainder is always at least the leftover, so a
	// zero-remainder part (a zero weight, or an exactly-whole share) never
	// receives one.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return rem[order[a]].Cmp(rem[order[b]]) > 0
	})
	for k := int64(0); k < total-allocated; k++ {
		out[order[k]]++
	}
	return out, nil
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
