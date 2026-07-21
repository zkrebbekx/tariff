package tariff

import (
	"fmt"
	"math/big"
)

// Tier is one band of a tiered price schedule. Bands are half-open on the low
// side: tier i covers the units in (tiers[i-1].UpTo, tiers[i].UpTo], with the
// first tier covering (0, tiers[0].UpTo]. Exactly one tier — the last — is
// unbounded, marked with Last, so that every quantity is covered.
//
// Which fields matter depends on the charge's model. Graduated and volume read
// UnitRate; stairstep reads FlatRate. UpTo is ignored on the last (unbounded)
// tier.
type Tier struct {
	// UpTo is the inclusive upper bound of this band, in units. It is ignored
	// when Last is true.
	UpTo int64
	// Last marks the final, unbounded band. It must be true for the last tier
	// and false for every other.
	Last bool
	// UnitRate is the exact per-unit price within this band, used by the
	// graduated and volume models.
	UnitRate *big.Rat
	// FlatRate is the flat fee, in minor units, for landing in this band, used
	// by the stairstep model.
	FlatRate int64
}

// validateTiers checks that a tier schedule is well formed. needUnitRate
// requires every tier to carry a non-negative UnitRate (graduated, volume);
// needFlat requires every tier's FlatRate to be non-negative (stairstep).
func validateTiers(tiers []Tier, needUnitRate, needFlat bool) error {
	if len(tiers) == 0 {
		return ErrEmptyTiers
	}
	last := len(tiers) - 1
	for i, t := range tiers {
		isFinal := i == last
		if t.Last != isFinal {
			if t.Last {
				return fmt.Errorf("%w: tier %d is unbounded but not last", ErrTierOrder, i)
			}
			return fmt.Errorf("%w: final tier %d must be unbounded (set Last)", ErrTierOrder, i)
		}
		if !t.Last {
			if t.UpTo <= 0 {
				return fmt.Errorf("%w: tier %d upper bound %d must be positive", ErrTierOrder, i, t.UpTo)
			}
			if i > 0 && t.UpTo <= tiers[i-1].UpTo {
				return fmt.Errorf("%w: tier %d upper bound %d does not exceed previous %d",
					ErrTierOrder, i, t.UpTo, tiers[i-1].UpTo)
			}
		}
		if needUnitRate {
			if t.UnitRate == nil {
				return fmt.Errorf("%w: tier %d has no unit rate", ErrNoRate, i)
			}
			if t.UnitRate.Sign() < 0 {
				return fmt.Errorf("%w: tier %d unit rate is negative", ErrNoRate, i)
			}
		}
		if needFlat && t.FlatRate < 0 {
			return fmt.Errorf("%w: tier %d flat rate %d is negative", ErrNoRate, i, t.FlatRate)
		}
	}
	return nil
}

// landingTier returns the single tier a whole quantity falls in, for the volume
// and stairstep models: the first bounded tier whose UpTo is at least q, or the
// unbounded last tier. Callers pass a validated schedule and q >= 1.
func landingTier(tiers []Tier, q int64) Tier {
	for _, t := range tiers {
		if t.Last || q <= t.UpTo {
			return t
		}
	}
	return tiers[len(tiers)-1]
}
