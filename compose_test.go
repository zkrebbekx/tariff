package tariff

import (
	"errors"
	"math"
	"math/big"
	"testing"
)

// flatCharge builds a charge that rates to a fixed flat fee, for exercising the
// int64 overflow guards without contriving an enormous unit rate.
func flatCharge(cur Currency, fee int64) Charge {
	return Charge{Model: PerUnit, Currency: cur, UnitRate: big.NewRat(0, 1), FlatFee: fee}
}

// dollarsCharge builds a per-unit charge whose single unit costs the given whole
// number of dollars, so Charged(dollarsCharge(usd, 100), 1) contributes $100.00.
func dollarsCharge(cur Currency, dollars int64) Charge {
	return Charge{Model: PerUnit, Currency: cur, UnitRate: big.NewRat(dollars, 1)}
}

// reconciles asserts the invoice invariant: the line subtotals sum to Total.
func reconciles(t *testing.T, inv Invoice) {
	t.Helper()
	var sum int64
	for _, l := range inv.Lines {
		sum += l.Subtotal
	}
	if sum != inv.Total {
		t.Errorf("lines sum to %d but Total is %d — invoice must reconcile", sum, inv.Total)
	}
}

// runStep applies a single step to an invoice at a given starting total.
func runStep(t *testing.T, cur Currency, startTotal int64, s Step) Invoice {
	t.Helper()
	inv := Invoice{Currency: cur, Total: startTotal}
	if err := s.apply(&inv); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return inv
}

func TestComposeOrderMatters(t *testing.T) {
	usd := USD(RoundHalfUp)
	c := dollarsCharge(usd, 100) // $100.00

	t.Run("Given a $100 charge, a 10% discount, and a $95 minimum", func(t *testing.T) {
		t.Run("When the discount is applied before the minimum", func(t *testing.T) {
			inv, err := Compose(usd,
				Charged(c, 1),
				PercentOff(big.NewRat(1, 10), "10% off"),
				MinimumCharge(9500, "minimum $95"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then $100 - $10 = $90 is floored back up to $95", func(t *testing.T) {
				if inv.Total != 9500 {
					t.Errorf("Total = %d, want 9500", inv.Total)
				}
				reconciles(t, inv)
			})
		})

		t.Run("When the minimum is applied before the discount", func(t *testing.T) {
			inv, err := Compose(usd,
				Charged(c, 1),
				MinimumCharge(9500, "minimum $95"),
				PercentOff(big.NewRat(1, 10), "10% off"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then the $100 clears the floor untouched and the discount takes it to $90", func(t *testing.T) {
				if inv.Total != 9000 {
					t.Errorf("Total = %d, want 9000", inv.Total)
				}
				reconciles(t, inv)
			})
		})

		t.Run("Then the two orders yield different, individually-correct totals", func(t *testing.T) {
			a, _ := Compose(usd, Charged(c, 1), PercentOff(big.NewRat(1, 10), "d"), MinimumCharge(9500, "m"))
			b, _ := Compose(usd, Charged(c, 1), MinimumCharge(9500, "m"), PercentOff(big.NewRat(1, 10), "d"))
			if a.Total == b.Total {
				t.Errorf("orders agree (%d) but should differ", a.Total)
			}
		})
	})
}

func TestComposeCreditOrderMatters(t *testing.T) {
	usd := USD(RoundHalfUp)
	c := dollarsCharge(usd, 100) // $100.00

	t.Run("Given a $100 charge, a $95 credit balance, and a 20% discount", func(t *testing.T) {
		t.Run("When the credit is drawn before the discount", func(t *testing.T) {
			bal := int64(9500)
			inv, err := Compose(usd,
				Charged(c, 1),
				DrawCredit(&bal, "prepaid credit"),
				PercentOff(big.NewRat(1, 5), "20% off"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then the full $95 credit is consumed and 20% of the $5 remainder leaves $4", func(t *testing.T) {
				if inv.Total != 400 {
					t.Errorf("Total = %d, want 400", inv.Total)
				}
				if bal != 0 {
					t.Errorf("balance left = %d, want 0 (fully consumed)", bal)
				}
				reconciles(t, inv)
			})
		})

		t.Run("When the credit is drawn after the discount", func(t *testing.T) {
			bal := int64(9500)
			inv, err := Compose(usd,
				Charged(c, 1),
				PercentOff(big.NewRat(1, 5), "20% off"),
				DrawCredit(&bal, "prepaid credit"),
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then the discount lowers the total first, so only $80 of credit is consumed", func(t *testing.T) {
				if inv.Total != 0 {
					t.Errorf("Total = %d, want 0", inv.Total)
				}
				if bal != 1500 {
					t.Errorf("balance left = %d, want 1500 (only $80 drawn)", bal)
				}
				reconciles(t, inv)
			})
		})

		t.Run("Then the order changes how much credit is consumed", func(t *testing.T) {
			before := int64(9500)
			_, _ = Compose(usd, Charged(c, 1), DrawCredit(&before, "c"), PercentOff(big.NewRat(1, 5), "d"))
			after := int64(9500)
			_, _ = Compose(usd, Charged(c, 1), PercentOff(big.NewRat(1, 5), "d"), DrawCredit(&after, "c"))
			consumedBefore := 9500 - before
			consumedAfter := 9500 - after
			if consumedBefore == consumedAfter {
				t.Errorf("credit consumed is the same (%d) regardless of order, but should differ", consumedBefore)
			}
		})
	})
}

func TestCharged(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a charge composed onto an empty invoice", func(t *testing.T) {
		inv, err := Compose(usd, Charged(dollarsCharge(usd, 100), 3))
		if err != nil {
			t.Fatal(err)
		}
		t.Run("Then its lines are appended and both subtotal and total carry the charge", func(t *testing.T) {
			if inv.Subtotal != 30000 || inv.Total != 30000 {
				t.Errorf("subtotal/total = %d/%d, want 30000/30000", inv.Subtotal, inv.Total)
			}
			if len(inv.Lines) != 1 {
				t.Fatalf("got %d lines, want 1", len(inv.Lines))
			}
			reconciles(t, inv)
		})
	})

	t.Run("Given a charge whose currency code differs from the invoice", func(t *testing.T) {
		eur := Currency{Code: "EUR", Decimals: 2, Rounding: RoundHalfUp}
		t.Run("Then Compose returns ErrCurrencyMismatch", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(eur, 100), 1))
			if !errors.Is(err, ErrCurrencyMismatch) {
				t.Errorf("err = %v, want ErrCurrencyMismatch", err)
			}
		})
	})

	t.Run("Given a charge whose minor-unit scale differs from the invoice", func(t *testing.T) {
		usd3 := Currency{Code: "USD", Decimals: 3, Rounding: RoundHalfUp}
		t.Run("Then Compose returns ErrCurrencyMismatch", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(usd3, 100), 1))
			if !errors.Is(err, ErrCurrencyMismatch) {
				t.Errorf("err = %v, want ErrCurrencyMismatch", err)
			}
		})
	})

	t.Run("Given a charge that fails to rate", func(t *testing.T) {
		t.Run("Then the rating error propagates", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(usd, 100), -1))
			if !errors.Is(err, ErrNegativeQuantity) {
				t.Errorf("err = %v, want ErrNegativeQuantity", err)
			}
		})
	})

	t.Run("Given the invoice rounding mode differs from the charge's", func(t *testing.T) {
		charge := dollarsCharge(USD(RoundHalfEven), 100)
		t.Run("Then it still composes — only code and scale must match", func(t *testing.T) {
			inv, err := Compose(usd, Charged(charge, 1))
			if err != nil {
				t.Fatalf("err = %v, want nil (rounding mode may differ)", err)
			}
			if inv.Total != 10000 {
				t.Errorf("Total = %d, want 10000", inv.Total)
			}
		})
	})
}

func TestPercentOff(t *testing.T) {
	t.Run("Given a running total of 505 minor units", func(t *testing.T) {
		t.Run("When 10% is taken half-up", func(t *testing.T) {
			t.Run("Then 50.5 rounds to 51 and lands as a -51 line", func(t *testing.T) {
				inv := runStep(t, USD(RoundHalfUp), 505, PercentOff(big.NewRat(1, 10), "10% off"))
				if inv.Total != 454 {
					t.Errorf("Total = %d, want 454", inv.Total)
				}
				if len(inv.Lines) != 1 || inv.Lines[0].Subtotal != -51 || inv.Lines[0].Label != "10% off" {
					t.Errorf("line = %+v, want {-51, \"10%% off\"}", inv.Lines)
				}
			})
		})
		t.Run("When 10% is taken half-even", func(t *testing.T) {
			t.Run("Then 50.5 rounds to the even 50 — the discount rounds once via the currency", func(t *testing.T) {
				inv := runStep(t, USD(RoundHalfEven), 505, PercentOff(big.NewRat(1, 10), "10% off"))
				if inv.Total != 455 {
					t.Errorf("Total = %d, want 455", inv.Total)
				}
			})
		})
	})

	t.Run("Given an exact one-third discount of 10 minor units", func(t *testing.T) {
		t.Run("Then 3.333... rounds once to 3, not a drifting float", func(t *testing.T) {
			inv := runStep(t, USD(RoundHalfUp), 10, PercentOff(big.NewRat(1, 3), "a third off"))
			if inv.Total != 7 {
				t.Errorf("Total = %d, want 7", inv.Total)
			}
		})
	})

	t.Run("Given a zero-value discount", func(t *testing.T) {
		t.Run("Then no line is added and the total is unchanged", func(t *testing.T) {
			inv := runStep(t, USD(RoundHalfUp), 1000, PercentOff(new(big.Rat), "0% off"))
			if inv.Total != 1000 || len(inv.Lines) != 0 {
				t.Errorf("Total/lines = %d/%d, want 1000/0", inv.Total, len(inv.Lines))
			}
		})
	})

	t.Run("Given malformed percentages", func(t *testing.T) {
		cases := map[string]*big.Rat{
			"nil":         nil,
			"negative":    big.NewRat(-1, 10),
			"above 100%%": big.NewRat(11, 10),
		}
		for name, pct := range cases {
			t.Run("When the percentage is "+name, func(t *testing.T) {
				t.Run("Then Compose returns ErrBadDiscount", func(t *testing.T) {
					_, err := Compose(USD(RoundHalfUp), Charged(dollarsCharge(USD(RoundHalfUp), 1), 1), PercentOff(pct, "x"))
					if !errors.Is(err, ErrBadDiscount) {
						t.Errorf("err = %v, want ErrBadDiscount", err)
					}
				})
			})
		}
	})
}

func TestAmountOff(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a fixed amount off below the running total", func(t *testing.T) {
		t.Run("Then it subtracts and records a negative line", func(t *testing.T) {
			inv := runStep(t, usd, 1000, AmountOff(300, "$3 off"))
			if inv.Total != 700 || inv.Lines[0].Subtotal != -300 {
				t.Errorf("Total/line = %d/%d, want 700/-300", inv.Total, inv.Lines[0].Subtotal)
			}
		})
	})

	t.Run("Given a fixed amount off larger than the running total", func(t *testing.T) {
		t.Run("Then it may drive the total negative (pair with a minimum if unwanted)", func(t *testing.T) {
			inv := runStep(t, usd, 100, AmountOff(300, "$3 off"))
			if inv.Total != -200 {
				t.Errorf("Total = %d, want -200", inv.Total)
			}
		})
	})

	t.Run("Given a zero amount off", func(t *testing.T) {
		t.Run("Then no line is added", func(t *testing.T) {
			inv := runStep(t, usd, 1000, AmountOff(0, "nothing"))
			if len(inv.Lines) != 0 {
				t.Errorf("got %d lines, want 0", len(inv.Lines))
			}
		})
	})

	t.Run("Given a negative amount off", func(t *testing.T) {
		t.Run("Then Compose returns ErrBadDiscount", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(usd, 1), 1), AmountOff(-5, "x"))
			if !errors.Is(err, ErrBadDiscount) {
				t.Errorf("err = %v, want ErrBadDiscount", err)
			}
		})
	})
}

func TestMinimumCharge(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a running total below the floor", func(t *testing.T) {
		t.Run("Then a positive top-up line lifts it to the floor", func(t *testing.T) {
			inv := runStep(t, usd, 900, MinimumCharge(1000, "minimum"))
			if inv.Total != 1000 || inv.Lines[0].Subtotal != 100 {
				t.Errorf("Total/line = %d/%d, want 1000/100", inv.Total, inv.Lines[0].Subtotal)
			}
		})
	})

	t.Run("Given a running total already above the floor", func(t *testing.T) {
		t.Run("Then nothing is added", func(t *testing.T) {
			inv := runStep(t, usd, 1200, MinimumCharge(1000, "minimum"))
			if inv.Total != 1200 || len(inv.Lines) != 0 {
				t.Errorf("Total/lines = %d/%d, want 1200/0", inv.Total, len(inv.Lines))
			}
		})
	})

	t.Run("Given a running total exactly at the floor", func(t *testing.T) {
		t.Run("Then nothing is added", func(t *testing.T) {
			inv := runStep(t, usd, 1000, MinimumCharge(1000, "minimum"))
			if len(inv.Lines) != 0 {
				t.Errorf("got %d lines, want 0", len(inv.Lines))
			}
		})
	})

	t.Run("Given a negative floor", func(t *testing.T) {
		t.Run("Then Compose returns ErrBadFloor", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(usd, 1), 1), MinimumCharge(-1, "x"))
			if !errors.Is(err, ErrBadFloor) {
				t.Errorf("err = %v, want ErrBadFloor", err)
			}
		})
	})
}

func TestDrawCredit(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a credit balance larger than the running total", func(t *testing.T) {
		t.Run("Then the draw is capped at the total, which goes to zero", func(t *testing.T) {
			bal := int64(900)
			inv := runStep(t, usd, 500, DrawCredit(&bal, "credit"))
			if inv.Total != 0 || bal != 400 || inv.Lines[0].Subtotal != -500 {
				t.Errorf("total/bal/line = %d/%d/%d, want 0/400/-500", inv.Total, bal, inv.Lines[0].Subtotal)
			}
		})
	})

	t.Run("Given a credit balance smaller than the running total", func(t *testing.T) {
		t.Run("Then the draw is capped at the balance, which goes to zero", func(t *testing.T) {
			bal := int64(500)
			inv := runStep(t, usd, 900, DrawCredit(&bal, "credit"))
			if inv.Total != 400 || bal != 0 {
				t.Errorf("total/bal = %d/%d, want 400/0", inv.Total, bal)
			}
		})
	})

	t.Run("Given a running total of zero", func(t *testing.T) {
		t.Run("Then nothing is drawn and no line is added", func(t *testing.T) {
			bal := int64(500)
			inv := runStep(t, usd, 0, DrawCredit(&bal, "credit"))
			if len(inv.Lines) != 0 || bal != 500 {
				t.Errorf("lines/bal = %d/%d, want 0/500", len(inv.Lines), bal)
			}
		})
	})

	t.Run("Given a negative running total", func(t *testing.T) {
		t.Run("Then the draw is capped at zero and never adds to what is owed", func(t *testing.T) {
			bal := int64(500)
			inv := runStep(t, usd, -200, DrawCredit(&bal, "credit"))
			if inv.Total != -200 || bal != 500 || len(inv.Lines) != 0 {
				t.Errorf("total/bal/lines = %d/%d/%d, want -200/500/0", inv.Total, bal, len(inv.Lines))
			}
		})
	})

	t.Run("Given a nil balance pointer", func(t *testing.T) {
		t.Run("Then Compose returns ErrBadBalance", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(usd, 1), 1), DrawCredit(nil, "x"))
			if !errors.Is(err, ErrBadBalance) {
				t.Errorf("err = %v, want ErrBadBalance", err)
			}
		})
	})

	t.Run("Given a negative balance", func(t *testing.T) {
		t.Run("Then Compose returns ErrBadBalance", func(t *testing.T) {
			bal := int64(-1)
			_, err := Compose(usd, Charged(dollarsCharge(usd, 1), 1), DrawCredit(&bal, "x"))
			if !errors.Is(err, ErrBadBalance) {
				t.Errorf("err = %v, want ErrBadBalance", err)
			}
		})
	})
}

func TestDrawCommitment(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given a commitment balance", func(t *testing.T) {
		t.Run("Then it draws down with the same capping as a credit but its own label", func(t *testing.T) {
			bal := int64(600)
			inv := runStep(t, usd, 1000, DrawCommitment(&bal, "annual commitment"))
			if inv.Total != 400 || bal != 0 {
				t.Errorf("total/bal = %d/%d, want 400/0", inv.Total, bal)
			}
			if inv.Lines[0].Label != "annual commitment" || inv.Lines[0].Subtotal != -600 {
				t.Errorf("line = %+v, want {-600, annual commitment}", inv.Lines[0])
			}
		})
	})

	t.Run("Given a nil commitment pointer", func(t *testing.T) {
		t.Run("Then Compose returns ErrBadBalance", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(usd, 1), 1), DrawCommitment(nil, "x"))
			if !errors.Is(err, ErrBadBalance) {
				t.Errorf("err = %v, want ErrBadBalance", err)
			}
		})
	})
}

func TestComposeGuards(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given no steps", func(t *testing.T) {
		t.Run("Then Compose returns an empty, valid invoice", func(t *testing.T) {
			inv, err := Compose(usd)
			if err != nil {
				t.Fatal(err)
			}
			if inv.Total != 0 || inv.Subtotal != 0 || len(inv.Lines) != 0 || inv.Currency != usd {
				t.Errorf("empty invoice = %+v", inv)
			}
		})
	})

	t.Run("Given a nil step", func(t *testing.T) {
		t.Run("Then Compose returns ErrNilStep", func(t *testing.T) {
			_, err := Compose(usd, Charged(dollarsCharge(usd, 1), 1), nil)
			if !errors.Is(err, ErrNilStep) {
				t.Errorf("err = %v, want ErrNilStep", err)
			}
		})
	})

	t.Run("Given an unusable invoice currency", func(t *testing.T) {
		t.Run("Then Compose returns ErrBadCurrency", func(t *testing.T) {
			_, err := Compose(Currency{Code: "USD", Decimals: 2})
			if !errors.Is(err, ErrBadCurrency) {
				t.Errorf("err = %v, want ErrBadCurrency", err)
			}
		})
	})

	t.Run("Given a full mixed invoice", func(t *testing.T) {
		t.Run("Then every line reconciles to the total in application order", func(t *testing.T) {
			credit := int64(200)
			inv, err := Compose(usd,
				Charged(dollarsCharge(usd, 100), 1),
				PercentOff(big.NewRat(1, 10), "10% off"),
				AmountOff(500, "$5 coupon"),
				DrawCredit(&credit, "credit"),
				MinimumCharge(5000, "minimum $50"),
			)
			if err != nil {
				t.Fatal(err)
			}
			reconciles(t, inv)
			if inv.Subtotal != 10000 {
				t.Errorf("Subtotal = %d, want 10000 (gross charges)", inv.Subtotal)
			}
		})
	})
}

func TestComposeOverflow(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given adjustments that push the running total past int64", func(t *testing.T) {
		t.Run("When two near-max fixed discounts underflow the total", func(t *testing.T) {
			t.Run("Then the running-total guard returns ErrOverflow", func(t *testing.T) {
				_, err := Compose(usd, AmountOff(math.MaxInt64, "a"), AmountOff(math.MaxInt64, "b"))
				if !errors.Is(err, ErrOverflow) {
					t.Errorf("err = %v, want ErrOverflow", err)
				}
			})
		})

		t.Run("When a top-up to a max floor from a negative total overflows", func(t *testing.T) {
			t.Run("Then the minimum-charge subtraction returns ErrOverflow", func(t *testing.T) {
				_, err := Compose(usd, AmountOff(100, "drive negative"), MinimumCharge(math.MaxInt64, "min"))
				if !errors.Is(err, ErrOverflow) {
					t.Errorf("err = %v, want ErrOverflow", err)
				}
			})
		})

		t.Run("When two near-max charges overflow the gross subtotal", func(t *testing.T) {
			t.Run("Then the subtotal guard returns ErrOverflow", func(t *testing.T) {
				_, err := Compose(usd,
					Charged(flatCharge(usd, math.MaxInt64), 1),
					Charged(flatCharge(usd, math.MaxInt64), 1),
				)
				if !errors.Is(err, ErrOverflow) {
					t.Errorf("err = %v, want ErrOverflow", err)
				}
			})
		})
	})
}

// TestComposeAtomicBalances pins that a Compose that errors after a draw leaves
// the caller's balance untouched — the draw is committed only if the whole
// composition succeeds. Regression for the non-atomic in-place mutation.
func TestComposeAtomicBalances(t *testing.T) {
	usd := USD(RoundHalfUp)
	charge := dollarsCharge(usd, 100)

	t.Run("Given a draw followed by a step that errors", func(t *testing.T) {
		cases := []struct {
			name string
			bad  Step
		}{
			{"a bad discount (10x, not 10%)", PercentOff(big.NewRat(10, 1), "oops")},
			{"a negative amount off", AmountOff(-1, "bad")},
		}
		for _, tc := range cases {
			t.Run("When the failing step is "+tc.name, func(t *testing.T) {
				bal := int64(3000)
				_, err := Compose(usd, Charged(charge, 1), DrawCredit(&bal, "credit"), tc.bad)
				t.Run("Then Compose errors and the balance is unchanged", func(t *testing.T) {
					if err == nil {
						t.Fatal("expected an error")
					}
					if bal != 3000 {
						t.Fatalf("balance = %d after failed compose, want 3000 unchanged", bal)
					}
				})
			})
		}
	})

	t.Run("Given a second draw whose balance is invalid", func(t *testing.T) {
		good := int64(3000)
		bad := int64(-1)
		_, err := Compose(usd, Charged(charge, 1), DrawCredit(&good, "good"), DrawCredit(&bad, "bad"))
		t.Run("Then the first, valid balance is not drawn down", func(t *testing.T) {
			if !errors.Is(err, ErrBadBalance) {
				t.Fatalf("err = %v, want ErrBadBalance", err)
			}
			if good != 3000 {
				t.Fatalf("good balance = %d, want 3000 unchanged", good)
			}
		})
	})

	t.Run("Given a successful compose", func(t *testing.T) {
		bal := int64(3000)
		inv, err := Compose(usd, Charged(charge, 1), DrawCredit(&bal, "credit"))
		t.Run("Then the balance is committed exactly once", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			if bal != 0 {
				t.Fatalf("balance = %d, want 0 ($30 balance fully drawn against the $100 total)", bal)
			}
			if inv.Total != 7000 {
				t.Fatalf("total = %d, want 7000 ($100 - $30 credit)", inv.Total)
			}
		})
	})

	t.Run("Given two draws against the SAME balance in one compose", func(t *testing.T) {
		// The deferred draws must net against each other, or the second reads the
		// un-decremented balance and over-draws it negative.
		bal := int64(50)
		inv, err := Compose(usd,
			Charged(dollarsCharge(usd, 200), 1), // $200
			DrawCredit(&bal, "first"),
			DrawCredit(&bal, "second"),
		)
		t.Run("Then the balance is drawn once, never negative", func(t *testing.T) {
			if err != nil {
				t.Fatal(err)
			}
			if bal != 0 {
				t.Fatalf("balance = %d, want 0 — a $50 balance drawn twice must not go negative", bal)
			}
			if inv.Total != 19950 {
				t.Fatalf("total = %d, want 19950 ($200 - $0.50 balance)", inv.Total)
			}
		})
	})
}

// TestPercentOffNonPositiveTotal pins that a percentage discount on a
// zero-or-negative running total is a no-op, never a surcharge line.
func TestPercentOffNonPositiveTotal(t *testing.T) {
	usd := USD(RoundHalfUp)
	t.Run("Given a running total driven negative before a percentage discount", func(t *testing.T) {
		inv, err := Compose(usd,
			dollarsChargeStep(usd, 100),    // +$100
			AmountOff(30000, "big coupon"), // -$300 -> -$200
			PercentOff(big.NewRat(1, 10), "10% off"),
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Run("Then the discount does not add a positive surcharge line", func(t *testing.T) {
			if inv.Total != -20000 {
				t.Fatalf("total = %d, want -20000 — the 10%% step must not move a negative total", inv.Total)
			}
			last := inv.Lines[len(inv.Lines)-1]
			if last.Label == "10% off" {
				t.Fatalf("a no-op percent-off still appended a line: %+v", last)
			}
		})
	})
}

func dollarsChargeStep(cur Currency, dollars int64) Step {
	return Charged(dollarsCharge(cur, dollars), 1)
}
