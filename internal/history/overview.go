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
	TaskID       string
	Suite        string
	Runs         int
	Score        float64
	Pass         float64
	Solved       int
	Cost         float64
	MedianTokens int64
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

// Significance reports whether the leader's lead is distinguishable from noise.
type Significance struct {
	P               float64
	Distinguishable bool
	Note            string
}

// OverviewReport is everything the dashboard's front page renders.
type OverviewReport struct {
	Scope           string
	Profiles        []ProfileScore
	Suites          []string
	Scatter         []ScatterPoint
	HeroDiff        []profile.ChangeNote
	Significance    *Significance
	ExcludedRuns    int
	TotalRuns       int
	OpenCodeVersion string
	LastRun         time.Time
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
		overview.OpenCodeVersion = r.OpenCodeVersion
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

	overview.finish(ctx, st)
	return overview, nil
}

// finish adds the leader-vs-runner-up comparison, which needs both profiles.
func (o *OverviewReport) finish(ctx context.Context, st *store.Store) {
	if len(o.Profiles) < 2 {
		return
	}
	leader, runnerUp := o.Profiles[0], o.Profiles[1]
	if !leader.HasRuns || !runnerUp.HasRuns {
		return // nothing to compare yet
	}

	leaderProfile, err := loadProfile(ctx, st, leader.Hash)
	if err != nil {
		return
	}
	runnerUpProfile, err := loadProfile(ctx, st, runnerUp.Hash)
	if err != nil {
		return
	}
	o.HeroDiff = profile.DiffNotes(runnerUpProfile, leaderProfile)

	p := stats.PermutationP(taskScores(leader), taskScores(runnerUp), ciSeed, ciIters)
	o.Significance = &Significance{P: p, Distinguishable: p < stats.DefaultAlpha}
	if o.Significance.Distinguishable {
		o.Significance.Note = fmt.Sprintf("%s leads %s with p=%.3f over the tasks both ran", leader.Label, runnerUp.Label, p)
	} else {
		o.Significance.Note = fmt.Sprintf("no detectable difference between %s and %s (p=%.2f); more runs would help", leader.Label, runnerUp.Label, p)
	}
}

// scoreProfile aggregates one profile's runs into a score, an interval, a pass
// rate and a cost per solved task.
func scoreProfile(ctx context.Context, st *store.Store, hash string, runs []store.RunRow) (ProfileScore, error) {
	ps := ProfileScore{Hash: hash, Runs: len(runs), HasRuns: len(runs) > 0, PerSuite: map[string]float64{}}

	tasks := map[[2]string]*taskAcc{}
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
		acc.scoreSum += values["score"]
		acc.passSum += values["success"]
		if values["success"] == 1 {
			acc.solved++
		}
		acc.cost += values["cost"]
		acc.tokens = append(acc.tokens, int64(values["tokens_total"]))

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
			MedianTokens: int64(stats.Median(floats(acc.tokens))),
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
	var totalCost float64
	var totalSolved, totalRuns int
	for _, ts := range ps.Tasks {
		suiteSums[ts.Suite] += ts.Score
		suiteCounts[ts.Suite]++
		allScores = append(allScores, ts.Score)
		totalCost += ts.Cost
		totalSolved += ts.Solved
		totalRuns += ts.Runs
	}
	for suite, sum := range suiteSums {
		ps.PerSuite[suite] = sum / float64(suiteCounts[suite])
	}

	ps.Score = weightedScore(ps.PerSuite, suiteCounts)
	ps.ScoreCI, ps.ScoreCIOK = ci(allScores)

	if totalRuns > 0 {
		ps.PassRate = float64(totalSolved) / float64(totalRuns)
		lo, hi := stats.Wilson(totalSolved, totalRuns, 1.96)
		ps.PassCI, ps.PassCIOK = [2]float64{lo, hi}, true
	}
	if totalSolved > 0 {
		ps.CostPerSolved = totalCost / float64(totalSolved)
		ps.CostPerSolvedOK = true
	}
	if len(ps.Tasks) > 0 {
		medians := make([]float64, 0, len(ps.Tasks))
		for _, ts := range ps.Tasks {
			medians = append(medians, float64(ts.MedianTokens))
		}
		ps.MedianTokens = int64(stats.Median(medians))
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

func metricValues(metrics []store.MetricRow) map[string]float64 {
	out := make(map[string]float64, len(metrics))
	for _, m := range metrics {
		if m.ValueNum != nil {
			out[m.Name] = *m.ValueNum
		}
	}
	return out
}

func taskScores(p ProfileScore) []float64 {
	out := make([]float64, 0, len(p.Tasks))
	for _, t := range p.Tasks {
		out = append(out, t.Score)
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
	tokens            []int64
}
