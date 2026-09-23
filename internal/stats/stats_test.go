package stats

import (
	"math"
	"testing"
)

const tol = 1e-3

func approx(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (±%v)", name, got, want, tol)
	}
}

func TestWilsonGolden(t *testing.T) {
	lo, hi := Wilson(0, 5, 1.96)
	approx(t, "Wilson(0,5,1.96) lo", lo, 0)
	approx(t, "Wilson(0,5,1.96) hi", hi, 0.434)

	lo, hi = Wilson(5, 5, 1.96)
	approx(t, "Wilson(5,5,1.96) lo", lo, 0.566)
	approx(t, "Wilson(5,5,1.96) hi", hi, 1)
}

func TestWilsonZeroN(t *testing.T) {
	lo, hi := Wilson(0, 0, 1.96)
	if lo != 0 || hi != 1 {
		t.Fatalf("Wilson(0,0,1.96) = (%v,%v), want (0,1)", lo, hi)
	}
}

func TestPassAtKPassAllK(t *testing.T) {
	cases := []struct {
		name      string
		outcomes  []bool
		atK, allK int
	}{
		{"all pass", []bool{true, true, true}, 1, 1},
		{"mixed", []bool{true, false, false}, 1, 0},
		{"all fail", []bool{false, false}, 0, 0},
		{"empty", nil, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PassAtK(tc.outcomes); got != tc.atK {
				t.Errorf("PassAtK(%v) = %d, want %d", tc.outcomes, got, tc.atK)
			}
			if got := PassAllK(tc.outcomes); got != tc.allK {
				t.Errorf("PassAllK(%v) = %d, want %d", tc.outcomes, got, tc.allK)
			}
		})
	}
}

func TestMedian(t *testing.T) {
	cases := []struct {
		name string
		in   []float64
		want float64
	}{
		{"odd", []float64{3, 1, 2}, 2},
		{"even", []float64{4, 1, 3, 2}, 2.5},
		{"unsorted", []float64{5, 3, 9, 1}, 4},
		{"single", []float64{7}, 7},
		{"empty", []float64{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Median(tc.in); got != tc.want {
				t.Errorf("Median(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestMedianDoesNotMutate(t *testing.T) {
	in := []float64{5, 3, 9, 1}
	before := append([]float64(nil), in...)
	_ = Median(in)
	for i := range in {
		if in[i] != before[i] {
			t.Fatalf("Median mutated input: got %v, want %v", in, before)
		}
	}
}

func TestIQR(t *testing.T) {
	q1, q3 := IQR([]float64{1, 2, 3, 4, 5, 6, 7, 8})
	approx(t, "IQR q1", q1, 2.5)
	approx(t, "IQR q3", q3, 6.5)
}

func TestPermutationPIdentical(t *testing.T) {
	s := []float64{1, 2, 3, 4, 5}
	if got := PermutationP(s, s, 1, 2000); got <= 0.9 {
		t.Fatalf("PermutationP identical = %v, want > 0.9", got)
	}
}

func TestPermutationPSeparated(t *testing.T) {
	a := make([]float64, 20)
	b := make([]float64, 20)
	for i := range a {
		b[i] = 100
	}
	if got := PermutationP(a, b, 1, 1000); got >= 0.05 {
		t.Fatalf("PermutationP separated = %v, want < 0.05", got)
	}
}

func TestPermutationPDeterministic(t *testing.T) {
	a := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	b := []float64{1.1, 2.1, 3.1, 4.1, 5.1, 6.1, 7.1, 8.1}
	first := PermutationP(a, b, 42, 10000)
	second := PermutationP(a, b, 42, 10000)
	if first != second {
		t.Fatalf("same seed gave %v then %v", first, second)
	}
	other := PermutationP(a, b, 43, 10000)
	if other == first {
		t.Fatalf("different seed gave same value %v", other)
	}
}

func TestDefaultAlpha(t *testing.T) {
	if DefaultAlpha != 0.05 {
		t.Fatalf("DefaultAlpha = %v, want 0.05", DefaultAlpha)
	}
}
