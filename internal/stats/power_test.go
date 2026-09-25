package stats

import (
	"math"
	"testing"
)

func TestZForMatchesKnownNormalQuantiles(t *testing.T) {
	// Textbook quantiles for the one-sided tail probabilities the power
	// calculation uses.
	for _, tc := range []struct {
		p    float64
		want float64
	}{
		{0.975, 1.959964},
		{0.95, 1.644854},
		{0.80, 0.841621},
		{0.90, 1.281552},
		{0.50, 0},
	} {
		approx(t, "ZFor", ZFor(tc.p), tc.want)
	}
	// The approximation is symmetric about the median.
	approx(t, "ZFor symmetric", ZFor(0.2), -ZFor(0.8))
	approx(t, "ZFor symmetric", ZFor(0.025), -ZFor(0.975))
}

func TestMeanAndStdDev(t *testing.T) {
	approx(t, "Mean", Mean([]float64{1, 2, 3}), 2)
	approx(t, "Mean empty", Mean(nil), 0)

	// Sample standard deviation uses the n-1 denominator.
	sd, ok := StdDev([]float64{90, 100, 110})
	if !ok {
		t.Fatal("StdDev of three observations reported no estimate")
	}
	approx(t, "StdDev", sd, 10)

	if sd, ok := StdDev([]float64{5, 5, 5}); !ok || sd != 0 {
		t.Errorf("StdDev of a constant = %v, %v; want 0, true", sd, ok)
	}
	// A spread from fewer than two observations is not an estimate.
	if _, ok := StdDev([]float64{5}); ok {
		t.Error("StdDev of one observation claimed an estimate")
	}
	if _, ok := StdDev(nil); ok {
		t.Error("StdDev of no observations claimed an estimate")
	}
}

func TestMDEAtObservedSpread(t *testing.T) {
	// mean 100, sample SD 10. With 5 per arm the standard error of the
	// difference is 10*sqrt(2/5), and the 80%-power factor is
	// z(0.975)+z(0.80) = 2.8016.
	samples := []float64{90, 100, 110}
	mde, ok := MDE(samples, 5, DefaultPower, DefaultAlpha)
	if !ok {
		t.Fatal("MDE reported no estimate for a 3-sample spread")
	}
	approx(t, "MDE", mde, 2.8016*10*math.Sqrt(2.0/5.0))

	// More repeats shrink the detectable effect, by sqrt of the ratio.
	more, _ := MDE(samples, 20, DefaultPower, DefaultAlpha)
	approx(t, "MDE at 4x repeats", more, mde/2)

	// No spread estimate means no detectable-effect estimate.
	if _, ok := MDE([]float64{1}, 5, DefaultPower, DefaultAlpha); ok {
		t.Error("MDE claimed an estimate from one observation")
	}
	if _, ok := MDE(samples, 0, DefaultPower, DefaultAlpha); ok {
		t.Error("MDE claimed an estimate with no repeats per arm")
	}
}

func TestRepeatsForInvertsMDE(t *testing.T) {
	samples := []float64{90, 100, 110} // mean 100, SD 10
	// A 10% effect on a mean of 100 is 10 absolute.
	n, ok := RepeatsFor(samples, 10, DefaultPower, DefaultAlpha)
	if !ok {
		t.Fatal("RepeatsFor reported no estimate")
	}
	// 2*(2.8016*10/10)^2 = 15.7, rounded up.
	if n != 16 {
		t.Errorf("RepeatsFor(10%% effect) = %d, want 16", n)
	}

	// Detecting a smaller effect needs more repeats, monotonically.
	smaller, _ := RepeatsFor(samples, 5, DefaultPower, DefaultAlpha)
	if smaller <= n {
		t.Errorf("a 5%% effect needs %d repeats, want more than %d", smaller, n)
	}

	// A zero or negative effect is not detectable at any sample size.
	if _, ok := RepeatsFor(samples, 0, DefaultPower, DefaultAlpha); ok {
		t.Error("RepeatsFor accepted a zero effect")
	}
	if _, ok := RepeatsFor([]float64{1}, 10, DefaultPower, DefaultAlpha); ok {
		t.Error("RepeatsFor claimed an estimate from one observation")
	}
}

// The two helpers must agree: the repeats RepeatsFor asks for must be enough
// for MDE to report the effect back.
func TestMDEAndRepeatsAgree(t *testing.T) {
	samples := []float64{88, 97, 103, 112}
	const effect = 12
	n, ok := RepeatsFor(samples, effect, DefaultPower, DefaultAlpha)
	if !ok {
		t.Fatal("RepeatsFor reported no estimate")
	}
	mde, ok := MDE(samples, n, DefaultPower, DefaultAlpha)
	if !ok {
		t.Fatal("MDE reported no estimate")
	}
	if mde > effect+1e-9 {
		t.Errorf("MDE at the requested %d repeats = %v, want at most %v", n, mde, effect)
	}
	// One fewer repeat must fall short, or RepeatsFor is overstating the need.
	if mdeBefore, _ := MDE(samples, n-1, DefaultPower, DefaultAlpha); mdeBefore <= effect {
		t.Errorf("MDE at %d repeats = %v, want more than %v", n-1, mdeBefore, effect)
	}
}

func TestMDEForRelSDAgreesWithMDE(t *testing.T) {
	// A metric with a 10% relative spread and 5 repeats per arm: the absolute
	// MDE from the same spread must match the relative form times the mean.
	samples := []float64{90, 100, 110}
	rel := MDEForRelSD(0.10, 5, DefaultPower, DefaultAlpha)
	abs, ok := MDE(samples, 5, DefaultPower, DefaultAlpha)
	if !ok {
		t.Fatal("MDE reported no estimate")
	}
	approx(t, "MDEForRelSD vs MDE", rel*Mean(samples), abs)

	// No spread, or no repeats, means no detectable-effect estimate.
	if got := MDEForRelSD(0, 5, DefaultPower, DefaultAlpha); got != 0 {
		t.Errorf("MDEForRelSD(0) = %v, want 0", got)
	}
	if got := MDEForRelSD(0.1, 0, DefaultPower, DefaultAlpha); got != 0 {
		t.Errorf("MDEForRelSD(n=0) = %v, want 0", got)
	}
}
