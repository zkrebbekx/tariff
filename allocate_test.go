package tariff

import (
	"errors"
	"math/big"
	"testing"
)

// TestAllocateInternalGuards exercises the shared core's defensive guards
// directly, since the public Allocate screens most bad input before reaching
// them.
func TestAllocateInternalGuards(t *testing.T) {
	t.Run("Given the internal allocate core", func(t *testing.T) {
		t.Run("Then it rejects no parts and a negative weight", func(t *testing.T) {
			if _, err := allocate(10, nil); !errors.Is(err, ErrBadAllocation) {
				t.Errorf("no parts: err = %v", err)
			}
			if _, err := allocate(10, []*big.Int{big.NewInt(-1)}); !errors.Is(err, ErrBadAllocation) {
				t.Errorf("negative weight: err = %v", err)
			}
		})
		t.Run("Then a negative total is now allowed, not an error", func(t *testing.T) {
			if _, err := allocate(-1, []*big.Int{big.NewInt(1)}); err != nil {
				t.Errorf("negative total: err = %v, want nil", err)
			}
		})
	})
}

func TestCloneRatNil(t *testing.T) {
	t.Run("Given a nil rate", func(t *testing.T) {
		t.Run("Then cloneRat returns nil", func(t *testing.T) {
			if cloneRat(nil) != nil {
				t.Error("cloneRat(nil) != nil")
			}
		})
	})
}

func sumInt64(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

func TestAllocate(t *testing.T) {
	t.Run("Given a total that does not divide evenly", func(t *testing.T) {
		t.Run("When split across equal ratios", func(t *testing.T) {
			t.Run("Then equal largest remainders break ties by position, leftover to the first", func(t *testing.T) {
				got, err := Allocate(100, []int64{1, 1, 1})
				if err != nil {
					t.Fatal(err)
				}
				want := []int64{34, 33, 33}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("Allocate = %v, want %v", got, want)
					}
				}
			})
		})

		t.Run("When split across weighted ratios", func(t *testing.T) {
			t.Run("Then shares follow the weights and still sum to the total", func(t *testing.T) {
				// 62 across [105, 205, 305] mirrors the graduated reconciliation
				// path: floors 10/20/30 sum to 60; the 2 leftover units go to the
				// two parts with the largest proportional remainder (410 and 460,
				// parts 1 and 2), not round-robin from the first.
				got, err := Allocate(62, []int64{105, 205, 305})
				if err != nil {
					t.Fatal(err)
				}
				want := []int64{10, 21, 31}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("Allocate = %v, want %v", got, want)
					}
				}
				if sumInt64(got) != 62 {
					t.Fatalf("shares sum to %d, want 62", sumInt64(got))
				}
			})
		})
	})

	t.Run("Given ratios that are all zero", func(t *testing.T) {
		t.Run("When a non-zero total is allocated", func(t *testing.T) {
			t.Run("Then it falls back to an even split that loses nothing", func(t *testing.T) {
				got, err := Allocate(10, []int64{0, 0, 0})
				if err != nil {
					t.Fatal(err)
				}
				want := []int64{4, 3, 3}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("Allocate = %v, want %v", got, want)
					}
				}
			})
		})
	})

	t.Run("Given a ratio of zero among non-zero ratios", func(t *testing.T) {
		t.Run("When a total with a leftover is allocated", func(t *testing.T) {
			t.Run("Then the zero ratio receives zero, not a stray penny", func(t *testing.T) {
				// Regression: round-robin-from-first handed part 0 a penny here,
				// giving [1 3 3]. Largest-remainder gives the zero-weight part
				// its exact zero share.
				got, err := Allocate(7, []int64{0, 1, 1})
				if err != nil {
					t.Fatal(err)
				}
				want := []int64{0, 4, 3}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("Allocate(7, [0 1 1]) = %v, want %v", got, want)
					}
				}
			})
		})
	})

	t.Run("Given a zero total", func(t *testing.T) {
		t.Run("When allocated across parts", func(t *testing.T) {
			t.Run("Then every part is zero", func(t *testing.T) {
				got, err := Allocate(0, []int64{3, 1, 6})
				if err != nil {
					t.Fatal(err)
				}
				if sumInt64(got) != 0 {
					t.Fatalf("shares = %v, want all zero", got)
				}
			})
		})
	})

	t.Run("Given invalid inputs", func(t *testing.T) {
		cases := []struct {
			name   string
			total  int64
			ratios []int64
		}{
			{"no parts", 100, nil},
			{"negative ratio", 100, []int64{1, -1}},
		}
		for _, tc := range cases {
			t.Run("When allocating with "+tc.name, func(t *testing.T) {
				t.Run("Then it returns ErrBadAllocation", func(t *testing.T) {
					if _, err := Allocate(tc.total, tc.ratios); !errors.Is(err, ErrBadAllocation) {
						t.Errorf("error = %v, want ErrBadAllocation", err)
					}
				})
			})
		}
	})
}

func TestAllocateSigned(t *testing.T) {
	t.Run("Given a negative total (a proration credit)", func(t *testing.T) {
		t.Run("When split across weighted ratios", func(t *testing.T) {
			t.Run("Then the parts are the exact mirror of the positive split", func(t *testing.T) {
				pos, err := Allocate(62, []int64{105, 205, 305})
				if err != nil {
					t.Fatal(err)
				}
				neg, err := Allocate(-62, []int64{105, 205, 305})
				if err != nil {
					t.Fatal(err)
				}
				for i := range pos {
					if neg[i] != -pos[i] {
						t.Fatalf("Allocate(-62) = %v, want negation of %v", neg, pos)
					}
				}
			})
			t.Run("Then the parts sum exactly to the negative total", func(t *testing.T) {
				got, err := Allocate(-62, []int64{105, 205, 305})
				if err != nil {
					t.Fatal(err)
				}
				if s := sumInt64(got); s != -62 {
					t.Fatalf("shares %v sum to %d, want -62", got, s)
				}
			})
		})

		t.Run("When a zero weight sits among the ratios", func(t *testing.T) {
			t.Run("Then the zero weight receives exactly zero, not a stray negative penny", func(t *testing.T) {
				got, err := Allocate(-7, []int64{0, 1, 1})
				if err != nil {
					t.Fatal(err)
				}
				want := []int64{0, -4, -3}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("Allocate(-7, [0 1 1]) = %v, want %v", got, want)
					}
				}
			})
		})
	})

	t.Run("Given the existing positive behavior", func(t *testing.T) {
		t.Run("Then it is unchanged by lifting the sign restriction", func(t *testing.T) {
			got, err := Allocate(100, []int64{1, 1, 1})
			if err != nil {
				t.Fatal(err)
			}
			want := []int64{34, 33, 33}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("Allocate(100, [1 1 1]) = %v, want %v", got, want)
				}
			}
		})
	})
}

func TestAllocateProperties(t *testing.T) {
	t.Run("Given a spread of totals and ratio sets", func(t *testing.T) {
		cases := []struct {
			total  int64
			ratios []int64
		}{
			{7, []int64{1, 1, 1}},
			{1, []int64{1, 1, 1, 1, 1}},
			{100, []int64{2, 3, 5}},
			{9999, []int64{1, 7, 3, 11}},
			{5, []int64{10, 0, 0}},
		}
		for _, tc := range cases {
			t.Run("When split", func(t *testing.T) {
				got, err := Allocate(tc.total, tc.ratios)
				if err != nil {
					t.Fatal(err)
				}
				t.Run("Then the parts sum exactly to the total", func(t *testing.T) {
					if s := sumInt64(got); s != tc.total {
						t.Errorf("sum = %d, want %d (ratios %v)", s, tc.total, tc.ratios)
					}
				})
				t.Run("Then the split is deterministic", func(t *testing.T) {
					again, err := Allocate(tc.total, tc.ratios)
					if err != nil {
						t.Fatal(err)
					}
					for i := range got {
						if got[i] != again[i] {
							t.Fatalf("non-deterministic: %v vs %v", got, again)
						}
					}
				})
				t.Run("Then every part is non-negative", func(t *testing.T) {
					for i, p := range got {
						if p < 0 {
							t.Errorf("part %d = %d is negative", i, p)
						}
					}
				})
			})
		}
	})
}
