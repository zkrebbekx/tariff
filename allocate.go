package tariff

import (
	"fmt"
	"math/big"
)

// Allocate splits a whole total across len(ratios) parts in proportion to the
// given ratios, losing nothing. Each part receives the floor of its exact
// share, then the leftover minor units — always fewer than the number of parts
// — are handed out one each, round-robin from the first part. The result
// therefore sums exactly to total for any ratios and any remainder, and is
// deterministic.
//
// This is the penny-safe remainder split that makes line-item reconciliation
// (and, later, proration) exact. tariff uses the same routine internally to
// distribute a rounded tiered total back across its tier lines. If every ratio
// is zero the split is made evenly.
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

// allocate is the shared core: a round-robin remainder split of total across
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
	bigTotal := big.NewInt(total)
	allocated := int64(0)
	for i, w := range eff {
		// share = floor(total * w / sum). Since 0 <= w <= sum and total fits an
		// int64, the quotient is in [0, total] and always fits an int64 too.
		share := new(big.Int).Mul(bigTotal, w)
		share.Div(share, sum)
		out[i] = share.Int64()
		allocated += out[i]
	}

	// The leftover is total minus the summed floors: an integer in [0, n).
	// Hand it out one minor unit at a time from the first part.
	for i := int64(0); i < total-allocated; i++ {
		out[i]++
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
