package tariff

import (
	"errors"
	"math"
	"math/big"
	"testing"
)

// rat is a terse constructor for an exact rate.
func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

// stripeTiers is the schedule pinned in the design doc: 1-5 @ $7, 6-10 @ $6.50,
// 11+ @ $6.
func stripeTiers() []Tier {
	return []Tier{
		{UpTo: 5, UnitRate: rat(7, 1)},
		{UpTo: 10, UnitRate: rat(13, 2)},
		{Last: true, UnitRate: rat(6, 1)},
	}
}

type wantLine struct {
	quantity int64
	subtotal int64
	rate     *big.Rat // nil means the line is expected to carry no rate
}

func checkResult(t *testing.T, got Result, wantTotal int64, want []wantLine) {
	t.Helper()
	if got.Total != wantTotal {
		t.Errorf("Total = %d, want %d", got.Total, wantTotal)
	}
	var sum int64
	for _, l := range got.Lines {
		sum += l.Subtotal
	}
	if sum != got.Total {
		t.Errorf("line subtotals sum to %d but Total is %d — lines must reconcile", sum, got.Total)
	}
	if len(got.Lines) != len(want) {
		t.Fatalf("got %d lines, want %d: %+v", len(got.Lines), len(want), got.Lines)
	}
	for i, w := range want {
		gl := got.Lines[i]
		if gl.Quantity != w.quantity {
			t.Errorf("line %d Quantity = %d, want %d", i, gl.Quantity, w.quantity)
		}
		if gl.Subtotal != w.subtotal {
			t.Errorf("line %d Subtotal = %d, want %d", i, gl.Subtotal, w.subtotal)
		}
		switch {
		case w.rate == nil && gl.Rate != nil:
			t.Errorf("line %d Rate = %v, want nil", i, gl.Rate)
		case w.rate != nil && gl.Rate == nil:
			t.Errorf("line %d Rate = nil, want %v", i, w.rate)
		case w.rate != nil && gl.Rate.Cmp(w.rate) != 0:
			t.Errorf("line %d Rate = %v, want %v", i, gl.Rate, w.rate)
		}
	}
}

func TestGraduated(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given the Stripe graduated schedule", func(t *testing.T) {
		c := Charge{Model: Graduated, Currency: usd, Tiers: stripeTiers()}

		t.Run("When quantity 6 is rated", func(t *testing.T) {
			t.Run("Then the total is $41.50 across two tier lines", func(t *testing.T) {
				res, err := c.Rate(6)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 4150, []wantLine{
					{quantity: 5, subtotal: 3500, rate: rat(7, 1)},
					{quantity: 1, subtotal: 650, rate: rat(13, 2)},
				})
			})
		})

		t.Run("When quantity 5 lands exactly on the first tier boundary", func(t *testing.T) {
			t.Run("Then only the first tier is charged, $35.00", func(t *testing.T) {
				res, err := c.Rate(5)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 3500, []wantLine{{quantity: 5, subtotal: 3500, rate: rat(7, 1)}})
			})
		})

		t.Run("When quantity 11 crosses into the unbounded tier", func(t *testing.T) {
			// The design prompt's $71.50 is arithmetically wrong: 5*$7 +
			// 5*$6.50 + 1*$6 = $73.50. This test pins the correct value.
			t.Run("Then the total is $73.50 across three tier lines", func(t *testing.T) {
				res, err := c.Rate(11)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 7350, []wantLine{
					{quantity: 5, subtotal: 3500, rate: rat(7, 1)},
					{quantity: 5, subtotal: 3250, rate: rat(13, 2)},
					{quantity: 1, subtotal: 600, rate: rat(6, 1)},
				})
			})
		})

		t.Run("When quantity 10 sits on the second tier boundary", func(t *testing.T) {
			t.Run("Then units split 5 and 5 with no spill into the last tier", func(t *testing.T) {
				res, err := c.Rate(10)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 6750, []wantLine{
					{quantity: 5, subtotal: 3500, rate: rat(7, 1)},
					{quantity: 5, subtotal: 3250, rate: rat(13, 2)},
				})
			})
		})

		t.Run("When quantity is zero", func(t *testing.T) {
			t.Run("Then the total is $0 with no lines and no error", func(t *testing.T) {
				res, err := c.Rate(0)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 0, nil)
			})
		})
	})

	t.Run("Given the Lago graduated schedule of $1.00 / $0.50 / $0.10", func(t *testing.T) {
		c := Charge{Model: Graduated, Currency: usd, Tiers: []Tier{
			{UpTo: 100, UnitRate: rat(1, 1)},
			{UpTo: 200, UnitRate: rat(1, 2)},
			{Last: true, UnitRate: rat(1, 10)},
		}}

		t.Run("When 250 units are rated", func(t *testing.T) {
			t.Run("Then the total is $155.00", func(t *testing.T) {
				res, err := c.Rate(250)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 15500, []wantLine{
					{quantity: 100, subtotal: 10000, rate: rat(1, 1)},
					{quantity: 100, subtotal: 5000, rate: rat(1, 2)},
					{quantity: 50, subtotal: 500, rate: rat(1, 10)},
				})
			})
		})
	})
}

func TestGraduatedReconciliation(t *testing.T) {
	// Three tiers whose exact subtotals each end in half a cent: $0.105, $0.205,
	// $0.305, summing to exactly $0.615. Rounding each line independently
	// (half-up) would give 11 + 21 + 31 = 63c, but the correctly rounded total
	// is 62c. Allocation distributes 62c back so the lines reconcile.
	usd := USD(RoundHalfUp)
	c := Charge{Model: Graduated, Currency: usd, Tiers: []Tier{
		{UpTo: 1, UnitRate: rat(21, 200)},    // $0.105
		{UpTo: 2, UnitRate: rat(41, 200)},    // $0.205
		{Last: true, UnitRate: rat(61, 200)}, // $0.305
	}}

	t.Run("Given three tiers that each round to a half-cent remainder", func(t *testing.T) {
		t.Run("When quantity 3 is rated", func(t *testing.T) {
			res, err := c.Rate(3)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			t.Run("Then the once-rounded total is 62 cents", func(t *testing.T) {
				if res.Total != 62 {
					t.Errorf("Total = %d, want 62", res.Total)
				}
			})

			t.Run("Then naive independent per-line rounding would have drifted to 63", func(t *testing.T) {
				var naive int64
				for _, l := range c.Tiers {
					v, err := usd.round(c.scaledAmount(1, l.UnitRate))
					if err != nil {
						t.Fatal(err)
					}
					naive += v
				}
				if naive != 63 {
					t.Fatalf("independent rounding = %d, expected the drifting 63", naive)
				}
				if naive == res.Total {
					t.Fatal("test is not exercising drift: naive equals reconciled total")
				}
			})

			t.Run("Then the allocated lines sum exactly to the total", func(t *testing.T) {
				checkResult(t, res, 62, []wantLine{
					{quantity: 1, subtotal: 11, rate: rat(21, 200)},
					{quantity: 1, subtotal: 21, rate: rat(41, 200)},
					{quantity: 1, subtotal: 30, rate: rat(61, 200)},
				})
			})
		})
	})
}

func TestVolume(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given the Stripe schedule rated by volume", func(t *testing.T) {
		c := Charge{Model: Volume, Currency: usd, Tiers: stripeTiers()}

		t.Run("When quantity 6 lands in the second tier", func(t *testing.T) {
			t.Run("Then the whole quantity is charged at $6.50 for $39.00", func(t *testing.T) {
				res, err := c.Rate(6)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 3900, []wantLine{{quantity: 6, subtotal: 3900, rate: rat(13, 2)}})
			})
		})

		t.Run("When quantity sits on and just past the tier-2 boundary", func(t *testing.T) {
			t.Run("Then 10 rates at $6.50 and 11 rates at $6.00 (whole quantity)", func(t *testing.T) {
				r10, err := c.Rate(10)
				if err != nil {
					t.Fatal(err)
				}
				r11, err := c.Rate(11)
				if err != nil {
					t.Fatal(err)
				}
				checkResult(t, r10, 6500, []wantLine{{quantity: 10, subtotal: 6500, rate: rat(13, 2)}})
				checkResult(t, r11, 6600, []wantLine{{quantity: 11, subtotal: 6600, rate: rat(6, 1)}})
			})
		})
	})

	t.Run("Given a steeply decreasing schedule ($10 then $1)", func(t *testing.T) {
		// The volume total can DECREASE as usage grows into a cheaper tier. Note
		// the Stripe schedule above does NOT show this at 10->11 (65->66); it
		// needs a steeper drop.
		c := Charge{Model: Volume, Currency: usd, Tiers: []Tier{
			{UpTo: 10, UnitRate: rat(10, 1)},
			{Last: true, UnitRate: rat(1, 1)},
		}}

		t.Run("When quantity grows from 10 to 11", func(t *testing.T) {
			t.Run("Then the total falls from $100.00 to $11.00", func(t *testing.T) {
				r10, err := c.Rate(10)
				if err != nil {
					t.Fatal(err)
				}
				r11, err := c.Rate(11)
				if err != nil {
					t.Fatal(err)
				}
				if r10.Total != 10000 {
					t.Errorf("Rate(10).Total = %d, want 10000", r10.Total)
				}
				if r11.Total != 1100 {
					t.Errorf("Rate(11).Total = %d, want 1100", r11.Total)
				}
				if r11.Total >= r10.Total {
					t.Errorf("expected a decrease crossing the boundary: %d !< %d", r11.Total, r10.Total)
				}
			})
		})
	})
}

func TestPackage(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given $5 per 100-unit package with 100 free units", func(t *testing.T) {
		c := Charge{Model: Package, Currency: usd, PackageSize: 100, PackagePrice: 500, FreeAllowance: 100}

		cases := []struct {
			name  string
			qty   int64
			total int64
			blks  int64
		}{
			{"100 units are all free", 100, 0, 0},
			{"101 units round up to one block", 101, 500, 1},
			{"200 units fill exactly one chargeable block", 200, 500, 1},
			{"201 units round the part-block up to two", 201, 1000, 2},
		}
		for _, tc := range cases {
			t.Run("When "+tc.name, func(t *testing.T) {
				res, err := c.Rate(tc.qty)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.blks == 0 {
					checkResult(t, res, 0, nil)
					return
				}
				checkResult(t, res, tc.total, []wantLine{{quantity: tc.blks, subtotal: tc.total}})
			})
		}

		t.Run("Then the free allowance is applied before the ceil, not after", func(t *testing.T) {
			// 201 - 100 = 101 chargeable -> ceil(101/100) = 2 blocks = $10.
			// Ceiling first (ceil(201/100)=3) then subtracting would misprice.
			res, err := c.Rate(201)
			if err != nil {
				t.Fatal(err)
			}
			if res.Total != 1000 {
				t.Errorf("Total = %d, want 1000 (free-before-ceil)", res.Total)
			}
		})
	})
}

func TestPerUnit(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a sub-cent per-unit rate with a flat fee", func(t *testing.T) {
		// Lago's vector: 65000 * $0.0006 = $39.00, plus a $10.00 flat fee = $49.
		c := Charge{Model: PerUnit, Currency: usd, UnitRate: rat(6, 10000), FlatFee: 1000}

		t.Run("When 65000 units are rated", func(t *testing.T) {
			t.Run("Then exact big.Rat arithmetic gives $49.00 with no float drift", func(t *testing.T) {
				res, err := c.Rate(65000)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				checkResult(t, res, 4900, []wantLine{
					{quantity: 65000, subtotal: 3900, rate: rat(6, 10000)},
					{quantity: 0, subtotal: 1000}, // the flat-fee line
				})
			})
		})
	})

	t.Run("Given a rate whose float representation rounds the wrong way", func(t *testing.T) {
		// $1.005 exact is 100.5 cents; half-up gives 101c ($1.01). In float64,
		// 1.005 is stored as 1.00499999..., so the naive float computation
		// 1.005*100 = 100.4999... rounds to 100c ($1.00) — a lost cent.
		c := Charge{Model: PerUnit, Currency: usd, UnitRate: rat(201, 200)}

		t.Run("When one unit is rated", func(t *testing.T) {
			t.Run("Then the exact engine rounds 100.5c up to 101c, not down to 100c", func(t *testing.T) {
				res, err := c.Rate(1)
				if err != nil {
					t.Fatal(err)
				}
				if res.Total != 101 {
					t.Errorf("Total = %d, want 101 (float drift would give 100)", res.Total)
				}
				dollars := 1.005                       // a runtime float64, not a constant
				floatCents := int64(dollars*100 + 0.5) // the naive, wrong computation
				if floatCents != 100 {
					t.Fatalf("expected the float path to lose a cent at 100, got %d", floatCents)
				}
				if floatCents == res.Total {
					t.Fatalf("float path also gave %d; the drift demonstration is not firing", floatCents)
				}
			})
		})
	})
}

func TestStairstep(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given flat fees of $100 / $180 / $250 per band", func(t *testing.T) {
		c := Charge{Model: Stairstep, Currency: usd, Tiers: []Tier{
			{UpTo: 10, FlatRate: 10000},
			{UpTo: 20, FlatRate: 18000},
			{Last: true, FlatRate: 25000},
		}}

		t.Run("When a mid-band quantity of 15 is rated", func(t *testing.T) {
			t.Run("Then the band's flat $180.00 is charged regardless of position", func(t *testing.T) {
				res, err := c.Rate(15)
				if err != nil {
					t.Fatal(err)
				}
				checkResult(t, res, 18000, []wantLine{{quantity: 15, subtotal: 18000}})
			})
		})

		t.Run("When a quantity lands in the unbounded top band", func(t *testing.T) {
			t.Run("Then the top flat fee applies", func(t *testing.T) {
				res, err := c.Rate(999)
				if err != nil {
					t.Fatal(err)
				}
				checkResult(t, res, 25000, []wantLine{{quantity: 999, subtotal: 25000}})
			})
		})
	})
}

func TestFreeAllowance(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a free allowance at least as large as the quantity", func(t *testing.T) {
		t.Run("When each model rates a fully-covered quantity", func(t *testing.T) {
			t.Run("Then every model returns $0 with no lines", func(t *testing.T) {
				charges := []Charge{
					{Model: PerUnit, Currency: usd, UnitRate: rat(5, 1), FreeAllowance: 10},
					{Model: Graduated, Currency: usd, Tiers: stripeTiers(), FreeAllowance: 10},
					{Model: Volume, Currency: usd, Tiers: stripeTiers(), FreeAllowance: 10},
					{Model: Stairstep, Currency: usd, Tiers: []Tier{{Last: true, FlatRate: 500}}, FreeAllowance: 10},
					{Model: Package, Currency: usd, PackageSize: 5, PackagePrice: 100, FreeAllowance: 10},
				}
				for _, c := range charges {
					res, err := c.Rate(10)
					if err != nil {
						t.Fatalf("%s: unexpected error: %v", c.Model, err)
					}
					if res.Total != 0 || len(res.Lines) != 0 {
						t.Errorf("%s: got %d/%d lines, want 0/0", c.Model, res.Total, len(res.Lines))
					}
				}
			})
		})
	})

	t.Run("Given a quantity strictly below the free allowance", func(t *testing.T) {
		t.Run("When rated", func(t *testing.T) {
			t.Run("Then the chargeable quantity clamps to zero, not negative", func(t *testing.T) {
				c := Charge{Model: PerUnit, Currency: usd, UnitRate: rat(5, 1), FreeAllowance: 100}
				res, err := c.Rate(30)
				if err != nil {
					t.Fatal(err)
				}
				if res.Total != 0 || len(res.Lines) != 0 {
					t.Errorf("got %d/%d lines, want 0/0", res.Total, len(res.Lines))
				}
			})
		})
	})
}

func TestFlatFeeAppliesWithoutUsage(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a charge with a flat fee and zero usage", func(t *testing.T) {
		c := Charge{Model: PerUnit, Currency: usd, UnitRate: rat(5, 1), FlatFee: 2500}

		t.Run("When quantity 0 is rated", func(t *testing.T) {
			t.Run("Then only the flat fee is charged", func(t *testing.T) {
				res, err := c.Rate(0)
				if err != nil {
					t.Fatal(err)
				}
				checkResult(t, res, 2500, []wantLine{{quantity: 0, subtotal: 2500}})
			})
		})
	})
}

func TestChargeErrors(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given various malformed charges", func(t *testing.T) {
		cases := []struct {
			name string
			c    Charge
			qty  int64
			want error
		}{
			{"negative quantity", Charge{Model: PerUnit, Currency: usd, UnitRate: rat(1, 1)}, -1, ErrNegativeQuantity},
			{"negative free allowance", Charge{Model: PerUnit, Currency: usd, UnitRate: rat(1, 1), FreeAllowance: -1}, 5, ErrBadAllowance},
			{"negative flat fee", Charge{Model: PerUnit, Currency: usd, UnitRate: rat(1, 1), FlatFee: -1}, 5, ErrNoRate},
			{"per-unit missing rate", Charge{Model: PerUnit, Currency: usd}, 5, ErrNoRate},
			{"per-unit negative rate", Charge{Model: PerUnit, Currency: usd, UnitRate: rat(-1, 1)}, 5, ErrNoRate},
			{"graduated empty tiers", Charge{Model: Graduated, Currency: usd}, 5, ErrEmptyTiers},
			{"volume empty tiers", Charge{Model: Volume, Currency: usd}, 5, ErrEmptyTiers},
			{"stairstep empty tiers", Charge{Model: Stairstep, Currency: usd}, 5, ErrEmptyTiers},
			{"package bad size", Charge{Model: Package, Currency: usd, PackageSize: 0, PackagePrice: 100}, 5, ErrBadPackage},
			{"package negative price", Charge{Model: Package, Currency: usd, PackageSize: 10, PackagePrice: -1}, 5, ErrBadPackage},
			{"unknown model", Charge{Model: Model(99), Currency: usd}, 5, ErrUnknownModel},
			{"bad currency", Charge{Model: PerUnit, Currency: Currency{Decimals: 2}, UnitRate: rat(1, 1)}, 5, ErrBadCurrency},
		}
		for _, tc := range cases {
			t.Run("When rating "+tc.name, func(t *testing.T) {
				t.Run("Then it returns the typed error", func(t *testing.T) {
					_, err := tc.c.Rate(tc.qty)
					if !errors.Is(err, tc.want) {
						t.Errorf("error = %v, want %v", err, tc.want)
					}
				})
			})
		}
	})

	t.Run("Given a config error paired with zero quantity", func(t *testing.T) {
		t.Run("When empty tiers are rated at quantity 0", func(t *testing.T) {
			t.Run("Then the config is still validated and errors", func(t *testing.T) {
				c := Charge{Model: Graduated, Currency: usd}
				if _, err := c.Rate(0); !errors.Is(err, ErrEmptyTiers) {
					t.Errorf("error = %v, want ErrEmptyTiers even at qty 0", err)
				}
			})
		})
	})
}

func TestOverflow(t *testing.T) {
	t.Run("Given amounts that exceed int64 minor units", func(t *testing.T) {
		t.Run("When a per-unit charge overflows during rounding", func(t *testing.T) {
			c := Charge{Model: PerUnit, Currency: JPY(RoundHalfUp), UnitRate: rat(2, 1)}
			if _, err := c.Rate(math.MaxInt64); !errors.Is(err, ErrOverflow) {
				t.Errorf("error = %v, want ErrOverflow", err)
			}
		})

		t.Run("When a package charge overflows during block pricing", func(t *testing.T) {
			c := Charge{Model: Package, Currency: JPY(RoundHalfUp), PackageSize: 1, PackagePrice: math.MaxInt64}
			if _, err := c.Rate(2); !errors.Is(err, ErrOverflow) {
				t.Errorf("error = %v, want ErrOverflow", err)
			}
		})

		t.Run("When a graduated charge overflows during rounding", func(t *testing.T) {
			c := Charge{Model: Graduated, Currency: JPY(RoundHalfUp), Tiers: []Tier{{Last: true, UnitRate: rat(2, 1)}}}
			if _, err := c.Rate(math.MaxInt64); !errors.Is(err, ErrOverflow) {
				t.Errorf("error = %v, want ErrOverflow", err)
			}
		})

		t.Run("When a volume charge overflows during rounding", func(t *testing.T) {
			c := Charge{Model: Volume, Currency: JPY(RoundHalfUp), Tiers: []Tier{{Last: true, UnitRate: rat(2, 1)}}}
			if _, err := c.Rate(math.MaxInt64); !errors.Is(err, ErrOverflow) {
				t.Errorf("error = %v, want ErrOverflow", err)
			}
		})
	})
}

func TestModelString(t *testing.T) {
	t.Run("Given each model and an unknown one", func(t *testing.T) {
		t.Run("Then String names them", func(t *testing.T) {
			cases := map[Model]string{
				PerUnit: "per-unit", Graduated: "graduated", Volume: "volume",
				Package: "package", Stairstep: "stairstep", Model(200): "Model(200)",
			}
			for m, want := range cases {
				if got := m.String(); got != want {
					t.Errorf("Model(%d).String() = %q, want %q", m, got, want)
				}
			}
		})
	})
}
