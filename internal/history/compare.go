package history

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
)

// MetricDelta is one metric's change from Before to After. Percent is nil when
// the Before value is zero, so a zero baseline never divides by zero. A metric
// present on only one side is treated as zero on the missing side.
type MetricDelta struct {
	Name    string
	Before  float64
	After   float64
	Delta   float64
	Percent *float64
}

// Comparison is the deterministic result of comparing two runs. Before is the
// run selected by the left selector and After the run selected by the right
// selector. Metrics and ProfileChanges are never nil. ControlledRunWarning is
// non-empty only when more than one profile component changed; it states the
// count and recommends a controlled comparison without claiming causation.
type Comparison struct {
	Before               RunDetail
	After                RunDetail
	Metrics              []MetricDelta
	ProfileChanges       []profile.Change
	ControlledRunWarning string
}

// Compare resolves left and right (a full UUID, "latest" or "previous") and
// returns the comparison. "previous" always selects the Before side and the
// selector it is relative to selects After, so "latest previous" and
// "previous latest" resolve to the same ordered pair and deltas are always
// After-minus-Before. Otherwise left maps to Before and right to After. Using
// "previous" for both selectors is a usage error. A dry run on either resolved
// side is rejected as a usage error, as are explicit runs that are not
// mutually compatible (different suite name/version/hash, task id/version or
// fixture); such pairs are never presented as benchmark deltas. A missing run
// surfaces the store's wrapped sql.ErrNoRows.
func Compare(ctx context.Context, st *store.Store, left, right string) (Comparison, error) {
	if st == nil {
		return Comparison{}, errors.New("history: nil store")
	}
	if left == SelectorPrevious && right == SelectorPrevious {
		return Comparison{}, fmt.Errorf("%w: both selectors cannot be %q", ErrSelector, SelectorPrevious)
	}

	leftRun, rightRun, err := resolvePair(ctx, st, left, right)
	if err != nil {
		return Comparison{}, err
	}
	// A dry run is never a benchmark result. The latest/previous selectors
	// already exclude dry runs, but an explicit UUID can still name one.
	if leftRun.DryRun || rightRun.DryRun {
		return Comparison{}, fmt.Errorf("%w: dry runs cannot be compared", ErrSelector)
	}
	// "previous" is compatible by construction; only explicit/independent
	// selections need the mutual-compatibility check.
	if left != SelectorPrevious && right != SelectorPrevious && !compatible(*leftRun, *rightRun) {
		return Comparison{}, fmt.Errorf("%w: runs are not compatible; suite, task and fixture must match", ErrSelector)
	}

	before, err := loadDetail(ctx, st, *leftRun)
	if err != nil {
		return Comparison{}, err
	}
	after, err := loadDetail(ctx, st, *rightRun)
	if err != nil {
		return Comparison{}, err
	}

	changes := profile.Diff(before.Profile, after.Profile)
	if changes == nil {
		changes = make([]profile.Change, 0)
	}

	return Comparison{
		Before:               before,
		After:                after,
		Metrics:              metricDeltas(before.Metrics, after.Metrics),
		ProfileChanges:       changes,
		ControlledRunWarning: controlledRunWarning(len(changes)),
	}, nil
}

// resolvePair resolves both selectors. When one side is "previous", the other
// side is resolved first as the anchor and "previous" is resolved relative to
// it. "previous" is always returned as the first (Before) run, so the pair is
// the same regardless of argument order.
func resolvePair(ctx context.Context, st *store.Store, left, right string) (*store.RunRow, *store.RunRow, error) {
	switch {
	case left == SelectorPrevious:
		anchor, err := resolveOne(ctx, st, right)
		if err != nil {
			return nil, nil, err
		}
		prev, err := st.PreviousCompatibleRun(ctx, *anchor)
		if err != nil {
			return nil, nil, err
		}
		return prev, anchor, nil
	case right == SelectorPrevious:
		anchor, err := resolveOne(ctx, st, left)
		if err != nil {
			return nil, nil, err
		}
		prev, err := st.PreviousCompatibleRun(ctx, *anchor)
		if err != nil {
			return nil, nil, err
		}
		return prev, anchor, nil
	default:
		l, err := resolveOne(ctx, st, left)
		if err != nil {
			return nil, nil, err
		}
		r, err := resolveOne(ctx, st, right)
		if err != nil {
			return nil, nil, err
		}
		return l, r, nil
	}
}

// resolveOne resolves a selector that is not "previous". The store's missing
// run errors wrap sql.ErrNoRows.
func resolveOne(ctx context.Context, st *store.Store, selector string) (*store.RunRow, error) {
	if selector == SelectorLatest {
		return st.LatestRun(ctx)
	}
	return st.GetRun(ctx, selector)
}

// compatible reports whether two runs form a controlled comparison: the same
// suite name/version/hash, task id/version and fixture, and neither is a dry
// run. Requiring the suite hash means a changed suite body under the same
// name/version is not treated as compatible.
func compatible(a, b store.RunRow) bool {
	return !a.DryRun && !b.DryRun &&
		a.SuiteName == b.SuiteName && a.SuiteVersion == b.SuiteVersion && a.SuiteHash == b.SuiteHash &&
		a.TaskID == b.TaskID && a.TaskVersion == b.TaskVersion &&
		a.FixtureSHA == b.FixtureSHA
}

// metricDeltas unions the metric names of both runs, sorts them lexically and
// computes After-Before for each. A metric missing from one side counts as
// zero. Percent is nil when Before is zero.
func metricDeltas(before, after map[string]float64) []MetricDelta {
	names := make(map[string]struct{}, len(before)+len(after))
	for name := range before {
		names[name] = struct{}{}
	}
	for name := range after {
		names[name] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	out := make([]MetricDelta, 0, len(sorted))
	for _, name := range sorted {
		b := before[name]
		a := after[name]
		delta := MetricDelta{Name: name, Before: b, After: a, Delta: a - b}
		if b != 0 {
			percent := (a - b) / b * 100
			delta.Percent = &percent
		}
		out = append(out, delta)
	}
	return out
}

// controlledRunWarning returns the warning shown when more than one profile
// component changed. It states the exact count and recommends a controlled
// comparison; it deliberately makes no causal claim.
func controlledRunWarning(changes int) string {
	if changes <= 1 {
		return ""
	}
	return fmt.Sprintf("%d profile components changed between the compared runs; run a controlled comparison that changes one component at a time.", changes)
}
