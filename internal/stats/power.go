package stats

import "math"

// DefaultPower is the conventional statistical power (1 - beta) used when
// sizing an experiment: an 80% chance of detecting an effect that is really
// there. It is the counterpart to DefaultAlpha on the error side.
const DefaultPower = 0.80

// Mean returns the arithmetic mean of xs, or 0 for an empty slice.
func Mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var total float64
	for _, x := range xs {
		total += x
	}
	return total / float64(len(xs))
}

// StdDev returns the sample standard deviation of xs, using the n-1
// denominator so the estimate is unbiased for the population spread.
//
// ok is false for fewer than two observations. A spread read from a single
// sample is not an estimate, and returning one would let a lone run masquerade
// as a measured noise floor — which is the mistake this whole report exists to
// prevent.
func StdDev(xs []float64) (float64, bool) {
	if len(xs) < 2 {
		return 0, false
	}
	mean := Mean(xs)
	var sum float64
	for _, x := range xs {
		d := x - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(xs)-1)), true
}

// ZFor returns the standard normal quantile for a one-sided tail probability p:
// the value z with P(Z <= z) = p. It uses the Acklam rational approximation,
// which is accurate to about 1e-9 over the open interval (0,1) — far tighter
// than the spread estimates it multiplies. The extremes return infinities rather
// than a silently wrong finite number.
func ZFor(p float64) float64 {
	if p <= 0 {
		return math.Inf(-1)
	}
	if p >= 1 {
		return math.Inf(1)
	}
	// Coefficients of the Acklam approximation.
	a := [6]float64{
		-3.969683028665376e+01, 2.209460984245205e+02, -2.759285104469687e+02,
		1.383577518672690e+02, -3.066479806614716e+01, 2.506628277459239e+00,
	}
	b := [5]float64{
		-5.447609879822406e+01, 1.615858368580409e+02, -1.556989798598866e+02,
		6.680131188771972e+01, -1.328068155288572e+01,
	}
	c := [6]float64{
		-7.784894002430293e-03, -3.223964580411365e-01, -2.400758277161838e+00,
		-2.549732539343734e+00, 4.374664141464968e+00, 2.938163982698783e+00,
	}
	d := [4]float64{
		7.784695709041462e-03, 3.224671290700398e-01,
		2.445134137142996e+00, 3.754408661907416e+00,
	}
	const pLow = 0.02425
	const pHigh = 1 - pLow

	switch {
	case p < pLow:
		q := math.Sqrt(-2 * math.Log(p))
		return (((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	case p > pHigh:
		q := math.Sqrt(-2 * math.Log(1-p))
		return -(((((c[0]*q+c[1])*q+c[2])*q+c[3])*q+c[4])*q + c[5]) /
			((((d[0]*q+d[1])*q+d[2])*q+d[3])*q + 1)
	default:
		q := p - 0.5
		r := q * q
		return (((((a[0]*r+a[1])*r+a[2])*r+a[3])*r+a[4])*r + a[5]) * q /
			(((((b[0]*r+b[1])*r+b[2])*r+b[3])*r+b[4])*r + 1)
	}
}

// MDE returns the smallest true difference in means that an experiment with
// nPerArm observations per arm would detect with the given power at a two-sided
// alpha, given the spread observed in samples.
//
// The result is in the units of samples, so a caller converts it to a relative
// figure by dividing by Mean(samples). ok is false when the spread cannot be
// estimated (fewer than two observations) or when no repeats are planned: an
// experiment with one observation per arm detects nothing.
func MDE(samples []float64, nPerArm int, power, alpha float64) (float64, bool) {
	sd, ok := StdDev(samples)
	if !ok || nPerArm < 1 {
		return 0, false
	}
	power, alpha = normalizePower(power, alpha)
	z := ZFor(1-alpha/2) + ZFor(power)
	// Standard error of the difference between two equal arms.
	return z * sd * math.Sqrt(2/float64(nPerArm)), true
}

// RepeatsFor returns the repeats per arm needed to detect an absolute effect
// given the spread observed in samples, rounding up to the next whole run.
//
// ok is false when the effect is not positive, when the spread cannot be
// estimated, or when the observed spread is exactly zero. Zero observed spread
// is not evidence that a metric is noiseless — it is the signature of a handful
// of identical small samples — and sizing an experiment on it would promise a
// detection that a second run can easily contradict.
func RepeatsFor(samples []float64, effect, power, alpha float64) (int, bool) {
	sd, ok := StdDev(samples)
	if !ok {
		return 0, false
	}
	return RepeatsForSD(sd, effect, power, alpha)
}

// RepeatsForSD is RepeatsFor for a caller that has already estimated the
// standard deviation, which is what a pooled within-group spread is. Passing
// the concatenated samples instead would understate the spread whenever more
// than one group contributes, because duplicating each group's deviations
// inflates the denominator without adding independent information.
func RepeatsForSD(sd, effect, power, alpha float64) (int, bool) {
	if effect <= 0 || sd <= 0 {
		return 0, false
	}
	power, alpha = normalizePower(power, alpha)
	z := ZFor(1-alpha/2) + ZFor(power)
	n := int(math.Ceil(2 * math.Pow(z*sd/effect, 2)))
	if n < 1 {
		n = 1
	}
	return n, true
}

// normalizePower replaces out-of-range power and alpha with their defaults, so
// a caller cannot turn a bad argument into a NaN that propagates into a report.
func normalizePower(power, alpha float64) (float64, float64) {
	if power <= 0 || power >= 1 {
		power = DefaultPower
	}
	if alpha <= 0 || alpha >= 1 {
		alpha = DefaultAlpha
	}
	return power, alpha
}

// MDEForRelSD returns the detectable relative effect for a metric whose pooled
// relative standard deviation is relSD, when an experiment runs nPerArm
// observations per arm. It is the relative form of MDE, for callers that have
// already normalised a metric by its mean — which is how the variance report
// compares metrics measured on different scales.
func MDEForRelSD(relSD float64, nPerArm int, power, alpha float64) float64 {
	if relSD <= 0 || nPerArm < 1 {
		return 0
	}
	power, alpha = normalizePower(power, alpha)
	return (ZFor(1-alpha/2) + ZFor(power)) * relSD * math.Sqrt(2/float64(nPerArm))
}
