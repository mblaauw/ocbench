package web

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"mbl/ocbench/internal/history"
)

// page builds the shared chrome for a request: navigation with the current
// entry marked, and the environment panel of stored facts.
func (h *handler) page(r *http.Request, current, crumb, title, sub string) layout {
	hints := map[string]string{}
	if h.store != nil {
		if n, err := h.store.CountRuns(r.Context()); err == nil {
			hints["runs"] = fmt.Sprintf("%d", n)
		}
		if n, err := h.store.ListProfiles(r.Context()); err == nil {
			hints["overview"] = fmt.Sprintf("%d profiles", len(n))
		}
		if tasks, err := h.store.ListSuiteTasks(r.Context()); err == nil {
			// The catalogue shows one revision per suite name, so the hint
			// counts names rather than every historical task row.
			names := make(map[string]bool, len(tasks))
			for _, t := range tasks {
				names[t.SuiteName] = true
			}
			hints["suites"] = fmt.Sprintf("%d", len(names))
		}
		if cohorts, err := h.store.ListExperiments(r.Context(), 0); err == nil {
			if armCounts, err := h.store.ExperimentArmCounts(r.Context()); err == nil {
				count := 0
				for _, cohort := range cohorts {
					if armCounts[cohort.ID] >= 2 {
						count++
					}
				}
				hints["cohorts"] = fmt.Sprintf("%d", count)
			}
		}
	}
	return layout{
		Title:     title,
		Crumb:     crumb,
		PageTitle: title,
		Sub:       sub,
		Nav:       navItems(current, hints),
		Env:       h.envRows(r.Context()),
	}
}

// leaderRow is one line of the leaderboard.
type leaderRow struct {
	Rank         int
	First        bool
	Label        string
	Hash         string
	ShortHash    string
	Architecture string
	Href         string
	HasRuns      bool
	ScoreText    string
	// CIText is the half-width of the score's interval, which the hero shows
	// beside the score the way the prototype does.
	CIText         string
	ScorePercent   int
	CILowPercent   int
	CIDeltaPercent int
	PassText       string
	CostText       string
	TokensText     string
	// CostSort and TokensSort are the raw sort keys for the client-side
	// leaderboard. They are empty when the value is missing, so the script can
	// keep "—" rows last instead of sorting them as zero.
	CostSort   string
	TokensSort string
	Runs       int
	// TaskCount is the sample size behind the score; MDEText is the smallest
	// difference that sample could resolve. Together they say whether the
	// ranking means anything.
	TaskCount      int
	MDEText        string
	EvidenceStrong bool
	// CacheText is the share of prompt tokens served from cache, which is what
	// explains a token figure as much as the token figure itself.
	CacheText string
}

// matrixHeads abbreviates suite names for the matrix header.
func matrixHeads(suites []string) []string {
	out := make([]string, 0, len(suites))
	for _, s := range suites {
		if short, ok := suiteAbbrev[s]; ok {
			out = append(out, short)
			continue
		}
		out = append(out, s)
	}
	return out
}

// suiteAbbrev shortens the suite names that do not fit a matrix column.
var suiteAbbrev = map[string]string{
	"standard":  "std",
	"harvested": "harv",
	"agentic":   "agent",
}

// matrixCell is one profile-by-suite score. Tint is a 0..5 band so the cell
// colour comes from a class rather than an inline style, which the dashboard's
// Content-Security-Policy forbids.
type matrixCell struct {
	Text string
	Tint int
}

// matrixRow is one profile across the suites.
type matrixRow struct {
	Label string
	Cells []matrixCell
}

// scatterDot positions one profile on the score-against-cost chart.
type scatterDot struct {
	X      float64
	Y      float64
	LabelX float64
	LabelY float64
	Anchor string
	Label  string
	First  bool
}

// diffNote is one explained component difference.
type diffNote struct {
	Sign   string
	Class  string
	Change string
	What   string
	Note   string
}

// overviewPage backs the profile leaderboard.
type overviewPage struct {
	layout

	HasRuns bool
	Rows    []leaderRow
	Suites  []string
	// MatrixHeads abbreviates the suite names for the matrix header, where a
	// column per suite has to fit beside the profile name.
	MatrixHeads  []string
	Matrix       []matrixRow
	Scatter      []scatterDot
	ScatterXMax  string
	HeroDiff     []diffNote
	HeroDiffMore int
	Significance *significanceView
	ExcludedRuns int
	TotalRuns    int
}

// significanceView is the verdict box under the hero. The prototype states it
// as a bordered callout with a headline and the evidence behind it, rather than
// as a tag with a sentence.
type significanceView struct {
	Distinguishable bool
	Head            string
	Body            string
	// Class is the colour of the border and text: good when the lead is
	// distinguishable, warn when it is not.
	Class string
}

// newSignificanceView states the verdict the way the prototype does: a signed
// gap over the runner-up, and the evidence behind it.
func newSignificanceView(sig *history.Significance, rows []leaderRow) *significanceView {
	sign := "+"
	gap := sig.Gap
	if gap < 0 {
		sign, gap = "−", -gap
	}
	verdict := "within noise"
	if sig.Distinguishable {
		verdict = "is significant"
	}
	v := &significanceView{
		Distinguishable: sig.Distinguishable,
		Head:            fmt.Sprintf("%s%.2f over #2 %s", sign, gap, verdict),
		Class:           "warn",
	}
	if sig.Distinguishable {
		v.Class = "good"
	}
	leader, runnerUp := 0, 0
	if len(rows) > 0 {
		leader = rows[0].Runs
	}
	if len(rows) > 1 {
		runnerUp = rows[1].Runs
	}
	v.Body = fmt.Sprintf("p = %.3f, permutation test, n = %d vs %d", sig.P, leader, runnerUp)
	if !sig.Distinguishable {
		v.Body += " — run an experiment with more repeats before acting."
	} else {
		v.Body += "."
	}
	return v
}

// mdeText renders a detectable effect, or a dash when the scores did not vary
// enough for one to be estimated. Printing 0.00 would read as "detects
// everything", which is the opposite of what an unmeasurable spread means.
func mdeText(mde float64) string {
	if mde <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.2f", mde)
}

// minRankableTasks is the task count below which a profile's score is shown as
// thin evidence. It matches the variance report's threshold: with fewer than
// two tasks no difference can be distinguished from noise.
const minRankableTasks = 2

// heroDiffLimit caps the "what the leader changes" panel.
const heroDiffLimit = 6

// handleOverview renders exploratory historic observations. It deliberately
// does not make a controlled efficiency claim across independent runs.
func (h *handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	scope := r.URL.Query().Get("scope")

	ov, err := history.Overview(r.Context(), h.store, scope)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	page := overviewPage{
		layout:       h.page(r, "overview", "History", "Exploratory profile history", "Independent historic runs are diagnostic only; choose a controlled cohort for efficiency standings."),
		Suites:       ov.Suites,
		MatrixHeads:  matrixHeads(ov.Suites),
		ExcludedRuns: ov.ExcludedRuns,
		TotalRuns:    ov.TotalRuns,
		HasRuns:      scoredProfiles(ov.Profiles) > 0,
	}
	page.ShowScope = true
	page.Scope = scopeItems("/overview", scope, ov.Suites)

	page.Rows = leaderRows(ov.Profiles)
	page.Matrix = matrixRows(ov.Profiles, ov.Suites)
	page.Scatter = scatterDots(ov.Scatter)
	page.ScatterXMax = moneyTick(scatterMax(ov.Scatter))
	// The panel is a summary, not a full inventory: show the first few and
	// count the rest, so a profile that differs in forty skills does not push
	// the leaderboard off the page.
	for i, n := range ov.HeroDiff {
		if i == heroDiffLimit {
			page.HeroDiffMore = len(ov.HeroDiff) - heroDiffLimit
			break
		}
		page.HeroDiff = append(page.HeroDiff, diffNote{
			Sign: n.Sign, Class: signClass(n.Sign), Change: n.Change,
			What: n.Kind + "/" + n.Name, Note: n.Note,
		})
	}
	if ov.Significance != nil {
		page.Significance = newSignificanceView(ov.Significance, page.Rows)
	}
	render(w, overviewTmpl, page)
}

// scoredProfiles counts profiles that have runs in this scope. It is separate
// from the scatter, which additionally needs a solved run to plot a cost.
func scoredProfiles(profiles []history.ProfileScore) int {
	n := 0
	for _, p := range profiles {
		if p.HasRuns {
			n++
		}
	}
	return n
}

func overviewSub(ov history.OverviewReport) string {
	return fmt.Sprintf("%d suites · %d profiles · %d runs", len(ov.Suites), len(ov.Profiles), ov.TotalRuns)
}

func leaderRows(profiles []history.ProfileScore) []leaderRow {
	rows := make([]leaderRow, 0, len(profiles))
	for i, p := range profiles {
		row := leaderRow{
			Rank:         i + 1,
			First:        i == 0 && p.HasRuns,
			Label:        p.Label,
			Hash:         p.Hash,
			ShortHash:    shortHash(p.Hash),
			Architecture: p.Architecture,
			Href:         "/arch/" + p.Hash,
			HasRuns:      p.HasRuns,
			Runs:         p.Runs,
			TokensText:   tokensText(p.MedianTokens),
			CostSort:     "",
			TokensSort:   "",
			TaskCount:    p.TaskCount,
			CacheText:    "—",
		}
		if p.HasRuns {
			row.ScoreText = fmt.Sprintf("%.2f", p.Score)
			if p.ScoreMDE > 0 {
				row.MDEText = fmt.Sprintf("±%.2f", p.ScoreMDE)
			} else {
				row.MDEText = "—"
			}
			// Evidence is strong once a profile has been scored on enough
			// tasks for the interval to mean something.
			row.EvidenceStrong = p.TaskCount >= minRankableTasks
			if p.CacheHitRateOK {
				row.CacheText = fmt.Sprintf("%.0f%%", p.CacheHitRate*100)
			}
			row.ScorePercent = band(p.Score)
			row.CILowPercent, row.CIDeltaPercent = ciSpan(p.ScoreCI)
			if p.ScoreCIOK {
				row.CIText = fmt.Sprintf("±%.2f", (p.ScoreCI[1]-p.ScoreCI[0])/2)
			}
			row.PassText = fmt.Sprintf("%.0f%%", p.PassRate*100)
			if p.CostPerSolvedOK {
				row.CostText = fmt.Sprintf("$%.3f", p.CostPerSolved)
				row.CostSort = strconv.FormatFloat(p.CostPerSolved, 'f', 6, 64)
			} else {
				row.CostText = "—"
			}
			if p.MedianTokens > 0 {
				row.TokensSort = strconv.FormatInt(p.MedianTokens, 10)
			}
		} else {
			row.ScoreText = "—"
			row.PassText = "—"
			row.CostText = "—"
		}
		rows = append(rows, row)
	}
	return rows
}

func matrixRows(profiles []history.ProfileScore, suites []string) []matrixRow {
	rows := make([]matrixRow, 0, len(profiles))
	for _, p := range profiles {
		if !p.HasRuns {
			continue
		}
		row := matrixRow{Label: p.Label}
		for _, suite := range suites {
			score, ok := p.PerSuite[suite]
			if !ok {
				row.Cells = append(row.Cells, matrixCell{Text: "—"})
				continue
			}
			row.Cells = append(row.Cells, matrixCell{
				Text: fmt.Sprintf("%.2f", score),
				Tint: tint(score),
			})
		}
		// The overall column is the profile's scope score.
		row.Cells = append(row.Cells, matrixCell{
			Text: fmt.Sprintf("%.2f", p.Score),
			Tint: tint(p.Score),
		})
		rows = append(rows, row)
	}
	return rows
}

func scatterDots(points []history.ScatterPoint) []scatterDot {
	if len(points) == 0 {
		return nil
	}
	maxCost := scatterMax(points)
	if maxCost <= 0 {
		maxCost = 1
	}
	dots := make([]scatterDot, 0, len(points))
	for i, p := range points {
		x := 20 + (p.CostPerSolved/maxCost)*280
		dot := scatterDot{
			X:     x,
			Y:     210 - math.Min(1, math.Max(0, p.Score))*190,
			Label: shortHash(p.Hash),
			First: i == 0,
		}
		// A label near the right edge would be clipped, so it moves to the
		// left of its dot and anchors to the end.
		if x > 240 {
			dot.LabelX, dot.Anchor = x-8, "end"
		} else {
			dot.LabelX, dot.Anchor = x+8, "start"
		}
		// Profiles that tie on score share a y, so their labels would collide:
		// stagger them above and below the dot.
		dot.LabelY = dot.Y + 3
		if i%2 == 1 {
			dot.LabelY = dot.Y + 14
		}
		dots = append(dots, dot)
	}
	return dots
}

func scatterMax(points []history.ScatterPoint) float64 {
	max := 0.0
	for _, p := range points {
		if p.CostPerSolved > max {
			max = p.CostPerSolved
		}
	}
	return max
}

// band maps a 0..1 score to a 0..100 bar width.
func band(score float64) int {
	return int(math.Round(math.Min(1, math.Max(0, score)) * 100))
}

// ciSpan renders an interval as a start offset and a width, both on the 0..100
// scale the bar uses.
func ciSpan(ci [2]float64) (left, width int) {
	lo, hi := band(ci[0]), band(ci[1])
	if hi < lo {
		lo, hi = hi, lo
	}
	return lo, hi - lo
}

// tint maps a 0..1 score to one of six colour bands.
func tint(score float64) int {
	t := math.Min(1, math.Max(0, score))
	return int(t * 5.999)
}

// moneyTick renders an axis label with enough precision to be useful: a few
// tenths of a cent per solved task is typical, so two decimals would read
// "$0.00" and mean nothing.
func moneyTick(v float64) string {
	switch {
	case v == 0:
		return "$0"
	case v < 0.01:
		return fmt.Sprintf("$%.4f", v)
	case v < 1:
		return fmt.Sprintf("$%.3f", v)
	default:
		return fmt.Sprintf("$%.2f", v)
	}
}

func signClass(sign string) string {
	switch sign {
	case "+":
		return "add"
	case "−":
		return "del"
	default:
		return "chg"
	}
}

// tokensText renders a token count compactly.
func tokensText(n int64) string {
	switch {
	case n <= 0:
		return "—"
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// shortHash abbreviates a profile hash for display.
func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
