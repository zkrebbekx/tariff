package tariff

import (
	"math/big"
	"testing"
)

var (
	benchResult Result
	benchShares []int64
)

// manyTiers builds a graduated schedule of n bands, each 100 units wide, with a
// gently varying rate, plus an unbounded final band.
func manyTiers(n int) []Tier {
	tiers := make([]Tier, 0, n)
	for i := 0; i < n-1; i++ {
		tiers = append(tiers, Tier{
			UpTo:     int64((i + 1) * 100),
			UnitRate: big.NewRat(int64(100+(i%7)), 100), // $1.00 .. $1.06
		})
	}
	tiers = append(tiers, Tier{Last: true, UnitRate: big.NewRat(90, 100)})
	return tiers
}

func BenchmarkGraduatedManyTiers(b *testing.B) {
	c := Charge{Model: Graduated, Currency: USD(RoundHalfUp), Tiers: manyTiers(100)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchResult, _ = c.Rate(9950) // spans nearly all bands
	}
}

func BenchmarkVolume(b *testing.B) {
	c := Charge{Model: Volume, Currency: USD(RoundHalfUp), Tiers: manyTiers(100)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchResult, _ = c.Rate(9950)
	}
}

func BenchmarkPerUnit(b *testing.B) {
	c := Charge{Model: PerUnit, Currency: USD(RoundHalfUp), UnitRate: big.NewRat(6, 10000)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchResult, _ = c.Rate(65000)
	}
}

func BenchmarkPackage(b *testing.B) {
	c := Charge{Model: Package, Currency: USD(RoundHalfUp), PackageSize: 100, PackagePrice: 500, FreeAllowance: 100}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchResult, _ = c.Rate(1_000_001)
	}
}

func BenchmarkAllocate(b *testing.B) {
	ratios := []int64{105, 205, 305, 55, 990, 12}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchShares, _ = Allocate(100000, ratios)
	}
}
