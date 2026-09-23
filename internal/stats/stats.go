// Package stats provides deterministic, pure statistical helpers used by the
// ocbench experiments report. It performs no I/O and depends only on the
// standard library.
package stats

import (
	"math"
	"math/rand"
	"sort"
)

// DefaultAlpha is the conventional significance level used for interval and
// p-value reporting.
const DefaultAlpha = 0.05

// Wilson returns the two-sided Wilson score interval for a binomial proportion
// with successes out of n trials at confidence level z. The interval is
// computed from the score statistic:
//
//	centre = (p + z²/2n) / (1 + z²/n)
//	half   = z/(1+z²/n) * sqrt(p(1-p)/n + z²/4n²)
//
// where p = successes/n. The result is clamped to [0,1]. For n == 0 there is no
// information, so the maximally wide interval (0,1) is returned.
func Wilson(successes, n int, z float64) (lo, hi float64) {
	if n <= 0 {
		return 0, 1
	}
	p := float64(successes) / float64(n)
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	nf := float64(n)
	z2 := z * z
	denom := 1 + z2/nf
	centre := (p + z2/(2*nf)) / denom
	half := z / denom * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf))
	lo = math.Max(0, centre-half)
	hi = math.Min(1, centre+half)
	return lo, hi
}

// PassAtK returns 1 when any repeat passed, else 0. It is the pass@k criterion
// (at least one success in k attempts).
func PassAtK(outcomes []bool) int {
	for _, ok := range outcomes {
		if ok {
			return 1
		}
	}
	return 0
}

// PassAllK returns 1 when every repeat passed, else 0. A run with no repeats
// has no evidence of passing and returns 0 rather than the vacuous truth.
func PassAllK(outcomes []bool) int {
	if len(outcomes) == 0 {
		return 0
	}
	for _, ok := range outcomes {
		if !ok {
			return 0
		}
	}
	return 1
}

// Median returns the median of xs. The input slice is copied before sorting so
// callers' slices are never mutated. An empty slice yields 0.
func Median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// IQR returns the first and third quartiles of xs using the median of the lower
// and upper halves (the middle element is excluded for odd lengths). The input
// slice is not mutated. An empty slice yields (0,0).
func IQR(xs []float64) (q1, q3 float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	sorted := append([]float64(nil), xs...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n == 1 {
		return sorted[0], sorted[0]
	}
	return Median(sorted[:n/2]), Median(sorted[(n+1)/2:])
}

// PermutationP returns the two-sided permutation-test p-value for the
// difference in means between a and b. It pools both samples, then for iters
// iterations resamples two groups of the original sizes and counts how often
// the absolute resampled mean difference is at least the observed absolute mean
// difference. The result is (count+1)/(iters+1), so it is never exactly zero.
//
// Randomness comes from math/rand.New(rand.NewSource(seed)), so identical
// inputs and seed produce identical results across runs and machines.
func PermutationP(a, b []float64, seed int64, iters int) float64 {
	if len(a) == 0 || len(b) == 0 || iters <= 0 {
		return 1
	}
	observed := math.Abs(mean(a) - mean(b))
	pooled := make([]float64, 0, len(a)+len(b))
	pooled = append(pooled, a...)
	pooled = append(pooled, b...)

	rng := rand.New(rand.NewSource(seed))
	scratch := make([]float64, len(pooled))
	count := 0
	for i := 0; i < iters; i++ {
		copy(scratch, pooled)
		rng.Shuffle(len(scratch), func(x, y int) {
			scratch[x], scratch[y] = scratch[y], scratch[x]
		})
		groupA := scratch[:len(a)]
		groupB := scratch[len(a):]
		if math.Abs(mean(groupA)-mean(groupB)) >= observed {
			count++
		}
	}
	return float64(count+1) / float64(iters+1)
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}
