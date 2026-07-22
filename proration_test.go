package tariff

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

// utc builds a UTC instant tersely.
func utc(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, time.UTC)
}

func fracEq(t *testing.T, got *big.Rat, wantNum, wantDen int64) {
	t.Helper()
	want := big.NewRat(wantNum, wantDen)
	if got.Cmp(want) != 0 {
		t.Errorf("fraction = %s, want %s", got, want)
	}
}

func TestProrationGolden(t *testing.T) {
	usd := USD(RoundHalfUp)

	t.Run("Given the Stripe mid-period upgrade, prorated to the second", func(t *testing.T) {
		// A 30-day January period; the change lands exactly halfway.
		p := Period{Start: utc(2026, time.January, 1, 0, 0), End: utc(2026, time.January, 31, 0, 0)}
		at := utc(2026, time.January, 16, 0, 0)

		t.Run("When the remaining fraction is taken by second", func(t *testing.T) {
			frac, err := p.Fraction(at, p.End, ProrateBySecond)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then it is exactly one half", func(t *testing.T) {
				fracEq(t, frac, 1, 2)
			})
		})

		t.Run("When a $10 plan is upgraded to $20 at the midpoint", func(t *testing.T) {
			got, err := Change(1000, 2000, usd, p, at, ProrateBySecond)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then credit is -$5, charge is $10, net is $5 — the verified model", func(t *testing.T) {
				want := Proration{Credit: -500, Charge: 1000, Net: 500}
				if got != want {
					t.Errorf("Change = %+v, want %+v", got, want)
				}
			})
		})
	})

	t.Run("Given the Chargebee day-based formula over a 31-day term", func(t *testing.T) {
		// credit = (old/term) x remaining, charge = (new/term) x remaining.
		p := Period{Start: utc(2026, time.January, 1, 0, 0), End: utc(2026, time.February, 1, 0, 0)}
		at := utc(2026, time.January, 16, 0, 0)

		t.Run("When the remaining fraction is taken by day", func(t *testing.T) {
			frac, err := p.Fraction(at, p.End, ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then it is 16 remaining days over 31 term days", func(t *testing.T) {
				fracEq(t, frac, 16, 31)
			})
		})

		t.Run("When $31 upgrades to $62 with 16 days remaining", func(t *testing.T) {
			got, err := Change(3100, 6200, usd, p, at, ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			t.Run("Then credit -$16, charge $32, net $16 exactly", func(t *testing.T) {
				want := Proration{Credit: -1600, Charge: 3200, Net: 1600}
				if got != want {
					t.Errorf("Change = %+v, want %+v", got, want)
				}
			})
		})
	})
}

func TestFractionBoundaries(t *testing.T) {
	p := Period{Start: utc(2026, time.January, 1, 0, 0), End: utc(2026, time.February, 1, 0, 0)}

	t.Run("Given a billing period", func(t *testing.T) {
		for _, b := range []Basis{ProrateBySecond, ProrateByDay} {
			b := b
			t.Run("With basis "+b.String(), func(t *testing.T) {
				t.Run("When the window is the whole period", func(t *testing.T) {
					t.Run("Then the fraction is exactly 1", func(t *testing.T) {
						frac, err := p.Fraction(p.Start, p.End, b)
						if err != nil {
							t.Fatal(err)
						}
						fracEq(t, frac, 1, 1)
					})
				})
				t.Run("When the window is wider than the period", func(t *testing.T) {
					t.Run("Then it clamps to exactly 1", func(t *testing.T) {
						frac, err := p.Fraction(p.Start.Add(-48*time.Hour), p.End.Add(48*time.Hour), b)
						if err != nil {
							t.Fatal(err)
						}
						fracEq(t, frac, 1, 1)
					})
				})
				t.Run("When the window is empty (from == to)", func(t *testing.T) {
					t.Run("Then the fraction is exactly 0", func(t *testing.T) {
						frac, err := p.Fraction(p.Start, p.Start, b)
						if err != nil {
							t.Fatal(err)
						}
						fracEq(t, frac, 0, 1)
					})
				})
				t.Run("When the window lies entirely after the period", func(t *testing.T) {
					t.Run("Then it clamps to a zero fraction, not an error", func(t *testing.T) {
						frac, err := p.Fraction(p.End.Add(time.Hour), p.End.Add(72*time.Hour), b)
						if err != nil {
							t.Fatal(err)
						}
						fracEq(t, frac, 0, 1)
					})
				})
			})
		}
	})

	t.Run("Given an incoherent window with from after to", func(t *testing.T) {
		t.Run("Then Fraction returns ErrBadWindow", func(t *testing.T) {
			if _, err := p.Fraction(p.End, p.Start, ProrateBySecond); !errors.Is(err, ErrBadWindow) {
				t.Errorf("err = %v, want ErrBadWindow", err)
			}
		})
	})

	t.Run("Given a period whose end is not after its start", func(t *testing.T) {
		bad := Period{Start: p.End, End: p.Start}
		t.Run("Then Fraction returns ErrBadPeriod", func(t *testing.T) {
			if _, err := bad.Fraction(p.Start, p.End, ProrateBySecond); !errors.Is(err, ErrBadPeriod) {
				t.Errorf("err = %v, want ErrBadPeriod", err)
			}
		})
	})

	t.Run("Given a day basis on a period within a single calendar day", func(t *testing.T) {
		sub := Period{Start: utc(2026, time.January, 1, 8, 0), End: utc(2026, time.January, 1, 20, 0)}
		t.Run("Then Fraction returns ErrBadPeriod (zero day denominator)", func(t *testing.T) {
			if _, err := sub.Fraction(sub.Start, sub.End, ProrateByDay); !errors.Is(err, ErrBadPeriod) {
				t.Errorf("err = %v, want ErrBadPeriod", err)
			}
		})
		t.Run("But a second basis on the same period is fine", func(t *testing.T) {
			frac, err := sub.Fraction(sub.Start, sub.End, ProrateBySecond)
			if err != nil {
				t.Fatal(err)
			}
			fracEq(t, frac, 1, 1)
		})
	})

	t.Run("Given an unknown basis", func(t *testing.T) {
		t.Run("Then Fraction returns ErrBadBasis", func(t *testing.T) {
			if _, err := p.Fraction(p.Start, p.End, Basis(99)); !errors.Is(err, ErrBadBasis) {
				t.Errorf("err = %v, want ErrBadBasis", err)
			}
		})
	})
}

func TestFractionSecondVsDay(t *testing.T) {
	t.Run("Given the same period and a window ending at midday", func(t *testing.T) {
		// 31-day period; window covers 15.5 real days but 15 whole calendar days.
		p := Period{Start: utc(2026, time.January, 1, 0, 0), End: utc(2026, time.February, 1, 0, 0)}
		to := utc(2026, time.January, 16, 12, 0)

		t.Run("When measured by second", func(t *testing.T) {
			t.Run("Then it is 15.5/31 = 1/2 of the period", func(t *testing.T) {
				frac, err := p.Fraction(p.Start, to, ProrateBySecond)
				if err != nil {
					t.Fatal(err)
				}
				fracEq(t, frac, 1, 2)
			})
		})
		t.Run("When measured by day", func(t *testing.T) {
			t.Run("Then it is 15/31 — the partial day is not counted", func(t *testing.T) {
				frac, err := p.Fraction(p.Start, to, ProrateByDay)
				if err != nil {
					t.Fatal(err)
				}
				fracEq(t, frac, 15, 31)
			})
		})
		t.Run("Then the two bases genuinely differ", func(t *testing.T) {
			sec, _ := p.Fraction(p.Start, to, ProrateBySecond)
			day, _ := p.Fraction(p.Start, to, ProrateByDay)
			if sec.Cmp(day) == 0 {
				t.Errorf("bases agree (%s) but should differ", sec)
			}
		})
	})
}

func TestFractionDST(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata for America/New_York: %v", err)
	}

	t.Run("Given a March period spanning the spring-forward (a 23-hour day)", func(t *testing.T) {
		// DST begins 2026-03-08. Window [Mar 1, Mar 15) contains it.
		p := Period{
			Start: time.Date(2026, time.March, 1, 0, 0, 0, 0, ny),
			End:   time.Date(2026, time.April, 1, 0, 0, 0, 0, ny),
		}
		to := time.Date(2026, time.March, 15, 0, 0, 0, 0, ny)

		t.Run("When measured by second", func(t *testing.T) {
			t.Run("Then real elapsed time is 335h/743h — the lost hour is counted", func(t *testing.T) {
				// 14 days incl. one 23h day = 335h; term 31 days incl. one 23h day = 743h.
				frac, err := p.Fraction(p.Start, to, ProrateBySecond)
				if err != nil {
					t.Fatal(err)
				}
				fracEq(t, frac, 335, 743)
			})
		})
		t.Run("When measured by day", func(t *testing.T) {
			t.Run("Then the DST day counts as one: 14/31, not off by an hour", func(t *testing.T) {
				frac, err := p.Fraction(p.Start, to, ProrateByDay)
				if err != nil {
					t.Fatal(err)
				}
				fracEq(t, frac, 14, 31)
			})
		})
		t.Run("Then the two bases differ by exactly the DST hour's worth", func(t *testing.T) {
			sec, _ := p.Fraction(p.Start, to, ProrateBySecond)
			day, _ := p.Fraction(p.Start, to, ProrateByDay)
			if sec.Cmp(day) == 0 {
				t.Errorf("bases agree (%s) across a DST change but should differ", sec)
			}
		})
	})

	t.Run("Given a November period spanning the fall-back (a 25-hour day)", func(t *testing.T) {
		// DST ends 2026-11-01. Window [Nov 1, Nov 15) contains it.
		p := Period{
			Start: time.Date(2026, time.November, 1, 0, 0, 0, 0, ny),
			End:   time.Date(2026, time.December, 1, 0, 0, 0, 0, ny),
		}
		to := time.Date(2026, time.November, 15, 0, 0, 0, 0, ny)

		t.Run("When measured by second", func(t *testing.T) {
			t.Run("Then real elapsed time is 337h/721h — the repeated hour is counted", func(t *testing.T) {
				// 14 days incl. one 25h day = 337h; term 30 days incl. one 25h day = 721h.
				frac, err := p.Fraction(p.Start, to, ProrateBySecond)
				if err != nil {
					t.Fatal(err)
				}
				fracEq(t, frac, 337, 721)
			})
		})
		t.Run("When measured by day", func(t *testing.T) {
			t.Run("Then the 25-hour day still counts as one: 14/30", func(t *testing.T) {
				frac, err := p.Fraction(p.Start, to, ProrateByDay)
				if err != nil {
					t.Fatal(err)
				}
				fracEq(t, frac, 14, 30)
			})
		})
	})
}

func TestFractionLeapYear(t *testing.T) {
	t.Run("Given a February period in a leap year", func(t *testing.T) {
		p := Period{Start: utc(2024, time.February, 1, 0, 0), End: utc(2024, time.March, 1, 0, 0)}
		t.Run("Then the whole period by day is exactly 1 over 29 term days", func(t *testing.T) {
			frac, err := p.Fraction(p.Start, p.End, ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			fracEq(t, frac, 1, 1)
			half, err := p.Fraction(p.Start, utc(2024, time.February, 15, 0, 0), ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			fracEq(t, half, 14, 29) // 14 days elapsed of a 29-day February
		})
	})

	t.Run("Given a full leap year of 366 days", func(t *testing.T) {
		p := Period{Start: utc(2024, time.January, 1, 0, 0), End: utc(2025, time.January, 1, 0, 0)}
		t.Run("When measured to the start of July", func(t *testing.T) {
			t.Run("Then the fraction is 182/366 over the 366-day denominator", func(t *testing.T) {
				frac, err := p.Fraction(p.Start, utc(2024, time.July, 1, 0, 0), ProrateByDay)
				if err != nil {
					t.Fatal(err)
				}
				fracEq(t, frac, 182, 366)
			})
		})
	})
}

func TestProrate(t *testing.T) {
	t.Run("Given a half fraction", func(t *testing.T) {
		half := big.NewRat(1, 2)
		t.Run("When 101 minor units are prorated half-up", func(t *testing.T) {
			t.Run("Then 50.5 rounds to 51", func(t *testing.T) {
				got, err := Prorate(101, USD(RoundHalfUp), half)
				if err != nil {
					t.Fatal(err)
				}
				if got != 51 {
					t.Errorf("Prorate = %d, want 51", got)
				}
			})
		})
		t.Run("When 101 minor units are prorated half-even", func(t *testing.T) {
			t.Run("Then 50.5 rounds to the even 50 — one rounding via the currency", func(t *testing.T) {
				got, err := Prorate(101, USD(RoundHalfEven), half)
				if err != nil {
					t.Fatal(err)
				}
				if got != 50 {
					t.Errorf("Prorate = %d, want 50", got)
				}
			})
		})
		t.Run("When the amount is negative (a credit)", func(t *testing.T) {
			t.Run("Then the prorated amount is negative", func(t *testing.T) {
				got, err := Prorate(-1000, USD(RoundHalfUp), half)
				if err != nil {
					t.Fatal(err)
				}
				if got != -500 {
					t.Errorf("Prorate = %d, want -500", got)
				}
			})
		})
	})

	t.Run("Given a nil fraction", func(t *testing.T) {
		t.Run("Then Prorate returns ErrBadWindow", func(t *testing.T) {
			if _, err := Prorate(100, USD(RoundHalfUp), nil); !errors.Is(err, ErrBadWindow) {
				t.Errorf("err = %v, want ErrBadWindow", err)
			}
		})
	})

	t.Run("Given an unusable currency", func(t *testing.T) {
		t.Run("Then Prorate returns ErrBadCurrency", func(t *testing.T) {
			if _, err := Prorate(100, Currency{Decimals: 2}, big.NewRat(1, 2)); !errors.Is(err, ErrBadCurrency) {
				t.Errorf("err = %v, want ErrBadCurrency", err)
			}
		})
	})
}

func TestChangeTrialToPaid(t *testing.T) {
	usd := USD(RoundHalfUp)
	p := Period{Start: utc(2026, time.January, 1, 0, 0), End: utc(2026, time.January, 31, 0, 0)}
	at := utc(2026, time.January, 16, 0, 0)

	t.Run("Given a trial (old amount zero) converting to a paid plan", func(t *testing.T) {
		got, err := Change(0, 2000, usd, p, at, ProrateBySecond)
		if err != nil {
			t.Fatal(err)
		}
		t.Run("Then the credit half is zero and only the new plan is charged", func(t *testing.T) {
			want := Proration{Credit: 0, Charge: 1000, Net: 1000}
			if got != want {
				t.Errorf("Change = %+v, want %+v", got, want)
			}
		})
	})

	t.Run("Given an invalid period", func(t *testing.T) {
		bad := Period{Start: p.End, End: p.Start}
		t.Run("Then Change surfaces ErrBadPeriod", func(t *testing.T) {
			if _, err := Change(1000, 2000, usd, bad, at, ProrateBySecond); !errors.Is(err, ErrBadPeriod) {
				t.Errorf("err = %v, want ErrBadPeriod", err)
			}
		})
	})

	t.Run("Given an unusable currency", func(t *testing.T) {
		t.Run("Then Change surfaces ErrBadCurrency", func(t *testing.T) {
			if _, err := Change(1000, 2000, Currency{Decimals: 2}, p, at, ProrateBySecond); !errors.Is(err, ErrBadCurrency) {
				t.Errorf("err = %v, want ErrBadCurrency", err)
			}
		})
	})
}

func TestNextBoundaryMonthEnd(t *testing.T) {
	t.Run("Given a January 31 anchor in a leap year", func(t *testing.T) {
		anchor := utc(2024, time.January, 31, 0, 0)
		t.Run("When stepping monthly", func(t *testing.T) {
			b1 := NextBoundary(anchor, anchor, Monthly)
			b2 := NextBoundary(anchor, b1, Monthly)
			t.Run("Then it clamps to Feb 29, then returns to Mar 31 (no drift to the 28th)", func(t *testing.T) {
				if !b1.Equal(utc(2024, time.February, 29, 0, 0)) {
					t.Errorf("first boundary = %s, want 2024-02-29", b1.Format(time.RFC3339))
				}
				if !b2.Equal(utc(2024, time.March, 31, 0, 0)) {
					t.Errorf("second boundary = %s, want 2024-03-31", b2.Format(time.RFC3339))
				}
			})
		})
	})

	t.Run("Given a January 31 anchor in a non-leap year", func(t *testing.T) {
		anchor := utc(2023, time.January, 31, 0, 0)
		t.Run("When stepping monthly", func(t *testing.T) {
			b1 := NextBoundary(anchor, anchor, Monthly)
			b2 := NextBoundary(anchor, b1, Monthly)
			t.Run("Then it clamps to Feb 28, then returns to Mar 31", func(t *testing.T) {
				if !b1.Equal(utc(2023, time.February, 28, 0, 0)) {
					t.Errorf("first boundary = %s, want 2023-02-28", b1.Format(time.RFC3339))
				}
				if !b2.Equal(utc(2023, time.March, 31, 0, 0)) {
					t.Errorf("second boundary = %s, want 2023-03-31", b2.Format(time.RFC3339))
				}
			})
		})
	})

	t.Run("Given a January 31 anchor stepped across a whole year", func(t *testing.T) {
		anchor := utc(2024, time.January, 31, 0, 0)
		t.Run("Then every month clamps to its own last day without permanent drift", func(t *testing.T) {
			want := []string{
				"2024-02-29", "2024-03-31", "2024-04-30", "2024-05-31",
				"2024-06-30", "2024-07-31", "2024-08-31", "2024-09-30",
				"2024-10-31", "2024-11-30", "2024-12-31", "2025-01-31",
			}
			cur := anchor
			for i, w := range want {
				cur = NextBoundary(anchor, cur, Monthly)
				if got := cur.Format("2006-01-02"); got != w {
					t.Errorf("boundary %d = %s, want %s", i, got, w)
				}
			}
		})
	})
}

func TestNextBoundaryYearly(t *testing.T) {
	t.Run("Given a February 29 anchor", func(t *testing.T) {
		anchor := utc(2024, time.February, 29, 0, 0)
		t.Run("When stepping yearly", func(t *testing.T) {
			t.Run("Then common years clamp to Feb 28 and the next leap year returns to Feb 29", func(t *testing.T) {
				want := []string{"2025-02-28", "2026-02-28", "2027-02-28", "2028-02-29"}
				cur := anchor
				for i, w := range want {
					cur = NextBoundary(anchor, cur, Yearly)
					if got := cur.Format("2006-01-02"); got != w {
						t.Errorf("year boundary %d = %s, want %s", i, got, w)
					}
				}
			})
		})
	})
}

func TestNextBoundaryAnchorRelation(t *testing.T) {
	anchor := utc(2026, time.March, 10, 9, 30)

	t.Run("Given a `from` before the anchor", func(t *testing.T) {
		t.Run("Then the anchor itself is the next boundary", func(t *testing.T) {
			got := NextBoundary(anchor, anchor.Add(-72*time.Hour), Monthly)
			if !got.Equal(anchor) {
				t.Errorf("next boundary = %s, want the anchor %s", got, anchor)
			}
		})
	})

	t.Run("Given a `from` a whole month before the anchor (negative step math)", func(t *testing.T) {
		// anchor Jan 31 2026, from Dec 20 2025: the next boundary is the
		// previous-year December instance, exercising the floored month/year
		// arithmetic for a negative step count.
		monthEndAnchor := utc(2026, time.January, 31, 0, 0)
		t.Run("Then the December boundary of the prior year is returned", func(t *testing.T) {
			got := NextBoundary(monthEndAnchor, utc(2025, time.December, 20, 0, 0), Monthly)
			if !got.Equal(utc(2025, time.December, 31, 0, 0)) {
				t.Errorf("next boundary = %s, want 2025-12-31", got)
			}
		})
	})

	t.Run("Given a `from` exactly on a boundary", func(t *testing.T) {
		t.Run("Then the strictly-later next boundary is returned", func(t *testing.T) {
			got := NextBoundary(anchor, anchor, Monthly)
			if !got.Equal(utc(2026, time.April, 10, 9, 30)) {
				t.Errorf("next boundary = %s, want 2026-04-10T09:30", got)
			}
		})
	})

	t.Run("Then the anchor's time of day and location are preserved", func(t *testing.T) {
		got := NextBoundary(anchor, anchor, Monthly)
		if got.Hour() != 9 || got.Minute() != 30 || got.Location() != anchor.Location() {
			t.Errorf("boundary lost its clock/location: %s", got)
		}
	})
}

func TestNextCalendarBoundary(t *testing.T) {
	from := utc(2026, time.March, 15, 12, 0)

	t.Run("Given a mid-month instant", func(t *testing.T) {
		t.Run("Then the next monthly calendar boundary is the 1st of next month", func(t *testing.T) {
			got := NextCalendarBoundary(from, Monthly)
			if !got.Equal(utc(2026, time.April, 1, 0, 0)) {
				t.Errorf("got %s, want 2026-04-01T00:00", got)
			}
		})
		t.Run("Then the next yearly calendar boundary is the following Jan 1", func(t *testing.T) {
			got := NextCalendarBoundary(from, Yearly)
			if !got.Equal(utc(2027, time.January, 1, 0, 0)) {
				t.Errorf("got %s, want 2027-01-01T00:00", got)
			}
		})
	})
}

func TestCivilDay(t *testing.T) {
	t.Run("Given the serial-day helper spanning year zero and into negative years", func(t *testing.T) {
		// The days_from_civil algorithm is documented valid across the full
		// range; exercise the negative-year branch and the proleptic-Gregorian
		// leap rule (year 0 is a leap year, year -1 is not).
		day := func(y int, m time.Month, d int) int64 {
			return civilDay(time.Date(y, m, d, 12, 0, 0, 0, time.UTC), time.UTC)
		}
		t.Run("Then a day is always one serial number after the day before it", func(t *testing.T) {
			if got := day(1, time.January, 1) - day(0, time.December, 31); got != 1 {
				t.Errorf("0000-12-31 to 0001-01-01 = %d days, want 1", got)
			}
			if got := day(0, time.January, 1) - day(-1, time.December, 31); got != 1 {
				t.Errorf("-0001-12-31 to 0000-01-01 = %d days, want 1", got)
			}
		})
		t.Run("Then year 0 spans 366 days (leap) and year -1 spans 365", func(t *testing.T) {
			if got := day(1, time.January, 1) - day(0, time.January, 1); got != 366 {
				t.Errorf("year 0 = %d days, want 366 (leap)", got)
			}
			if got := day(0, time.January, 1) - day(-1, time.January, 1); got != 365 {
				t.Errorf("year -1 = %d days, want 365", got)
			}
		})
	})
}

func TestCycleUnitBasisStrings(t *testing.T) {
	t.Run("Given the enums", func(t *testing.T) {
		t.Run("Then their String forms are stable, including the unknown fallbacks", func(t *testing.T) {
			cases := map[string]string{
				Monthly.String():         "monthly",
				Yearly.String():          "yearly",
				CycleUnit(9).String():    "CycleUnit(9)",
				ProrateBySecond.String(): "by-second",
				ProrateByDay.String():    "by-day",
				Basis(9).String():        "Basis(9)",
			}
			for got, want := range cases {
				if got != want {
					t.Errorf("String = %q, want %q", got, want)
				}
			}
		})
	})
}

// TestChangeRejectsNegativeAmounts pins that Change refuses a negative plan
// amount rather than silently inverting the Credit≤0 / Charge≥0 invariant.
func TestChangeRejectsNegativeAmounts(t *testing.T) {
	usd := USD(RoundHalfUp)
	p := Period{Start: utc(2027, time.Month(6), 1, 0, 0), End: utc(2027, time.Month(7), 1, 0, 0)}
	at := utc(2027, time.Month(6), 16, 0, 0)
	t.Run("Given a change with a negative amount", func(t *testing.T) {
		for _, tc := range []struct {
			name     string
			old, new int64
		}{
			{"negative old", -1000, 2000},
			{"negative new", 1000, -2000},
		} {
			t.Run("When "+tc.name, func(t *testing.T) {
				t.Run("Then it returns ErrNegativeAmount", func(t *testing.T) {
					if _, err := Change(tc.old, tc.new, usd, p, at, ProrateByDay); !errors.Is(err, ErrNegativeAmount) {
						t.Fatalf("err = %v, want ErrNegativeAmount", err)
					}
				})
			})
		}
	})
	t.Run("Given a legitimate downgrade", func(t *testing.T) {
		t.Run("When new < old, both non-negative", func(t *testing.T) {
			got, err := Change(2000, 1000, usd, p, at, ProrateByDay)
			t.Run("Then Net is a negative refund with Credit≤0 and Charge≥0", func(t *testing.T) {
				if err != nil {
					t.Fatal(err)
				}
				if got.Credit > 0 || got.Charge < 0 || got.Net >= 0 {
					t.Fatalf("downgrade = %+v, want Credit≤0, Charge≥0, Net<0", got)
				}
			})
		})
	})
}

// TestFractionPartitions pins the load-bearing invariant the design states but
// no test asserted directly: for any instant in the period, the day basis has
// used(Start,at) + remaining(at,End) == 1 exactly — including across a DST day.
func TestFractionPartitions(t *testing.T) {
	t.Run("Given a period and many split points", func(t *testing.T) {
		p := Period{Start: utc(2027, time.Month(3), 1, 0, 0), End: utc(2027, time.Month(4), 1, 0, 0)}
		for day := 1; day <= 31; day++ {
			at := utc(2027, time.March, day, 0, 0)
			used, err := p.Fraction(p.Start, at, ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			rem, err := p.Fraction(at, p.End, ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			sum := new(big.Rat).Add(used, rem)
			if sum.Cmp(big.NewRat(1, 1)) != 0 {
				t.Fatalf("day %d: used %s + remaining %s = %s, want exactly 1", day, used.RatString(), rem.RatString(), sum.RatString())
			}
		}
	})

	t.Run("Given a period spanning a DST spring-forward day", func(t *testing.T) {
		ny, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Skip("tzdata unavailable")
		}
		// March 2027: spring-forward is Mar 14 (a 23-hour civil day).
		p := Period{
			Start: time.Date(2027, 3, 1, 0, 0, 0, 0, ny),
			End:   time.Date(2027, 4, 1, 0, 0, 0, 0, ny),
		}
		for h := 0; h < 31*24; h += 5 {
			at := p.Start.Add(time.Duration(h) * time.Hour)
			used, err := p.Fraction(p.Start, at, ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			rem, err := p.Fraction(at, p.End, ProrateByDay)
			if err != nil {
				t.Fatal(err)
			}
			if new(big.Rat).Add(used, rem).Cmp(big.NewRat(1, 1)) != 0 {
				t.Fatalf("at %s: day-basis used+remaining ≠ 1 across DST", at)
			}
		}
	})
}
