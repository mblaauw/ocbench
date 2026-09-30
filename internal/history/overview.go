package history

import (
	"context"
	"fmt"
	"sort"
	"time"

	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/stats"
	"mbl/ocbench/internal/store"
)

// ScopeAll selects every suite; any other value names a single suite.
const ScopeAll = "all"

// suiteOrder is the order suites appear in the score matrix. Suites outside the
// list follow, alphabetically.
var suiteOrder = []string{"core", "standard", "hard", "agentic"}

// ciSeed and ciIters make every interval in the dashboard reproducible.
const (
	ciSeed  = 0x0CBE9C
	ciIters = stats.DefaultBootstrapIters
)

// TaskScore aggregates one task's runs for one profile.
type TaskScore struct {
	TaskID string
	Suite  string
	Runs   int
	Score  float64
	Pass   float64
	Solved int
	Cost   float64
	// CostComplete is true when every run of this task recorded a cost. A
	// task with partial cost data is excluded from cost per solved task
	// rather than counted as partly free.
	CostComplete bool
	MedianTokens int64
	HasTokens    bool
}

// ProfileScore is one configuration's standing in the current scope.
type ProfileScore struct {
	Hash         string
	ProfileID    string
	Label        string // architecture summary, the only human name a profile has
	Architecture string
	Runs         int
	HasRuns      bool

	Score     float64
	ScoreCI   [2]float64
	ScoreCIOK bool

	PassRate float64
	PassCI   [2]float64
	PassCIOK bool

	CostPerSolved   float64
	CostPerSolvedOK bool
	MedianTokens    int64
	MedianTokensOK  bool

	// CacheHitRate is the share of this profile's prompt tokens served from
	// the provider's cache, pooled across its runs. CacheHitRateOK is false
	// when the runs used no prompt tokens, so there is no rate to report.
	CacheHitRate   float64
	CacheHitRateOK bool

	// TaskCount is how many distinct tasks this profile was scored on: the
	// sample size behind Score.
	TaskCount int
	// ScoreMDE is the smallest difference in mean task score a two-arm
	// comparison with this many tasks could detect, given the spread of task
	// scores actually observed. Zero means the spread could not be estimated,
	// so no difference is detectable on this evidence.
	ScoreMDE float64

	PerSuite map[string]float64
	Tasks    []TaskScore
	LastRun  time.Time
}

// ScatterPoint is one profile positioned by score and cost per solved task.
type ScatterPoint struct {
	Hash          string
	Label         string
	Score         float64
	CostPerSolved float64
}

// OverviewReport is everything the dashboard's front page renders.
type OverviewReport struct {
	Scope        string
	Profiles     []ProfileScore
	Suites       []string
	Scatter      []ScatterPoint
	ExcludedRuns int
	TotalRuns    int
	LastRun      time.Time
}

// Overview computes the profile leaderboard for a scope from persisted runs.
// Statistics are derived here and never stored, so a fix to the maths re-reads
// history rather than requiring a migration.
func Overview(ctx context.Context, st *store.Store, scope string) (OverviewReport, error) {
	if st == nil {
		return OverviewReport{}, fmt.Errorf("history: nil store")
	}
	if scope == "" {
		scope = ScopeAll
	}

	runs, err := st.ListRuns(ctx, 0, "")
	if err != nil {
		return OverviewReport{}, err
	}

	overview := OverviewReport{Scope: scope}
	currentHash := latestSuiteHashes(runs)

	byProfile := map[string][]store.RunRow{}
	for _, r := range runs {
		if r.DryRun {
			continue
		}
		overview.TotalRuns++
		if r.StartedAt > "" {
			if ts, err := time.Parse(time.RFC3339, r.StartedAt); err == nil && ts.After(overview.LastRun) {
				overview.LastRun = ts
			}
		}
		// A suite is scored only from its most recent content: runs recorded
		// against an older hash are counted and disclosed, never mixed in.
		if currentHash[r.SuiteName] != "" && r.SuiteHash != currentHash[r.SuiteName] {
			overview.ExcludedRuns++
			continue
		}
		if scope != ScopeAll && r.SuiteName != scope {
			continue
		}
		byProfile[r.ProfileHash] = append(byProfile[r.ProfileHash], r)
	}

	suiteSet := map[string]bool{}
	for _, r := range runs {
		if !r.DryRun {
			suiteSet[r.SuiteName] = true
		}
	}
	overview.Suites = orderedSuites(suiteSet)

	// Every persisted profile appears, including ones with no runs: the
	// dashboard says "no runs" rather than hiding a configuration.
	profiles, err := st.ListProfiles(ctx)
	if err != nil {
		return OverviewReport{}, err
	}
	for _, row := range profiles {
		ps, err := scoreProfile(ctx, st, row.ProfileHash, byProfile[row.ProfileHash])
		if err != nil {
			return OverviewReport{}, err
		}
		ps.ProfileID = row.ID
		overview.Profiles = append(overview.Profiles, ps)
	}

	// Scored profiles rank first, best score leading; the rest follow.
	sort.SliceStable(overview.Profiles, func(i, j int) bool {
		a, b := overview.Profiles[i], overview.Profiles[j]
		if a.HasRuns != b.HasRuns {
			return a.HasRuns
		}
		if a.HasRuns && a.Score != b.Score {
			return a.Score > b.Score
		}
		return a.Hash < b.Hash
	})

	for _, p := range overview.Profiles {
		if p.HasRuns && p.CostPerSolvedOK {
			overview.Scatter = append(overview.Scatter, ScatterPoint{
				Hash: p.Hash, Label: p.Label, Score: p.Score, CostPerSolved: p.CostPerSolved,
			})
		}
	}

	return overview, nil
}

// ProfileScoreForRuns derives one profile's score from an explicitly selected
// run set. Callers use it for a controlled experiment cohort; unlike Overview,
// it never reaches into unrelated historic runs.
func ProfileScoreForRuns(ctx context.Context, st *store.Store, profileHash string, runs []store.RunRow) (ProfileScore, error) {
	selected := make([]store.RunRow, 0, len(runs))
	for _, run := range runs {
		if !run.DryRun && run.ProfileHash == profileHash {
			selected = append(selected, run)
		}
	}
	return scoreProfile(ctx, st, profileHash, selected)
}

// scoreProfile aggregates one profile's runs into a score, an interval, a pass
// rate and a cost per solved task.
func scoreProfile(ctx context.Context, st *store.Store, hash string, runs []store.RunRow) (ProfileScore, error) {
	ps := ProfileScore{Hash: hash, Runs: len(runs), HasRuns: len(runs) > 0, PerSuite: map[string]float64{}}

	tasks := map[[2]string]*taskAcc{}
	// Prompt tokens are pooled across runs so the profile's cache hit rate is
	// a rate over its whole spend rather than a mean of per-run rates, which
	// would let a cheap run weigh as much as an expensive one.
	var cacheRead, cacheInput float64
	for _, r := range runs {
		metrics, err := st.GetRunMetrics(ctx, r.ID)
		if err != nil {
			return ProfileScore{}, err
		}
		values := metricValues(metrics)

		key := [2]string{r.SuiteName, r.TaskID}
		acc := tasks[key]
		if acc == nil {
			acc = &taskAcc{suite: r.SuiteName, taskID: r.TaskID}
			tasks[key] = acc
		}
		acc.runs++
		acc.scoreSum += scoreOf(values)
		acc.passSum += values["success"]
		if values["success"] == 1 {
			acc.solved++
		}
		// Cost is only comparable when every execution recorded it: a missing
		// cost is unmeasured, not free.
		if cost, ok := values["cost"]; ok {
			acc.cost += cost
			acc.costRuns++
		}
		if tokens, ok := values["tokens_total"]; ok {
			acc.tokens = append(acc.tokens, int64(tokens))
		}
		cacheRead += values["tokens_cache_read"]
		cacheInput += values["tokens_input"]

		if ts, err := time.Parse(time.RFC3339, r.StartedAt); err == nil && ts.After(ps.LastRun) {
			ps.LastRun = ts
		}
		ps.ProfileID = r.ProfileID
	}

	for _, acc := range tasks {
		ts := TaskScore{
			TaskID: acc.taskID, Suite: acc.suite, Runs: acc.runs,
			Score:  acc.scoreSum / float64(acc.runs),
			Pass:   acc.passSum / float64(acc.runs),
			Solved: acc.solved, Cost: acc.cost,
			CostComplete: acc.costRuns == acc.runs,
			MedianTokens: int64(stats.Median(floats(acc.tokens))),
			HasTokens:    len(acc.tokens) > 0,
		}
		ps.Tasks = append(ps.Tasks, ts)
	}
	sort.Slice(ps.Tasks, func(i, j int) bool {
		if ps.Tasks[i].Suite != ps.Tasks[j].Suite {
			return ps.Tasks[i].Suite < ps.Tasks[j].Suite
		}
		return ps.Tasks[i].TaskID < ps.Tasks[j].TaskID
	})

	// Suite score is the mean over the tasks of that suite that have runs.
	suiteSums := map[string]float64{}
	suiteCounts := map[string]int{}
	var allScores []float64
	var costTotal float64
	var costSolved, totalSolved, totalRuns int
	for _, ts := range ps.Tasks {
		suiteSums[ts.Suite] += ts.Score
		suiteCounts[ts.Suite]++
		allScores = append(allScores, ts.Score)
		if ts.CostComplete {
			costTotal += ts.Cost
			costSolved += ts.Solved
		}
		totalSolved += ts.Solved
		totalRuns += ts.Runs
	}
	for suite, sum := range suiteSums {
		ps.PerSuite[suite] = sum / float64(suiteCounts[suite])
	}

	ps.Score = weightedScore(ps.PerSuite, suiteCounts)
	ps.ScoreCI, ps.ScoreCIOK = ci(allScores)
	if rate, ok := CacheHitRate(map[string]float64{
		"tokens_cache_read": cacheRead, "tokens_input": cacheInput,
	}); ok {
		ps.CacheHitRate, ps.CacheHitRateOK = rate, true
	}

	if totalRuns > 0 {
		ps.PassRate = float64(totalSolved) / float64(totalRuns)
		lo, hi := stats.Wilson(totalSolved, totalRuns, 1.96)
		ps.PassCI, ps.PassCIOK = [2]float64{lo, hi}, true
	}
	if costSolved > 0 {
		ps.CostPerSolved = costTotal / float64(costSolved)
		ps.CostPerSolvedOK = true
	}
	if len(ps.Tasks) > 0 {
		medians := make([]float64, 0, len(ps.Tasks))
		for _, ts := range ps.Tasks {
			if ts.HasTokens {
				medians = append(medians, float64(ts.MedianTokens))
			}
		}
		if median, ok := stats.MedianOK(medians); ok {
			ps.MedianTokens = int64(median)
			ps.MedianTokensOK = true
		}
	}
	// How large a difference these tasks could resolve. This is what turns the
	// leaderboard from a ranking into evidence: below this, a lead is noise.
	ps.TaskCount = len(ps.Tasks)
	if mde, ok := stats.MDE(allScores, ps.TaskCount, stats.DefaultPower, stats.DefaultAlpha); ok {
		ps.ScoreMDE = mde
	}

	if p, err := loadProfile(ctx, st, hash); err == nil {
		v := profile.NewView(p)
		ps.Label = v.Summary()
		ps.Architecture = v.Summary()
	}
	if ps.Label == "" {
		ps.Label = shortHash(hash)
	}
	return ps, nil
}

// weightedScore combines suite scores by how many tasks each suite holds, so a
// five-task suite counts for more than a two-task one. When a scope names a
// single suite this is just that suite's score.
func weightedScore(perSuite map[string]float64, counts map[string]int) float64 {
	var weighted, weights float64
	for suite, score := range perSuite {
		w := float64(counts[suite])
		weighted += score * w
		weights += w
	}
	if weights == 0 {
		return 0
	}
	return weighted / weights
}

// latestSuiteHashes returns, per suite name, the hash of its most recently
// started run.
func latestSuiteHashes(runs []store.RunRow) map[string]string {
	type latest struct {
		hash string
		at   string
	}
	seen := map[string]latest{}
	for _, r := range runs {
		if r.DryRun {
			continue
		}
		cur, ok := seen[r.SuiteName]
		if !ok || r.StartedAt > cur.at {
			seen[r.SuiteName] = latest{hash: r.SuiteHash, at: r.StartedAt}
		}
	}
	out := map[string]string{}
	for name, l := range seen {
		out[name] = l.hash
	}
	return out
}

// orderedSuites returns the known suites in a stable order.
func orderedSuites(set map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range suiteOrder {
		if set[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	var rest []string
	for name := range set {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

func loadProfile(ctx context.Context, st *store.Store, hash string) (*profile.Profile, error) {
	row, comps, err := st.GetProfileByHash(ctx, hash)
	if err != nil {
		return nil, err
	}
	return profile.FromRows(row, comps)
}

// scoreOf reads a run's weighted validator score, falling back to its binary
// success when the run predates the score metric. Without the fallback a run
// recorded before scoring existed would read as 0.00 and drag a profile's score
// down for a reason that has nothing to do with the configuration.
func scoreOf(values map[string]float64) float64 {
	if score, ok := values["score"]; ok {
		return score
	}
	return values["success"]
}

func metricValues(metrics []store.MetricRow) map[string]float64 {
	out := make(map[string]float64, len(metrics))
	for _, m := range metrics {
		if m.ValueNum != nil {
			out[m.Name] = *m.ValueNum
		}
	}
	return out
}

func ci(samples []float64) ([2]float64, bool) {
	lo, hi, ok := stats.BootstrapCI(samples, ciIters, ciSeed)
	return [2]float64{lo, hi}, ok
}

func floats(xs []int64) []float64 {
	out := make([]float64, 0, len(xs))
	for _, x := range xs {
		out = append(out, float64(x))
	}
	return out
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// taskAcc accumulates one task's runs while scoring a profile.
type taskAcc struct {
	suite, taskID     string
	runs              int
	scoreSum, passSum float64
	solved            int
	cost              float64
	costRuns          int
	tokens            []int64
}
