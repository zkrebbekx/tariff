package tariff_test

import (
	"fmt"
	"math/big"
	"time"

	"github.com/zkrebbekx/tariff"
)

// Example rates six units on a graduated schedule: 1-5 @ $7, 6-10 @ $6.50,
// 11+ @ $6.
func Example() {
	c := tariff.Charge{
		Model:    tariff.Graduated,
		Currency: tariff.USD(tariff.RoundHalfUp),
		Tiers: []tariff.Tier{
			{UpTo: 5, UnitRate: big.NewRat(7, 1)},
			{UpTo: 10, UnitRate: big.NewRat(13, 2)}, // $6.50
			{Last: true, UnitRate: big.NewRat(6, 1)},
		},
	}

	res, _ := c.Rate(6)
	fmt.Printf("total %s across %d lines\n", c.Currency.Format(res.Total), len(res.Lines))
	// Output: total 41.50 across 2 lines
}

// Example_volume charges the whole quantity at the single rate of the tier it
// lands in, which is why six units cost 6 x $6.50 rather than the graduated mix.
func Example_volume() {
	c := tariff.Charge{
		Model:    tariff.Volume,
		Currency: tariff.USD(tariff.RoundHalfUp),
		Tiers: []tariff.Tier{
			{UpTo: 5, UnitRate: big.NewRat(7, 1)},
			{UpTo: 10, UnitRate: big.NewRat(13, 2)},
			{Last: true, UnitRate: big.NewRat(6, 1)},
		},
	}

	res, _ := c.Rate(6)
	fmt.Println(c.Currency.Format(res.Total))
	// Output: 39.00
}

// Example_package rounds the chargeable quantity up to whole blocks after the
// free allowance: 201 - 100 free = 101 units, which is two $5 blocks.
func Example_package() {
	c := tariff.Charge{
		Model:         tariff.Package,
		Currency:      tariff.USD(tariff.RoundHalfUp),
		PackageSize:   100,
		PackagePrice:  500, // $5.00
		FreeAllowance: 100,
	}

	res, _ := c.Rate(201)
	fmt.Println(c.Currency.Format(res.Total))
	// Output: 10.00
}

// Example_reconciliation shows that tier lines are allocated from the
// once-rounded total, so they reconcile exactly even when each tier ends in
// half a minor unit.
func Example_reconciliation() {
	c := tariff.Charge{
		Model:    tariff.Graduated,
		Currency: tariff.USD(tariff.RoundHalfUp),
		Tiers: []tariff.Tier{
			{UpTo: 1, UnitRate: big.NewRat(21, 200)},    // $0.105
			{UpTo: 2, UnitRate: big.NewRat(41, 200)},    // $0.205
			{Last: true, UnitRate: big.NewRat(61, 200)}, // $0.305
		},
	}

	res, _ := c.Rate(3)
	var sum int64
	for _, l := range res.Lines {
		sum += l.Subtotal
	}
	fmt.Printf("total=%d lines=%d+%d+%d sum=%d\n",
		res.Total, res.Lines[0].Subtotal, res.Lines[1].Subtotal, res.Lines[2].Subtotal, sum)
	// Output: total=62 lines=10+21+31 sum=62
}

// Example_allocate splits a rounded total across parts by ratio, losing
// nothing: with equal ratios the leftover cent goes to the first part.
func Example_allocate() {
	shares, _ := tariff.Allocate(100, []int64{1, 1, 1})
	fmt.Println(shares)
	// Output: [34 33 33]
}

// Example_proration reproduces the Stripe mid-period upgrade: a $10 plan
// upgraded to $20 exactly halfway through a period is credited $5 for the
// unused old plan and charged $10 for the remaining new plan, netting $5.
func Example_proration() {
	usd := tariff.USD(tariff.RoundHalfUp)
	p := tariff.Period{
		Start: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC),
	}
	at := time.Date(2026, time.January, 16, 0, 0, 0, 0, time.UTC)

	pr, _ := tariff.Change(1000, 2000, usd, p, at, tariff.ProrateBySecond)
	fmt.Printf("credit %s, charge %s, net %s\n",
		usd.Format(pr.Credit), usd.Format(pr.Charge), usd.Format(pr.Net))
	// Output: credit -5.00, charge 10.00, net 5.00
}

// Example_compose shows that the order of steps is the caller's and is visible
// in the result: discounting a $100 charge by 10% then applying a $95 minimum
// floors the $90 back up to $95.
func Example_compose() {
	usd := tariff.USD(tariff.RoundHalfUp)
	c := tariff.Charge{Model: tariff.PerUnit, Currency: usd, UnitRate: big.NewRat(100, 1)}

	inv, _ := tariff.Compose(usd,
		tariff.Charged(c, 1),
		tariff.PercentOff(big.NewRat(1, 10), "10% off"),
		tariff.MinimumCharge(9500, "minimum $95"),
	)
	fmt.Println(usd.Format(inv.Total))
	// Output: 95.00
}
