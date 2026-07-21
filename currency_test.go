package tariff

import (
	"errors"
	"math/big"
	"testing"
)

func TestRounding(t *testing.T) {
	t.Run("Given exact amounts in minor units and each rounding mode", func(t *testing.T) {
		cases := []struct {
			name  string
			exact *big.Rat
			mode  RoundingMode
			want  int64
		}{
			{"half-up rounds a half away from zero", rat(123, 2), RoundHalfUp, 62}, // 61.5 -> 62
			{"half-up leaves an exact integer", rat(62, 1), RoundHalfUp, 62},
			{"half-up rounds just below a half down", rat(6149, 100), RoundHalfUp, 61}, // 61.49 -> 61
			{"half-up rounds just above a half up", rat(6151, 100), RoundHalfUp, 62},   // 61.51 -> 62
			{"half-even sends 61.5 to the even 62", rat(123, 2), RoundHalfEven, 62},
			{"half-even sends 62.5 to the even 62", rat(125, 2), RoundHalfEven, 62},
			{"half-even sends 63.5 to the even 64", rat(127, 2), RoundHalfEven, 64},
			{"floor truncates toward negative infinity", rat(6199, 100), RoundFloor, 61},
			{"floor of an exact integer is itself", rat(61, 1), RoundFloor, 61},
			{"ceil rounds any remainder up", rat(6101, 100), RoundCeil, 62},
			{"ceil of an exact integer is itself", rat(61, 1), RoundCeil, 61},
			{"half-up on a negative half goes to -1", rat(-1, 2), RoundHalfUp, -1},
			{"half-even on a negative half goes to the even 0", rat(-1, 2), RoundHalfEven, 0},
			{"floor of a negative fraction rounds down", rat(-1, 2), RoundFloor, -1},
			{"ceil of a negative fraction rounds toward zero", rat(-1, 2), RoundCeil, 0},
		}
		for _, tc := range cases {
			t.Run("When "+tc.name, func(t *testing.T) {
				c := Currency{Decimals: 2, Rounding: tc.mode}
				got, err := c.round(new(big.Rat).Set(tc.exact))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tc.want {
					t.Errorf("round(%s, %s) = %d, want %d", tc.exact.RatString(), tc.mode, got, tc.want)
				}
			})
		}
	})

	t.Run("Given an unspecified rounding mode", func(t *testing.T) {
		t.Run("When round is called directly", func(t *testing.T) {
			t.Run("Then it refuses with ErrBadCurrency", func(t *testing.T) {
				c := Currency{Decimals: 2, Rounding: RoundingUnspecified}
				if _, err := c.round(rat(1, 2)); !errors.Is(err, ErrBadCurrency) {
					t.Errorf("error = %v, want ErrBadCurrency", err)
				}
			})
		})
	})
}

func TestCurrencyMinorUnitScale(t *testing.T) {
	t.Run("Given currencies with 2, 0 and 3 decimal places", func(t *testing.T) {
		t.Run("When a rate is applied per-unit", func(t *testing.T) {
			t.Run("Then the minor-unit scale follows the currency, not a hardcoded cent", func(t *testing.T) {
				// USD: 3 * $1.50 = $4.50 -> 450 cents.
				usd := Charge{Model: PerUnit, Currency: USD(RoundHalfUp), UnitRate: rat(3, 2)}
				if r, _ := usd.Rate(3); r.Total != 450 {
					t.Errorf("USD total = %d, want 450", r.Total)
				}

				// JPY (0 decimals): ¥100.5/unit, one unit, half-up -> ¥101; the
				// half-even variant lands on the even ¥100.
				jpyUp := Charge{Model: PerUnit, Currency: JPY(RoundHalfUp), UnitRate: rat(201, 2)}
				if r, _ := jpyUp.Rate(1); r.Total != 101 {
					t.Errorf("JPY half-up total = %d, want 101", r.Total)
				}
				jpyEven := Charge{Model: PerUnit, Currency: JPY(RoundHalfEven), UnitRate: rat(201, 2)}
				if r, _ := jpyEven.Rate(1); r.Total != 100 {
					t.Errorf("JPY half-even total = %d, want 100", r.Total)
				}

				// KWD (3 decimals): 1.2345 dinar/unit, one unit -> 1234.5 fils,
				// half-up -> 1235 fils.
				kwd := Charge{Model: PerUnit, Currency: KWD(RoundHalfUp), UnitRate: rat(12345, 10000)}
				if r, _ := kwd.Rate(1); r.Total != 1235 {
					t.Errorf("KWD total = %d, want 1235", r.Total)
				}
			})
		})
	})
}

func TestCurrencyConstructors(t *testing.T) {
	t.Run("Given the currency constructors", func(t *testing.T) {
		t.Run("Then each carries the right decimal places and rounding", func(t *testing.T) {
			if c := USD(RoundHalfEven); c.Decimals != 2 || c.Code != "USD" || c.Rounding != RoundHalfEven {
				t.Errorf("USD = %+v", c)
			}
			if c := JPY(RoundFloor); c.Decimals != 0 || c.Code != "JPY" {
				t.Errorf("JPY = %+v", c)
			}
			if c := KWD(RoundCeil); c.Decimals != 3 || c.Code != "KWD" {
				t.Errorf("KWD = %+v", c)
			}
		})
	})
}

func TestCurrencyValidate(t *testing.T) {
	t.Run("Given assorted currency configurations", func(t *testing.T) {
		cases := []struct {
			name string
			c    Currency
			ok   bool
		}{
			{"valid USD", USD(RoundHalfUp), true},
			{"valid zero-decimal", JPY(RoundHalfEven), true},
			{"negative decimals", Currency{Decimals: -1, Rounding: RoundHalfUp}, false},
			{"too many decimals", Currency{Decimals: 19, Rounding: RoundHalfUp}, false},
			{"unset rounding", Currency{Decimals: 2}, false},
		}
		for _, tc := range cases {
			t.Run("When validating "+tc.name, func(t *testing.T) {
				err := tc.c.validate()
				if tc.ok && err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if !tc.ok && !errors.Is(err, ErrBadCurrency) {
					t.Errorf("error = %v, want ErrBadCurrency", err)
				}
			})
		}
	})
}

func TestCurrencyFormat(t *testing.T) {
	t.Run("Given minor-unit amounts in different currencies", func(t *testing.T) {
		t.Run("Then Format renders the decimal string", func(t *testing.T) {
			cases := []struct {
				c     Currency
				minor int64
				want  string
			}{
				{USD(RoundHalfUp), 4150, "41.50"},
				{USD(RoundHalfUp), 5, "0.05"},
				{USD(RoundHalfUp), -4150, "-41.50"},
				{USD(RoundHalfUp), 0, "0.00"},
				{JPY(RoundHalfUp), 4150, "4150"},
				{JPY(RoundHalfUp), -20, "-20"},
				{KWD(RoundHalfUp), 4150, "4.150"},
				{KWD(RoundHalfUp), 1235, "1.235"},
			}
			for _, tc := range cases {
				if got := tc.c.Format(tc.minor); got != tc.want {
					t.Errorf("%s.Format(%d) = %q, want %q", tc.c.Code, tc.minor, got, tc.want)
				}
			}
		})
	})
}

func TestRoundingModeString(t *testing.T) {
	t.Run("Given each rounding mode and an unknown one", func(t *testing.T) {
		t.Run("Then String names them", func(t *testing.T) {
			cases := map[RoundingMode]string{
				RoundingUnspecified: "unspecified", RoundHalfUp: "half-up",
				RoundHalfEven: "half-even", RoundFloor: "floor", RoundCeil: "ceil",
				RoundingMode(200): "RoundingMode(200)",
			}
			for m, want := range cases {
				if got := m.String(); got != want {
					t.Errorf("RoundingMode(%d).String() = %q, want %q", m, got, want)
				}
			}
		})
	})
}
