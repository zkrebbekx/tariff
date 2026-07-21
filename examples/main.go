// Command examples rates a small mixed invoice with tariff, showing each
// rating model, exact sub-cent arithmetic, per-currency minor units, and
// line-item reconciliation.
package main

import (
	"fmt"
	"math/big"
	"os"

	"github.com/zkrebbekx/tariff"
)

func main() {
	usd := tariff.USD(tariff.RoundHalfUp)

	// A mixed plan: a graduated API-call charge, a package data charge with a
	// free allowance, and a fixed platform fee folded into the per-unit line.
	charges := []struct {
		label string
		c     tariff.Charge
		qty   int64
	}{
		{
			label: "API calls (graduated)",
			qty:   6,
			c: tariff.Charge{
				Model:    tariff.Graduated,
				Currency: usd,
				Tiers: []tariff.Tier{
					{UpTo: 5, UnitRate: big.NewRat(7, 1)},
					{UpTo: 10, UnitRate: big.NewRat(13, 2)}, // $6.50
					{Last: true, UnitRate: big.NewRat(6, 1)},
				},
			},
		},
		{
			label: "Bandwidth (volume)",
			qty:   6,
			c: tariff.Charge{
				Model:    tariff.Volume,
				Currency: usd,
				Tiers: []tariff.Tier{
					{UpTo: 5, UnitRate: big.NewRat(7, 1)},
					{UpTo: 10, UnitRate: big.NewRat(13, 2)},
					{Last: true, UnitRate: big.NewRat(6, 1)},
				},
			},
		},
		{
			label: "Storage (package, 100 free)",
			qty:   201,
			c: tariff.Charge{
				Model:         tariff.Package,
				Currency:      usd,
				PackageSize:   100,
				PackagePrice:  500,
				FreeAllowance: 100,
			},
		},
		{
			label: "Metered events + platform fee (per-unit)",
			qty:   65000,
			c: tariff.Charge{
				Model:    tariff.PerUnit,
				Currency: usd,
				UnitRate: big.NewRat(6, 10000), // $0.0006, exact
				FlatFee:  1000,                 // $10.00 fixed
			},
		},
	}

	var invoice int64
	for _, ch := range charges {
		res, err := ch.c.Rate(ch.qty)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rate %q: %v\n", ch.label, err)
			os.Exit(1)
		}
		fmt.Printf("%-42s $%s\n", ch.label, usd.Format(res.Total))
		for _, l := range res.Lines {
			if l.Rate != nil {
				fmt.Printf("    %6d @ %-8s = $%s\n", l.Quantity, l.Rate.FloatString(4), usd.Format(l.Subtotal))
			} else {
				fmt.Printf("    %6d units/blocks       = $%s\n", l.Quantity, usd.Format(l.Subtotal))
			}
		}
		invoice += res.Total
	}
	fmt.Printf("%-42s $%s\n", "TOTAL", usd.Format(invoice))

	// Zero-decimal and three-decimal currencies round on their own minor unit.
	jpy := tariff.Charge{Model: tariff.PerUnit, Currency: tariff.JPY(tariff.RoundHalfUp), UnitRate: big.NewRat(201, 2)}
	kwd := tariff.Charge{Model: tariff.PerUnit, Currency: tariff.KWD(tariff.RoundHalfUp), UnitRate: big.NewRat(12345, 10000)}
	rj, _ := jpy.Rate(1)
	rk, _ := kwd.Rate(1)
	fmt.Printf("\nJPY 100.5/unit  -> %s yen\n", tariff.JPY(tariff.RoundHalfUp).Format(rj.Total))
	fmt.Printf("KWD 1.2345/unit -> %s dinar\n", tariff.KWD(tariff.RoundHalfUp).Format(rk.Total))

	// Allocation splits a rounded total with no lost minor units.
	shares, _ := tariff.Allocate(100, []int64{1, 1, 1})
	fmt.Printf("\nAllocate(100, 1:1:1) = %v (sums to 100)\n", shares)
}
