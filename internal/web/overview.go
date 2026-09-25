package web

import (
	"fmt"
	"math"
	"net/http"

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
			hints["profiles"] = fmt.Sprintf("%d", len(n))
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
	Rank           int
	First          bool
	Label          string
	Hash           string
	ShortHash      string
	Architecture   string
	Href           string
	HasRuns        bool
	ScoreText      string
	ScorePercent   int
	CILowPercent   int
	CIDeltaPercent int
	PassText       string
	CostText       string
	TokensText     string
	Runs           int
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

	HasRuns      bool
	Rows         []leaderRow
	Suites       []string
	Matrix       []matrixRow
	Scatter      []scatterDot
	ScatterXMax  string
	HeroDiff     []diffNote
	HeroDiffMore int
	Significance *significanceView
	ExcludedRuns int
	TotalRuns    int
}

type significanceView struct {
	Distinguishable bool
	Note            string
	GapText         string
	MDEText         string
	TasksText       string
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

// handleOverview renders the profile-first front page: who scores best, by how
// much, and at what cost.
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
		layout:       h.page(r, "overview", "Overview", "Which setup scores best?", overviewSub(ov)),
		Suites:       ov.Suites,
		ExcludedRuns: ov.ExcludedRuns,
		TotalRuns:    ov.TotalRuns,
		HasRuns:      scoredProfiles(ov.Profiles) > 0,
	}
	page.ShowScope = true
	page.Scope = scopeItems("/", scope, ov.Suites)

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
		page.Significance = &significanceView{
			Distinguishable: ov.Significance.Distinguishable,
			Note:            ov.Significance.Note,
			GapText:         fmt.Sprintf("%.2f", ov.Significance.Gap),
			MDEText:         mdeText(ov.Significance.MDE),
			TasksText:       fmt.Sprintf("%d", ov.Significance.TasksMax),
		}
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
	scored := 0
	for _, p := range ov.Profiles {
		if p.HasRuns {
			scored++
		}
	}
	sub := fmt.Sprintf("%d profiles · %d scored · %d runs · %d suites",
		len(ov.Profiles), scored, ov.TotalRuns, len(ov.Suites))
	if ov.ExcludedRuns > 0 {
		sub += fmt.Sprintf(" · %d runs excluded (older suite versions)", ov.ExcludedRuns)
	}
	return sub
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
			row.PassText = fmt.Sprintf("%.0f%%", p.PassRate*100)
			if p.CostPerSolvedOK {
				row.CostText = fmt.Sprintf("$%.3f", p.CostPerSolved)
			} else {
				row.CostText = "—"
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
