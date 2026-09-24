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
	Rank          int
	First         bool
	Label         string
	Hash          string
	ShortHash     string
	Architecture  string
	Href          string
	HasRuns       bool
	ScoreText     string
	ScorePercent  int
	CILowPercent  int
	CIHighPercent int
	PassText      string
	CostText      string
	TokensText    string
	Runs          int
}

// matrixCell is one profile-by-suite score.
type matrixCell struct {
	Text  string
	Style string
}

// matrixRow is one profile across the suites.
type matrixRow struct {
	Label string
	Cells []matrixCell
}

// scatterDot positions one profile on the score-against-cost chart.
type scatterDot struct {
	X     float64
	Y     float64
	Label string
	First bool
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
	Significance *significanceView
	ExcludedRuns int
	TotalRuns    int
}

type significanceView struct {
	Distinguishable bool
	Note            string
}

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
	page.ScatterXMax = fmt.Sprintf("$%.2f", scatterMax(ov.Scatter))
	for _, n := range ov.HeroDiff {
		page.HeroDiff = append(page.HeroDiff, diffNote{
			Sign: n.Sign, Class: signClass(n.Sign), Change: n.Change,
			What: n.Kind + "/" + n.Name, Note: n.Note,
		})
	}
	if ov.Significance != nil {
		page.Significance = &significanceView{
			Distinguishable: ov.Significance.Distinguishable,
			Note:            ov.Significance.Note,
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
			Href:         "/profiles/" + p.Hash,
			HasRuns:      p.HasRuns,
			Runs:         p.Runs,
			TokensText:   tokensText(p.MedianTokens),
		}
		if p.HasRuns {
			row.ScoreText = fmt.Sprintf("%.2f", p.Score)
			row.ScorePercent = band(p.Score)
			row.CILowPercent, row.CIHighPercent = ciPercents(p.ScoreCI)
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
				Text:  fmt.Sprintf("%.2f", score),
				Style: cellStyle(score),
			})
		}
		// The overall column is the profile's scope score.
		row.Cells = append(row.Cells, matrixCell{
			Text:  fmt.Sprintf("%.2f", p.Score),
			Style: cellStyle(p.Score),
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
		dots = append(dots, scatterDot{
			X:     20 + (p.CostPerSolved/maxCost)*280,
			Y:     210 - math.Min(1, math.Max(0, p.Score))*190,
			Label: shortHash(p.Hash),
			First: i == 0,
		})
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

// ciPercents clamps an interval to the bar's 0..100 range.
func ciPercents(ci [2]float64) (int, int) {
	lo := band(ci[0])
	hi := band(ci[1])
	if hi < lo {
		lo, hi = hi, lo
	}
	return lo, hi
}

// cellStyle tints a matrix cell by score, from the panel colour towards the
// accent, so the grid reads at a glance without a chart.
func cellStyle(score float64) string {
	t := math.Min(1, math.Max(0, score))
	// Blend #171b16 (panel2) towards #7ee787 (the accent green).
	from := [3]int{0x17, 0x1b, 0x16}
	to := [3]int{0x7e, 0xe7, 0x87}
	mix := func(a, b int) int { return a + int(float64(b-a)*t) }
	return fmt.Sprintf("background:#%02x%02x%02x;color:%s",
		mix(from[0], to[0]), mix(from[1], to[1]), mix(from[2], to[2]),
		inkOn(t))
}

// inkOn picks a readable foreground for the blended cell.
func inkOn(t float64) string {
	if t > 0.55 {
		return "#0a0c0a"
	}
	return "var(--ink)"
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
