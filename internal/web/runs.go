package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/profile"
)

// runRow is one line of the runs table.
type runRow struct {
	ID           string
	TaskID       string
	Suite        string
	Status       string
	StatusClass  string
	ProfileLabel string
	ProfileShort string
	ScoreText    string
	DurationText string
	TokensText   string
	Added        int
	Removed      int
	StartedDate  string
	StartedTime  string
	Href         string
	Selected     bool
}

// filterChip is one link in the status or profile filter row.
type filterChip struct {
	Label   string
	Href    string
	Current bool
}

// statCell is one figure in the selected run's grid.
type statCell struct {
	Label string
	Value string
}

// validatorRow is one validator outcome, with the excerpt that explains it.
type validatorRow struct {
	Seq        int
	Kind       string
	Name       string
	Status     string
	Class      string
	DurationMS int64
	Excerpt    string
}

// agentCard is one agent in the selected run's architecture panel.
type agentCard struct {
	Name         string
	Model        string
	Variant      string
	Primary      bool
	Messages     int
	ToolCalls    int
	TokensText   string
	SharePercent int
	ShareText    string
}

// runAside is the selected run's detail column.
type runAside struct {
	ID           string
	TaskID       string
	Suite        string
	ProfileShort string
	ProfileHash  string
	Status       string
	StatusClass  string
	Stats        []statCell
	Validators   []validatorRow

	Primary        *agentCard
	Subagents      []agentCard
	HasSubagents   bool
	Skills         []string
	MCP            []string
	ComponentCount int
}

// runsPage backs the runs listing with its selected-run detail.
type runsPage struct {
	layout
	StatusChips  []filterChip
	ProfileChips []filterChip
	Rows         []runRow
	Shown        int
	Total        int
	Selected     *runAside
}

// runFilters are the query filters the runs page understands.
type runFilters struct {
	status  string
	profile string
	run     string
}

func parseRunFilters(r *http.Request) runFilters {
	q := r.URL.Query()
	return runFilters{status: q.Get("status"), profile: q.Get("profile"), run: q.Get("run")}
}

// handleList renders the runs page: every persisted run, newest first, with the
// selected run's validators and architecture beside it.
func (h *handler) handleList(w http.ResponseWriter, r *http.Request) {
	h.renderRuns(w, r, parseRunFilters(r), "")
}

// handleRun renders the same page with one run selected, so a run has a stable
// URL as well as a row in the table.
func (h *handler) handleRun(w http.ResponseWriter, r *http.Request) {
	filters := parseRunFilters(r)
	filters.run = r.PathValue("id")
	h.renderRuns(w, r, filters, filters.run)
}

func (h *handler) renderRuns(w http.ResponseWriter, r *http.Request, filters runFilters, selectID string) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	all, err := history.List(r.Context(), h.store, "", 0)
	if err != nil {
		writeStoreError(w, err)
		return
	}

	page := runsPage{
		layout:      h.page(r, "runs", "History", "Runs", "Every run persisted to the store, newest first. Select one for validators and per-agent roll-up."),
		Total:       len(all),
		StatusChips: statusChips(filters),
	}
	page.ProfileChips = profileChips(filters, all)

	visible := make([]history.RunDetail, 0, len(all))
	for _, detail := range all {
		if filters.status != "" && detail.Run.Status != filters.status {
			continue
		}
		if filters.profile != "" && detail.Run.ProfileHash != filters.profile {
			continue
		}
		visible = append(visible, detail)
	}
	page.Shown = len(visible)

	// The selected run defaults to the newest visible one, so the detail column
	// is never empty on a store that has runs.
	selected := -1
	if selectID == "" {
		selectID = filters.run
	}
	for i, detail := range visible {
		if detail.Run.ID == selectID {
			selected = i
			break
		}
	}
	if selected < 0 {
		if selectID != "" {
			// The caller named a run that is not visible under this filter:
			// answering with a different run would be a lie.
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		if len(visible) > 0 {
			selected = 0
		}
	}

	for i, detail := range visible {
		row := newRunRow(detail, i == selected)
		row.Href = "/runs/" + detail.Run.ID
		page.Rows = append(page.Rows, row)
	}
	if selected >= 0 {
		page.Selected = h.newRunAside(visible[selected])
	}
	render(w, listTmpl, page)
}

// statusChips links the status filter values the runs table can carry.
func statusChips(filters runFilters) []filterChip {
	values := []struct{ label, value string }{
		{"all", ""}, {"passed", "passed"}, {"failed", "failed"}, {"error", "error"},
	}
	out := make([]filterChip, 0, len(values))
	for _, v := range values {
		out = append(out, filterChip{
			Label:   v.label,
			Href:    runsHref(v.value, filters.profile),
			Current: filters.status == v.value,
		})
	}
	return out
}

// profileChips links the profiles that have runs.
func profileChips(filters runFilters, runs []history.RunDetail) []filterChip {
	seen := map[string]bool{}
	var hashes []string
	for _, d := range runs {
		if !seen[d.Run.ProfileHash] {
			seen[d.Run.ProfileHash] = true
			hashes = append(hashes, d.Run.ProfileHash)
		}
	}
	sort.Strings(hashes)

	out := []filterChip{{Label: "all profiles", Href: runsHref(filters.status, ""), Current: filters.profile == ""}}
	for _, hash := range hashes {
		out = append(out, filterChip{
			Label:   shortHash(hash),
			Href:    runsHref(filters.status, hash),
			Current: filters.profile == hash,
		})
	}
	return out
}

func runsHref(status, profile string) string {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if profile != "" {
		q.Set("profile", profile)
	}
	if encoded := q.Encode(); encoded != "" {
		return "/runs?" + encoded
	}
	return "/runs"
}

func newRunRow(detail history.RunDetail, selected bool) runRow {
	run := detail.Run
	row := runRow{
		ID:          run.ID,
		TaskID:      run.TaskID,
		Suite:       run.SuiteName,
		Status:      run.Status,
		StatusClass: statusClass(run.Status),
		Selected:    selected,
	}
	row.StartedDate, row.StartedTime = shortTime(run.StartedAt)
	row.ProfileShort = shortHash(run.ProfileHash)
	if p := detail.Profile; p != nil {
		row.ProfileLabel = profile.NewView(p).Summary()
	} else {
		row.ProfileLabel = row.ProfileShort
	}
	if run.DurationMS != nil {
		row.DurationText = fmt.Sprintf("%.1fs", float64(*run.DurationMS)/1000)
	}
	score, ok := detail.Metrics["score"]
	if !ok {
		// Runs recorded before the weighted score metric existed only carry
		// the binary outcome.
		score = detail.Metrics["success"]
	}
	row.ScoreText = fmt.Sprintf("%.2f", score)
	row.TokensText = tokensText(int64(detail.Metrics["tokens_total"]))
	row.Added = int(detail.Metrics["diff_lines_added"])
	row.Removed = int(detail.Metrics["diff_lines_removed"])
	return row
}

// newRunAside builds the selected run's detail: figures, validators and the
// architecture of the profile that produced it.
func (h *handler) newRunAside(detail history.RunDetail) *runAside {
	run := detail.Run
	aside := &runAside{
		ID:           run.ID,
		TaskID:       run.TaskID,
		Suite:        run.SuiteName,
		ProfileShort: shortHash(run.ProfileHash),
		ProfileHash:  run.ProfileHash,
		Status:       run.Status,
		StatusClass:  statusClass(run.Status),
	}

	duration := "—"
	if run.DurationMS != nil {
		duration = fmt.Sprintf("%.1fs", float64(*run.DurationMS)/1000)
	}
	score := detail.Metrics["score"]
	if _, ok := detail.Metrics["score"]; !ok {
		score = detail.Metrics["success"]
	}
	aside.Stats = []statCell{
		{"Score", fmt.Sprintf("%.2f", score)},
		{"Duration", duration},
		{"Tokens", tokensText(int64(detail.Metrics["tokens_total"]))},
		{"Tool calls", fmt.Sprintf("%.0f", detail.Metrics["tool_calls_total"])},
		{"Files", fmt.Sprintf("%.0f", detail.Metrics["files_changed"])},
		{"Cost", fmt.Sprintf("$%.4f", detail.Metrics["cost"])},
	}

	for _, v := range detail.Validations {
		aside.Validators = append(aside.Validators, validatorRow{
			Seq: v.Seq, Kind: v.Kind, Name: v.Name, Status: v.Status,
			Class: statusClass(v.Status), DurationMS: v.DurationMS,
			Excerpt: firstLines(v.OutputExcerpt, 2),
		})
	}

	if detail.Profile != nil {
		view := profile.NewView(detail.Profile)
		aside.ComponentCount = len(detail.Profile.Components)
		for _, s := range view.Skills {
			aside.Skills = append(aside.Skills, s.Name)
		}
		for _, m := range view.MCP {
			aside.MCP = append(aside.MCP, m.Name)
		}
		primary := newAgentCard(view, view.Primary.DefaultAgent, detail.Metrics, 0, true)
		aside.Primary = &primary
		subagentTokens := detail.Metrics["subagent_tokens_total"]
		for _, usage := range history.AgentUsages(detail.Metrics) {
			if usage.Name == view.Primary.DefaultAgent {
				continue
			}
			card := newAgentCard(view, usage.Name, detail.Metrics, subagentTokens, false)
			aside.Subagents = append(aside.Subagents, card)
		}
		aside.HasSubagents = len(aside.Subagents) > 0
	}
	return aside
}

// newAgentCard renders one agent's configuration and its recorded usage.
func newAgentCard(view profile.View, name string, metrics map[string]float64, total float64, primary bool) agentCard {
	card := agentCard{Name: name, Primary: primary}
	for _, a := range view.Agents {
		if a.Name != name {
			continue
		}
		card.Model = shortModelName(a.Model)
		card.Variant = a.Variant
		break
	}
	for _, usage := range history.AgentUsages(metrics) {
		if usage.Name != name {
			continue
		}
		card.Messages = usage.Messages
		card.ToolCalls = usage.ToolCalls
		card.TokensText = tokensText(int64(usage.Tokens))
		if total > 0 {
			card.SharePercent = int(usage.Tokens / total * 100)
			card.ShareText = fmt.Sprintf("%d%%", card.SharePercent)
		}
		break
	}
	if card.TokensText == "" {
		card.TokensText = "—"
	}
	return card
}

// shortModelName drops the provider prefix from a model id.
func shortModelName(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	return model
}

// statusClass maps a status onto the badge classes the stylesheet defines.
func statusClass(status string) string {
	switch status {
	case "passed":
		return "good"
	case "failed":
		return "bad"
	case "error", "timeout":
		return "warn"
	default:
		return ""
	}
}

// shortTime splits an RFC3339 stamp into the date and time a table cell shows
// on two lines, which keeps the column narrow.
func shortTime(stamp string) (date, clock string) {
	if len(stamp) >= 16 {
		return stamp[5:10], stamp[11:16]
	}
	return stamp, ""
}

// firstLines keeps the first n lines of an excerpt, which is all a table cell
// can show.
func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + " …"
}
