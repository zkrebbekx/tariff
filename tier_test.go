package tariff

import (
	"errors"
	"math/big"
	"testing"
)

func TestValidateTiers(t *testing.T) {
	t.Run("Given assorted tier schedules", func(t *testing.T) {
		cases := []struct {
			name         string
			tiers        []Tier
			needUnitRate bool
			needFlat     bool
			want         error // nil means valid
		}{
			{
				name: "well-formed graduated schedule", needUnitRate: true,
				tiers: []Tier{{UpTo: 5, UnitRate: rat(7, 1)}, {Last: true, UnitRate: rat(6, 1)}},
			},
			{
				name: "single unbounded tier", needUnitRate: true,
				tiers: []Tier{{Last: true, UnitRate: rat(6, 1)}},
			},
			{
				name: "empty schedule", needUnitRate: true, want: ErrEmptyTiers,
			},
			{
				name: "non-final tier marked unbounded", needUnitRate: true, want: ErrTierOrder,
				tiers: []Tier{{Last: true, UnitRate: rat(1, 1)}, {Last: true, UnitRate: rat(1, 1)}},
			},
			{
				name: "final tier not marked unbounded", needUnitRate: true, want: ErrTierOrder,
				tiers: []Tier{{UpTo: 5, UnitRate: rat(1, 1)}, {UpTo: 10, UnitRate: rat(1, 1)}},
			},
			{
				name: "non-positive upper bound", needUnitRate: true, want: ErrTierOrder,
				tiers: []Tier{{UpTo: 0, UnitRate: rat(1, 1)}, {Last: true, UnitRate: rat(1, 1)}},
			},
			{
				name: "bounds not strictly increasing (overlap)", needUnitRate: true, want: ErrTierOrder,
				tiers: []Tier{{UpTo: 10, UnitRate: rat(1, 1)}, {UpTo: 10, UnitRate: rat(1, 1)}, {Last: true, UnitRate: rat(1, 1)}},
			},
			{
				name: "bounds out of order", needUnitRate: true, want: ErrTierOrder,
				tiers: []Tier{{UpTo: 10, UnitRate: rat(1, 1)}, {UpTo: 5, UnitRate: rat(1, 1)}, {Last: true, UnitRate: rat(1, 1)}},
			},
			{
				name: "missing unit rate", needUnitRate: true, want: ErrNoRate,
				tiers: []Tier{{Last: true}},
			},
			{
				name: "negative unit rate", needUnitRate: true, want: ErrNoRate,
				tiers: []Tier{{Last: true, UnitRate: rat(-1, 1)}},
			},
			{
				name: "negative flat rate", needFlat: true, want: ErrNoRate,
				tiers: []Tier{{Last: true, FlatRate: -1}},
			},
			{
				name: "valid stairstep flat schedule", needFlat: true,
				tiers: []Tier{{UpTo: 10, FlatRate: 100}, {Last: true, FlatRate: 200}},
			},
		}
		for _, tc := range cases {
			t.Run("When validating a "+tc.name, func(t *testing.T) {
				err := validateTiers(tc.tiers, tc.needUnitRate, tc.needFlat)
				if tc.want == nil {
					if err != nil {
						t.Errorf("unexpected error: %v", err)
					}
					return
				}
				if !errors.Is(err, tc.want) {
					t.Errorf("error = %v, want %v", err, tc.want)
				}
			})
		}
	})
}

func TestLandingTier(t *testing.T) {
	tiers := []Tier{
		{UpTo: 5, UnitRate: rat(1, 1)},
		{UpTo: 10, UnitRate: rat(2, 1)},
		{Last: true, UnitRate: rat(3, 1)},
	}

	t.Run("Given a bounded-then-unbounded schedule", func(t *testing.T) {
		t.Run("When a quantity lands on and across boundaries", func(t *testing.T) {
			t.Run("Then it selects the tier whose band contains it", func(t *testing.T) {
				cases := []struct {
					q    int64
					want *big.Rat
				}{
					{1, rat(1, 1)}, {5, rat(1, 1)}, // first band, incl. its upper bound
					{6, rat(2, 1)}, {10, rat(2, 1)}, // second band
					{11, rat(3, 1)}, {9999, rat(3, 1)}, // unbounded band
				}
				for _, tc := range cases {
					if got := landingTier(tiers, tc.q); got.UnitRate.Cmp(tc.want) != 0 {
						t.Errorf("landingTier(q=%d) rate = %v, want %v", tc.q, got.UnitRate, tc.want)
					}
				}
			})
		})
	})

	t.Run("Given a schedule with no unbounded final tier", func(t *testing.T) {
		// validateTiers rejects this, but landingTier must still terminate
		// safely if reached with an out-of-range quantity.
		capped := []Tier{{UpTo: 5, UnitRate: rat(1, 1)}, {UpTo: 10, UnitRate: rat(2, 1)}}

		t.Run("When a quantity exceeds every band", func(t *testing.T) {
			t.Run("Then it falls back to the last tier", func(t *testing.T) {
				if got := landingTier(capped, 999); got.UnitRate.Cmp(rat(2, 1)) != 0 {
					t.Errorf("fallback rate = %v, want 2", got.UnitRate)
				}
			})
		})
	})
}
