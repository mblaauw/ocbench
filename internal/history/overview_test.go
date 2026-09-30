package history_test

import (
	"context"
	"testing"
	"time"

	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/store"
)

// seedScoredRun inserts a run with the metrics the overview reads.
func seedScoredRun(t *testing.T, st *store.Store, id, started, profileID, profileHash, suite, task string, score, success, tokens, cost float64) {
	t.Helper()
	r := baseRun(id, started, profileID, profileHash)
	r.SuiteName, r.TaskID = suite, task
	r.SuiteHash = "hash-" + suite
	seedRun(t, st, r)
	if err := st.InsertRunMetrics(context.Background(), id, map[string]float64{
		"score": score, "success": success, "tokens_total": tokens, "cost": cost,
	}); err != nil {
		t.Fatalf("metrics for %s: %v", id, err)
	}
}

func TestOverviewRanksProfilesAndComputesIntervals(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	seedProfile(t, st, "p2", "hash-b", components("h2"))

	// Profile A: two core tasks scoring 1.0 and 0.5 → 0.75.
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "task-one", 1.0, 1, 1000, 0.10)
	seedScoredRun(t, st, "r2", "2026-03-01T10:01:00Z", "p1", "hash-a", suiteName, "task-two", 0.5, 0, 3000, 0.20)
	// Profile B: one core task scoring 0.5.
	seedScoredRun(t, st, "r3", "2026-03-01T10:02:00Z", "p2", "hash-b", suiteName, "task-one", 0.5, 0, 500, 0.05)

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}

	if ov.TotalRuns != 3 || ov.ExcludedRuns != 0 {
		t.Errorf("runs = %d excluded = %d, want 3/0", ov.TotalRuns, ov.ExcludedRuns)
	}
	if len(ov.Profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(ov.Profiles))
	}

	a, b := ov.Profiles[0], ov.Profiles[1]
	if a.Hash != "hash-a" || b.Hash != "hash-b" {
		t.Fatalf("ranking = %s then %s, want hash-a then hash-b", a.Hash, b.Hash)
	}
	if a.Score != 0.75 {
		t.Errorf("A score = %v, want 0.75", a.Score)
	}
	if b.Score != 0.5 {
		t.Errorf("B score = %v, want 0.5", b.Score)
	}
	if !a.ScoreCIOK || a.ScoreCI[0] > a.Score || a.ScoreCI[1] < a.Score {
		t.Errorf("A interval %v does not contain its score %v", a.ScoreCI, a.Score)
	}
	if a.PerSuite[suiteName] != 0.75 {
		t.Errorf("A per-suite = %v", a.PerSuite)
	}
	// A solved task-one for 0.10. task-two never passed, so its 0.20 bought no
	// solved work and is excluded, matching the experiment aggregate.
	if !a.CostPerSolvedOK || a.CostPerSolved < 0.099 || a.CostPerSolved > 0.101 {
		t.Errorf("A cost per solved = %v (ok=%v), want ~0.10", a.CostPerSolved, a.CostPerSolvedOK)
	}
	if a.PassRate != 0.5 || !a.PassCIOK {
		t.Errorf("A pass rate = %v (ok=%v), want 0.5", a.PassRate, a.PassCIOK)
	}
	if a.MedianTokens != 2000 {
		t.Errorf("A median tokens = %d, want 2000 (median of 1000 and 3000)", a.MedianTokens)
	}
	// B solved nothing, so it has no cost per solved task.
	if b.CostPerSolvedOK {
		t.Errorf("B should have no cost per solved task, got %v", b.CostPerSolved)
	}
	if len(ov.Scatter) != 1 || ov.Scatter[0].Hash != "hash-a" {
		t.Errorf("scatter = %+v, want only A", ov.Scatter)
	}
}

func TestProfileScoreForRunsUsesOnlyTheSuppliedCohort(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	seedScoredRun(t, st, "cohort-run", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "task-one", 1, 1, 100, 0.01)
	seedScoredRun(t, st, "historic-run", "2026-03-01T10:01:00Z", "p1", "hash-a", suiteName, "task-two", 0, 0, 500, 0.50)

	runs, err := st.ListRuns(context.Background(), 0, "")
	if err != nil {
		t.Fatal(err)
	}
	var cohort []store.RunRow
	for _, run := range runs {
		if run.ID == "cohort-run" {
			cohort = append(cohort, run)
		}
	}
	score, err := history.ProfileScoreForRuns(context.Background(), st, "hash-a", cohort)
	if err != nil {
		t.Fatal(err)
	}
	if score.Runs != 1 || score.Score != 1 || score.CostPerSolved != 0.01 {
		t.Fatalf("cohort score = %+v, want only cohort-run", score)
	}
}

// A task whose execution never recorded cost must not contribute to cost per
// solved task as if the missing cost were zero.
func TestOverviewIgnoresTasksWithoutCompleteCost(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))

	// task-one: solved with a recorded cost.
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "task-one", 1, 1, 100, 0.10)
	// task-two: solved, but the cost metric was never recorded.
	noCost := baseRun("r2", "2026-03-01T10:01:00Z", "p1", "hash-a")
	noCost.SuiteName, noCost.SuiteHash, noCost.TaskID = suiteName, "hash-"+suiteName, "task-two"
	seedRun(t, st, noCost)
	if err := st.InsertRunMetrics(context.Background(), "r2", map[string]float64{
		"score": 1, "success": 1, "tokens_total": 200,
	}); err != nil {
		t.Fatal(err)
	}

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	p := ov.Profiles[0]
	if !p.CostPerSolvedOK || p.CostPerSolved != 0.10 {
		t.Fatalf("cost per solved = %v (ok=%v), want 0.10 from the task with recorded cost",
			p.CostPerSolved, p.CostPerSolvedOK)
	}
}

// A task that never passed must not drag cost per solved up with spend that
// bought nothing; the experiment aggregate skips the same task.
func TestOverviewCostPerSolvedSkipsFullyFailedTasks(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))

	// task-one: solved for 0.10.
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "task-one", 1, 1, 100, 0.10)
	// task-two: complete cost, but every execution failed. Its spend bought
	// nothing and must not inflate the per-solved figure.
	seedScoredRun(t, st, "r2", "2026-03-01T10:01:00Z", "p1", "hash-a", suiteName, "task-two", 0, 0, 100, 10.00)

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	p := ov.Profiles[0]
	if !p.CostPerSolvedOK || p.CostPerSolved != 0.10 {
		t.Fatalf("cost per solved = %v (ok=%v), want 0.10 from the solved task only",
			p.CostPerSolved, p.CostPerSolvedOK)
	}
}

// With no recorded cost anywhere, cost per solved task is unavailable, not zero.
func TestOverviewCostPerSolvedRequiresRecordedCost(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	noCost := baseRun("r1", "2026-03-01T10:00:00Z", "p1", "hash-a")
	noCost.SuiteName, noCost.SuiteHash = suiteName, "hash-"+suiteName
	seedRun(t, st, noCost)
	if err := st.InsertRunMetrics(context.Background(), "r1", map[string]float64{
		"score": 1, "success": 1, "tokens_total": 100,
	}); err != nil {
		t.Fatal(err)
	}

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.Profiles[0].CostPerSolvedOK {
		t.Fatalf("cost per solved = %v, want unavailable when no cost was recorded",
			ov.Profiles[0].CostPerSolved)
	}
}

func TestOverviewIsDeterministic(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	seedProfile(t, st, "p2", "hash-b", components("h2"))
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "t1", 0.9, 1, 100, 0.01)
	seedScoredRun(t, st, "r2", "2026-03-01T10:01:00Z", "p1", "hash-a", suiteName, "t2", 0.4, 0, 200, 0.02)
	seedScoredRun(t, st, "r3", "2026-03-01T10:02:00Z", "p2", "hash-b", suiteName, "t1", 0.6, 1, 300, 0.03)

	first, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	second, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if first.Profiles[0].ScoreCI != second.Profiles[0].ScoreCI {
		t.Errorf("interval not deterministic: %v vs %v", first.Profiles[0].ScoreCI, second.Profiles[0].ScoreCI)
	}
}

func TestOverviewScopeFiltersSuites(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", "core", "t1", 1.0, 1, 100, 0.01)
	seedScoredRun(t, st, "r2", "2026-03-01T10:01:00Z", "p1", "hash-a", "hard", "t2", 0.2, 0, 200, 0.02)

	all, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Suites) != 2 || all.Suites[0] != "core" || all.Suites[1] != "hard" {
		t.Errorf("suites = %v, want [core hard] in canonical order", all.Suites)
	}
	if all.Profiles[0].Score != 0.6 {
		t.Errorf("all-suite score = %v, want the mean of 1.0 and 0.2", all.Profiles[0].Score)
	}

	hardOnly, err := history.Overview(context.Background(), st, "hard")
	if err != nil {
		t.Fatal(err)
	}
	if hardOnly.Profiles[0].Score != 0.2 || hardOnly.Profiles[0].Runs != 1 {
		t.Errorf("hard scope = score %v runs %d, want 0.2/1", hardOnly.Profiles[0].Score, hardOnly.Profiles[0].Runs)
	}
}

func TestOverviewExcludesRunsFromOlderSuiteHashes(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))

	// The older run carries a different suite hash for the same suite name.
	old := baseRun("r-old", "2026-03-01T09:00:00Z", "p1", "hash-a")
	old.SuiteName, old.SuiteHash, old.TaskID = "hard", "old-hash", "t1"
	seedRun(t, st, old)
	seedScoredRun(t, st, "r-new", "2026-03-01T10:00:00Z", "p1", "hash-a", "hard", "t1", 1.0, 1, 100, 0.01)

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if ov.ExcludedRuns != 1 {
		t.Errorf("excluded = %d, want 1 (the older suite hash)", ov.ExcludedRuns)
	}
	if ov.Profiles[0].Runs != 1 || ov.Profiles[0].Score != 1.0 {
		t.Errorf("scored runs = %d score = %v, want 1/1.0", ov.Profiles[0].Runs, ov.Profiles[0].Score)
	}
}

func TestOverviewListsProfilesWithoutRuns(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	seedProfile(t, st, "p2", "hash-b", components("h2"))
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "t1", 1.0, 1, 100, 0.01)

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Profiles) != 2 {
		t.Fatalf("profiles = %d, want both listed", len(ov.Profiles))
	}
	if !ov.Profiles[0].HasRuns || ov.Profiles[1].HasRuns {
		t.Errorf("ranking should put the scored profile first: %+v", ov.Profiles)
	}
	if ov.Profiles[1].Score != 0 {
		t.Errorf("a profile with no runs must not be scored: %v", ov.Profiles[1].Score)
	}
}

func TestOverviewEmptyStore(t *testing.T) {
	ov, err := history.Overview(context.Background(), testStore(t), history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview on an empty store: %v", err)
	}
	if len(ov.Profiles) != 0 || len(ov.Suites) != 0 || len(ov.Scatter) != 0 {
		t.Errorf("empty store produced %+v", ov)
	}
}

func TestOverviewFallsBackToSuccessWithoutScoreMetric(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))

	// A run recorded before the score metric existed: only success is present.
	old := baseRun("r-old", "2026-03-01T10:00:00Z", "p1", "hash-a")
	old.SuiteName, old.SuiteHash = "core", "hash-core"
	seedRun(t, st, old)
	if err := st.InsertRunMetrics(context.Background(), "r-old", map[string]float64{
		"success": 1, "tokens_total": 100, "cost": 0.01,
	}); err != nil {
		t.Fatal(err)
	}

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if got := ov.Profiles[0].Score; got != 1.0 {
		t.Errorf("score = %v, want 1.0: a passed run without a score metric must not read as 0", got)
	}
}

// A leaderboard must report the sample size behind each score and the smallest
// difference that sample could resolve, so a ranking is not read as evidence it
// cannot be.
func TestOverviewReportsTaskCountAndDetectableEffect(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))

	// Four tasks with a genuine spread of scores.
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "task-one", 1.0, 1, 1000, 0.10)
	seedScoredRun(t, st, "r2", "2026-03-01T10:01:00Z", "p1", "hash-a", suiteName, "task-two", 0.5, 0, 1000, 0.10)
	seedScoredRun(t, st, "r3", "2026-03-01T10:02:00Z", "p1", "hash-a", suiteName, "task-three", 1.0, 1, 1000, 0.10)
	seedScoredRun(t, st, "r4", "2026-03-01T10:03:00Z", "p1", "hash-a", suiteName, "task-four", 0.75, 1, 1000, 0.10)

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(ov.Profiles) != 1 {
		t.Fatalf("profiles = %d, want 1", len(ov.Profiles))
	}
	p := ov.Profiles[0]
	if p.TaskCount != 4 {
		t.Errorf("TaskCount = %d, want 4", p.TaskCount)
	}
	// The scores 1.0, 0.5, 1.0, 0.75 have a sample SD of 0.2394, so four tasks
	// resolve a difference of 2.8016 * 0.2394 * sqrt(2/4) = 0.474 and no
	// smaller.
	if p.ScoreMDE < 0.47 || p.ScoreMDE > 0.48 {
		t.Errorf("ScoreMDE = %v, want ~0.474 for this spread over four tasks", p.ScoreMDE)
	}
}

// The profile's cache hit rate pools prompt tokens across runs, so a cheap run
// cannot weigh as much as an expensive one.
func TestOverviewPoolsTheCacheHitRate(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	// One run with 900 of 1000 prompt tokens cached, one with none.
	for i, id := range []string{"c1", "c2"} {
		r := baseRun(id, "2026-03-01T10:0"+string(rune('0'+i))+":00Z", "p1", "hash-a")
		r.SuiteName, r.TaskID = suiteName, "task-"+id
		seedRun(t, st, r)
	}
	if err := st.InsertRunMetrics(context.Background(), "c1", map[string]float64{
		"score": 1, "success": 1, "tokens_cache_read": 900, "tokens_input": 100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertRunMetrics(context.Background(), "c2", map[string]float64{
		"score": 1, "success": 1, "tokens_cache_read": 0, "tokens_input": 900,
	}); err != nil {
		t.Fatal(err)
	}

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	p := ov.Profiles[0]
	if !p.CacheHitRateOK {
		t.Fatal("no cache hit rate reported")
	}
	// 900 of 1900 prompt tokens: a mean of the two per-run rates would say 45%.
	if p.CacheHitRate < 0.47 || p.CacheHitRate > 0.48 {
		t.Errorf("CacheHitRate = %v, want ~0.474 pooled across prompt tokens", p.CacheHitRate)
	}
}

// A scoped overview must count only the scope it shows. Reporting another
// suite's runs in "total runs" or in the older-suite disclosure would describe
// data the page does not list.
func TestOverviewScopeScopesCounters(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))

	// core holds a run against a superseded suite hash plus a current one;
	// hard holds a single current run.
	stale := baseRun("r-core-old", "2026-03-01T08:00:00Z", "p1", "hash-a")
	stale.SuiteName, stale.TaskID, stale.SuiteHash = "core", "t1", "core-stale"
	seedRun(t, st, stale)
	seedScoredRun(t, st, "r-core", "2026-03-01T09:00:00Z", "p1", "hash-a", "core", "t1", 1.0, 1, 100, 0.01)
	seedScoredRun(t, st, "r-hard", "2026-03-01T10:00:00Z", "p1", "hash-a", "hard", "t2", 0.5, 1, 200, 0.02)

	hard, err := history.Overview(context.Background(), st, "hard")
	if err != nil {
		t.Fatal(err)
	}
	if hard.TotalRuns != 1 {
		t.Errorf("scoped total runs = %d, want 1 (only the hard run)", hard.TotalRuns)
	}
	if hard.ExcludedRuns != 0 {
		t.Errorf("scoped excluded = %d, want 0 (core's superseded run is out of scope)", hard.ExcludedRuns)
	}
	if want := "2026-03-01T10:00:00Z"; !hard.LastRun.Equal(mustParse(t, want)) {
		t.Errorf("scoped last run = %v, want the hard run at %v", hard.LastRun, want)
	}

	all, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	if all.TotalRuns != 3 || all.ExcludedRuns != 1 {
		t.Errorf("all-suite total = %d excluded = %d, want 3/1", all.TotalRuns, all.ExcludedRuns)
	}
}

func mustParse(t *testing.T, stamp string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// The experiment aggregate and this read model must agree on what "solved" means,
// or the same corpus yields two different cost-per-solved figures. A run with
// half credit counts as solved, and a run that recorded no success metric at all
// falls back to its own status.
func TestOverviewSolvedMatchesTheExperimentRule(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))

	// task-one: partial credit above the half threshold.
	seedScoredRun(t, st, "r-half", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "task-one", 0.5, 0.5, 100, 0.02)
	// task-two: no success metric recorded; the run itself passed.
	r := baseRun("r-nometric", "2026-03-01T10:01:00Z", "p1", "hash-a")
	r.SuiteName, r.TaskID = suiteName, "task-two"
	r.SuiteHash = "hash-" + suiteName // same current content as the other run
	seedRun(t, st, r)
	if err := st.InsertRunMetrics(context.Background(), "r-nometric", map[string]float64{
		"score": 1, "cost": 0.03, "tokens_total": 100,
	}); err != nil {
		t.Fatal(err)
	}

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	ps := ov.Profiles[0]
	byTask := map[string]history.TaskScore{}
	for _, ts := range ps.Tasks {
		byTask[ts.TaskID] = ts
	}
	if got := byTask["task-one"].Solved; got != 1 {
		t.Errorf("task-one solved = %d, want 1 (success 0.5 clears the half threshold)", got)
	}
	if got := byTask["task-two"].Solved; got != 1 {
		t.Errorf("task-two solved = %d, want 1 (no success metric, run status passed)", got)
	}
	// Both tasks earned cost against a solved task, so the pooled figure is
	// the total spend over the two solved tasks.
	if !ps.CostPerSolvedOK || ps.CostPerSolved < 0.0249 || ps.CostPerSolved > 0.0251 {
		t.Errorf("cost per solved = %v (ok=%v), want 0.025", ps.CostPerSolved, ps.CostPerSolvedOK)
	}
	if ps.PassRate != 1 {
		t.Errorf("pass rate = %v, want 1: both runs solved their task", ps.PassRate)
	}
}
