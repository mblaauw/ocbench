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
					if hasControlledArms(armCounts, cohort.ID) {
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

// leaderRow is one line of the recorded profile observations.
type leaderRow struct {
	Label          string
	ShortHash      string
	Href           string
	HasRuns        bool
	ScoreText      string
	PassText       string
	CostText       string
	TokensText     string
	ScorePercent   int
	CILowPercent   int
	CIDeltaPercent int
	// CostSort and TokensSort are the raw sort keys for the client-side
	// observations table. They are empty when the value is missing, so the
	// script can keep "—" rows last instead of sorting them as zero.
	CostSort   string
	TokensSort string
	Runs       int
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

// overviewPage backs the recorded profile observations.
type overviewPage struct {
	layout

	HasRuns bool
	Rows    []leaderRow
	// MatrixHeads abbreviates the suite names for the matrix header, where a
	// column per suite has to fit beside the profile name.
	MatrixHeads  []string
	Matrix       []matrixRow
	Scatter      []scatterDot
	ScatterXMax  string
	ExcludedRuns int
}

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
		MatrixHeads:  matrixHeads(ov.Suites),
		ExcludedRuns: ov.ExcludedRuns,
		HasRuns:      scoredProfiles(ov.Profiles) > 0,
	}
	page.ShowScope = true
	page.Scope = scopeItems("/overview", scope, ov.Suites)

	page.Rows = leaderRows(ov.Profiles)
	page.Matrix = matrixRows(ov.Profiles, ov.Suites)
	page.Scatter = scatterDots(ov.Scatter)
	page.ScatterXMax = moneyTick(scatterMax(ov.Scatter))
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

func leaderRows(profiles []history.ProfileScore) []leaderRow {
	rows := make([]leaderRow, 0, len(profiles))
	for _, p := range profiles {
		row := leaderRow{
			Label:      p.Label,
			ShortHash:  shortHash(p.Hash),
			Href:       "/arch/" + p.Hash,
			HasRuns:    p.HasRuns,
			Runs:       p.Runs,
			TokensText: tokensText(p.MedianTokens),
		}
		if p.HasRuns {
			row.ScoreText = fmt.Sprintf("%.2f", p.Score)
			row.ScorePercent = band(p.Score)
			row.CILowPercent, row.CIDeltaPercent = ciSpan(p.ScoreCI)
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
