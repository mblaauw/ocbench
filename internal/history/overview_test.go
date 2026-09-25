package history_test

import (
	"context"
	"strings"
	"testing"

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
	// A solved one of two runs, spending 0.30.
	if !a.CostPerSolvedOK || a.CostPerSolved < 0.29 || a.CostPerSolved > 0.31 {
		t.Errorf("A cost per solved = %v (ok=%v), want ~0.30", a.CostPerSolved, a.CostPerSolvedOK)
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
	if ov.Significance == nil {
		t.Fatal("expected a leader-vs-runner-up comparison")
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
	if first.Significance.P != second.Significance.P {
		t.Errorf("significance not deterministic: %v vs %v", first.Significance.P, second.Significance.P)
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
	if ov.Significance != nil {
		t.Error("no comparison is possible with a single scored profile")
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
	if ov.Significance != nil {
		t.Error("empty store should have no significance result")
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

// With one task scored per profile no permutation can reach significance, and
// the report must say the evidence is insufficient rather than name a leader.
func TestOverviewRefusesToRankFromASingleTaskEach(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	seedProfile(t, st, "p2", "hash-b", components("h2"))
	seedScoredRun(t, st, "r1", "2026-03-01T10:00:00Z", "p1", "hash-a", suiteName, "task-one", 1.0, 1, 1000, 0.10)
	seedScoredRun(t, st, "r2", "2026-03-01T10:01:00Z", "p2", "hash-b", suiteName, "task-two", 0.0, 0, 1000, 0.10)

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.Significance == nil {
		t.Fatal("no significance block")
	}
	if ov.Significance.Distinguishable {
		t.Errorf("claimed a distinguishable lead from one task each: %+v", ov.Significance)
	}
	if !strings.Contains(ov.Significance.Note, "too little evidence") {
		t.Errorf("note = %q, want it to state the evidence is insufficient", ov.Significance.Note)
	}
}

// A lead smaller than the detectable effect is not evidence, even when the
// permutation p-value happens to fall below the threshold.
func TestOverviewGatesOnTheDetectableEffectNotJustThePValue(t *testing.T) {
	st := testStore(t)
	seedSuiteTask(t, st)
	seedProfile(t, st, "p1", "hash-a", components("h1"))
	seedProfile(t, st, "p2", "hash-b", components("h2"))

	// Two profiles whose task scores are tightly clustered, so the detectable
	// effect is small, but whose means differ by a hair.
	for i, id := range []string{"a1", "a2", "a3", "a4"} {
		seedScoredRun(t, st, id, "2026-03-01T10:0"+string(rune('0'+i))+":00Z",
			"p1", "hash-a", suiteName, "task-"+string(rune('a'+i)), 1.0, 1, 1000, 0.10)
	}
	for i, id := range []string{"b1", "b2", "b3", "b4"} {
		seedScoredRun(t, st, id, "2026-03-01T11:0"+string(rune('0'+i))+":00Z",
			"p2", "hash-b", suiteName, "task-"+string(rune('a'+i)), 0.99, 1, 1000, 0.10)
	}

	ov, err := history.Overview(context.Background(), st, history.ScopeAll)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if ov.Significance == nil {
		t.Fatal("no significance block")
	}
	// The gap is 0.01; the spread is tiny, so the gate is what decides. Either
	// way the reported gap and MDE must be consistent with the verdict.
	if ov.Significance.Distinguishable && ov.Significance.Gap <= ov.Significance.MDE {
		t.Errorf("called a %v gap distinguishable with MDE %v", ov.Significance.Gap, ov.Significance.MDE)
	}
	if !ov.Significance.Distinguishable && !strings.Contains(ov.Significance.Note, "no detectable difference") {
		t.Errorf("note = %q", ov.Significance.Note)
	}
}
