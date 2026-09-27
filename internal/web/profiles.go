package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"mbl/ocbench/internal/evaluation"
	"mbl/ocbench/internal/history"
	"mbl/ocbench/internal/profile"
)

// profileRow is one line of the profile index.
type profileRow struct {
	Hash           string
	ShortHash      string
	Summary        string
	OpenCode       string
	Created        string
	ComponentCount int
	RunCount       int
	Href           string
}

// profilesPage lists every fingerprinted profile.
type profilesPage struct {
	layout
	Rows []profileRow
}

// agentRow is one agent on the architecture page.
type agentRow struct {
	Name        string
	Mode        string
	Model       string
	Variant     string
	Steps       int
	Temperature string
	Native      bool
	Description string
	Prompt      string
	Tools       []string
	ToolOff     []string
	Permissions []permissionRow
	TaskRules   []string
	Options     []optionRow
	Primary     bool
	// Usage fields are aggregated from stored runs for the prototype-style
	// summary cards. They remain blank when this profile has no measured usage.
	MessagesPerRun string
	TokenShare     string
	TokenPercent   int
	HasUsage       bool
}

// optionRow is one model option set on an agent.
type optionRow struct {
	Name  string
	Value string
}

// permissionRow is one captured permission rule.
type permissionRow struct {
	Permission string
	Pattern    string
	Action     string
}

// instructionRow is one instruction file with its captured text.
type instructionRow struct {
	Scope    string
	Path     string
	SHA      string
	Text     string
	Captured bool
}

// skillRow is one configured skill. The captured body is deliberately not
// rendered: it is library content shared by every profile, so the description,
// location and content hash are what distinguish one profile from another.
type skillRow struct {
	Name        string
	Description string
	Location    string
	SHA         string
}

// changeNoteRow is one difference against a reference profile.
type changeNoteRow struct {
	Sign   string
	Kind   string
	Name   string
	Note   string
	Change string
}

// profilePageView is the architecture detail for one profile.
type profilePageView struct {
	layout
	Hash           string
	ShortHash      string
	OpenCode       string
	Summary        string
	ComponentCount int
	RunCount       int
	Snapshot       string

	// Stats is the strip across the top: score, pass rate, cost and tokens, with
	// deltas when another profile is selected to compare against.
	Stats     []archStat
	Primary   *agentRow
	Agents    []agentRow
	Subagents []agentRow
	Edges     []edgeRow
	// Tree is the edges grouped by primary agent, which is how the prototype
	// draws the delegation: a trunk per primary, a stub per subagent.
	Tree         []treeGroup
	Instructions []instructionRow
	Skills       []skillRow
	MCP          []mcpRow
	Plugins      []string
	Components   []componentView

	// CapturesAvailable is false when no capture files exist for this profile,
	// which is normal for a profile that only ever existed in the database.
	CapturesAvailable bool
	CaptureNote       string

	// Against is set when comparing this profile with another.
	Against      string
	AgainstShort string
	AgainstHref  string
	Changes      []changeNoteRow
	CompareChips []filterChip
}

// edgeRow is one primary-agent to subagent permission edge.
type edgeRow struct {
	From string
	To   string
}

// archStat is one figure in the architecture page's stat strip. Delta is the
// change against the profile being compared with, and is empty when there is
// nothing to compare against.
type archStat struct {
	Label string
	Value string
	Delta string
	Class string
}

// treeGroup is one primary agent and the subagents it may call, which is how the
// prototype draws the tree: a trunk per primary with a stub per subagent.
type treeGroup struct {
	Primary string
	Subs    []string
}

// mcpRow is one MCP server with its configuration keys.
type mcpRow struct {
	Name string
	Keys string
}

// handleProfiles lists the fingerprinted profiles.
func (h *handler) handleProfiles(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	rows, err := h.store.ListProfiles(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	counts := h.runCountsByProfile(r)

	page := profilesPage{
		layout: h.page(r, "profiles", "Profiles", "Profiles",
			"Every configuration fingerprint the store has recorded, with the runs scored against it."),
	}
	for _, row := range rows {
		view := profileRow{
			Hash:      row.ProfileHash,
			ShortHash: shortHash(row.ProfileHash),
			OpenCode:  row.OpenCodeVersion,
			Created:   shortDate(row.CreatedAt),
			Href:      "/arch/" + row.ProfileHash,
		}
		if _, comps, err := h.store.GetProfileByHash(r.Context(), row.ProfileHash); err == nil {
			if p, err := profile.FromRows(&row, comps); err == nil {
				view.Summary = profile.NewView(p).Summary()
				view.ComponentCount = len(p.Components)
			}
		}
		view.RunCount = counts[row.ProfileHash]
		page.Rows = append(page.Rows, view)
	}
	render(w, profilesTmpl, page)
}

// runCountsByProfile counts the runs recorded against each profile hash.
func (h *handler) runCountsByProfile(r *http.Request) map[string]int {
	out := map[string]int{}
	runs, err := h.store.ListRuns(r.Context(), 0, "")
	if err != nil {
		return out
	}
	for _, run := range runs {
		out[run.ProfileHash]++
	}
	return out
}

// handleProfile renders one profile's architecture: the agents it configures,
// which of them may call which, the instruction text and skill bodies behind the
// fingerprint, and — when ?against=<hash> is given — what changed.
func (h *handler) handleProfile(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	hash := r.PathValue("hash")
	row, comps, err := h.store.GetProfileByHash(r.Context(), hash)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	p, err := profile.FromRows(row, comps)
	if err != nil {
		http.Error(w, "profile could not be decoded", http.StatusInternalServerError)
		return
	}

	view := profile.NewView(p)
	page := profilePageView{
		layout:         h.page(r, "profiles", "Profiles", "Architecture", ""),
		Hash:           row.ProfileHash,
		ShortHash:      shortHash(row.ProfileHash),
		OpenCode:       row.OpenCodeVersion,
		Summary:        view.Summary(),
		ComponentCount: len(p.Components),
		Components:     componentRowViews(comps),
		RunCount:       h.runCountsByProfile(r)[row.ProfileHash],
		Snapshot:       stampText(row.CreatedAt),
	}
	page.PageTitle = view.Summary()
	page.Sub = fmt.Sprintf("Profile %s · OpenCode %s · %d components · %d runs",
		page.ShortHash, row.OpenCodeVersion, len(p.Components), page.RunCount)

	// The capture files hold the text the fingerprint reduced to hashes. A
	// profile loaded from the database alone has none, which is not an error.
	set, err := profile.ReadCaptures(h.captureDir(row.ProfileHash))
	if err != nil {
		page.CaptureNote = "capture files could not be read: " + err.Error()
	} else if !set.Available {
		page.CaptureNote = "No capture files for this profile, so prompt and instruction text is not shown."
	} else {
		page.CapturesAvailable = true
	}
	page.Primary, page.Agents, page.Subagents = agentRows(view, set)
	h.addProfileUsage(r, page.Hash, page.Primary, page.Subagents)
	page.Edges = edgeRows(view)
	page.Tree = treeGroups(page.Edges)
	page.Stats = h.archStats(r, row.ProfileHash, "")
	page.Instructions = instructionRows(view, set)
	page.Skills = skillRows(view, set)
	for _, m := range view.MCP {
		page.MCP = append(page.MCP, mcpRow{Name: m.Name, Keys: strings.Join(m.Keys, ", ")})
	}
	for _, pl := range view.Plugins {
		page.Plugins = append(page.Plugins, pl.Spec)
	}
	page.CompareChips = h.profileCompareChips(r, page.Hash)
	if against := r.URL.Query().Get("against"); against != "" && against != page.Hash {
		otherRow, otherComps, err := h.store.GetProfileByHash(r.Context(), against)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		other, err := profile.FromRows(otherRow, otherComps)
		if err != nil {
			http.Error(w, "comparison profile could not be decoded", http.StatusInternalServerError)
			return
		}
		page.Against = other.Hash
		page.AgainstShort = shortHash(other.Hash)
		page.Stats = h.archStats(r, row.ProfileHash, other.Hash)
		for _, note := range profile.DiffNotes(other, p) {
			page.Changes = append(page.Changes, changeNoteRow{
				Sign: note.Sign, Kind: note.Kind, Name: note.Name, Note: note.Note, Change: note.Change,
			})
		}
	}
	render(w, profileTmpl, page)
}

// addProfileUsage attaches measured per-agent messages/run and token share to
// the compact architecture summary. It derives everything from stored run
// metrics and leaves the cards explicitly unmeasured when those metrics are
// absent. Agent metric keys are sanitised the way the runner writes them, so a
// configured name like "code-reviewer" still matches its usage.
func (h *handler) addProfileUsage(r *http.Request, hash string, primary *agentRow, subs []agentRow) {
	runs, err := h.store.ListRunsByProfile(r.Context(), hash)
	if err != nil || len(runs) == 0 {
		return
	}
	type usage struct {
		messages int
		tokens   float64
	}
	byName := map[string]*usage{}
	totalTokens := 0.0
	measuredRuns := 0
	for _, run := range runs {
		rows, err := h.store.GetRunMetrics(r.Context(), run.ID)
		if err != nil {
			continue
		}
		metrics := make(map[string]float64, len(rows))
		for _, m := range rows {
			if m.ValueNum != nil {
				metrics[m.Name] = *m.ValueNum
			}
		}
		usages := history.AgentUsages(metrics)
		if len(usages) == 0 {
			continue
		}
		measuredRuns++
		for _, u := range usages {
			current := byName[u.Name]
			if current == nil {
				current = &usage{}
				byName[u.Name] = current
			}
			current.messages += u.Messages
			current.tokens += u.Tokens
			totalTokens += u.Tokens
		}
	}
	if measuredRuns == 0 {
		return
	}
	decorate := func(agent *agentRow) {
		if agent == nil {
			return
		}
		// The stored key is the sanitised name, not the configured one.
		u := byName[evaluation.SanitizeName(agent.Name)]
		if u == nil {
			return
		}
		agent.HasUsage = true
		agent.MessagesPerRun = fmt.Sprintf("%.1f", float64(u.messages)/float64(measuredRuns))
		if totalTokens > 0 {
			agent.TokenPercent = int(u.tokens/totalTokens*100 + 0.5)
			agent.TokenShare = fmt.Sprintf("%d%%", agent.TokenPercent)
		}
	}
	decorate(primary)
	for i := range subs {
		decorate(&subs[i])
	}
}

// profileCompareChips links the other profiles this one can be compared with.
func (h *handler) profileCompareChips(r *http.Request, hash string) []filterChip {
	against := r.URL.Query().Get("against")
	out := []filterChip{{
		Label: "Nothing", Value: "", Href: "/arch/" + hash, Current: against == "",
	}}
	rows, err := h.store.ListProfiles(r.Context())
	if err != nil {
		return out
	}
	for _, row := range rows {
		if row.ProfileHash == hash {
			continue
		}
		out = append(out, filterChip{
			Label: shortHash(row.ProfileHash), Value: row.ProfileHash,
			Href:    "/arch/" + hash + "?against=" + row.ProfileHash,
			Current: against == row.ProfileHash,
		})
	}
	return out
}

// stampText renders an RFC3339 stamp as "YYYY-MM-DD HH:MM", the precision the
// prototype shows for a profile snapshot. shortDate drops the time, which would
// make two snapshots on the same day look identical.
func stampText(stamp string) string {
	if len(stamp) >= 16 {
		return stamp[:10] + " " + stamp[11:16]
	}
	return stamp
}

// captureDir is where the capture files for a profile hash live. It is empty
// when the handler was built without paths, which disables capture rendering.
func (h *handler) captureDir(hash string) string {
	if h.paths.Profiles == "" {
		return ""
	}
	return filepath.Join(h.paths.Profiles, hash)
}

// agentRows projects the view into the rows the architecture page renders,
// joining the captured prompt text and permission rules where they exist.
func agentRows(view profile.View, set profile.CaptureSet) (*agentRow, []agentRow, []agentRow) {
	captured := map[string]profile.CapturedAgent{}
	for _, a := range set.Agents {
		captured[a.Name] = a
	}

	var rows []agentRow
	var primary *agentRow
	for _, a := range view.Agents {
		row := agentRow{
			Name:        a.Name,
			Mode:        a.Mode,
			Model:       a.Model,
			Variant:     a.Variant,
			Steps:       a.Steps,
			Native:      a.Native,
			Description: a.Description,
			Primary:     a.Mode == "primary",
		}
		for _, name := range sortedToolNames(a.Tools, true) {
			row.Tools = append(row.Tools, name)
		}
		for _, name := range sortedToolNames(a.Tools, false) {
			row.ToolOff = append(row.ToolOff, name)
		}
		for _, rule := range a.TaskRules {
			row.TaskRules = append(row.TaskRules, rule.Pattern+" → "+rule.Action)
		}
		if a.Temperature != nil {
			row.Temperature = fmt.Sprintf("%g", *a.Temperature)
		}
		for _, name := range sortedOptionNames(a.Options) {
			row.Options = append(row.Options, optionRow{
				Name: name, Value: formatOption(a.Options[name]),
			})
		}
		if cap, ok := captured[a.Name]; ok {
			row.Prompt = cap.Prompt
			if cap.Temperature != nil && row.Temperature == "" {
				row.Temperature = fmt.Sprintf("%g", *cap.Temperature)
			}
			for _, perm := range cap.Permissions {
				row.Permissions = append(row.Permissions, permissionRow{
					Permission: perm.Permission, Pattern: perm.Pattern, Action: perm.Action,
				})
			}
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Primary != rows[j].Primary {
			return rows[i].Primary
		}
		return rows[i].Name < rows[j].Name
	})
	for i := range rows {
		if rows[i].Primary && primary == nil {
			primary = &rows[i]
		}
	}

	var subs []agentRow
	for _, row := range rows {
		if row.Mode == "subagent" {
			subs = append(subs, row)
		}
	}
	return primary, rows, subs
}

// sortedOptionNames returns the model option keys in a stable order.
func sortedOptionNames(options map[string]any) []string {
	out := make([]string, 0, len(options))
	for name := range options {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// formatOption renders a model option value compactly.
func formatOption(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return fmt.Sprintf("%t", v)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(encoded)
	}
}

// sortedToolNames returns the tool names the agent enables (or disables).
func sortedToolNames(tools map[string]bool, enabled bool) []string {
	var out []string
	for name, on := range tools {
		if on == enabled {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// edgeRows is the subagent tree: which primary agent may call which subagent.
func edgeRows(view profile.View) []edgeRow {
	arch := view.Architecture()
	out := make([]edgeRow, 0, len(arch.Edges))
	for _, e := range arch.Edges {
		out = append(out, edgeRow{From: e.From, To: e.To})
	}
	return out
}

// archStats builds the profile's stat strip, with deltas against compareWith
// when a comparison is selected.
func (h *handler) archStats(r *http.Request, hash, compareWith string) []archStat {
	ov, err := history.Overview(r.Context(), h.store, history.ScopeAll)
	if err != nil {
		return nil
	}
	find := func(want string) *history.ProfileScore {
		for i := range ov.Profiles {
			if ov.Profiles[i].Hash == want {
				return &ov.Profiles[i]
			}
		}
		return nil
	}
	self := find(hash)
	if self == nil || !self.HasRuns {
		return nil
	}
	var other *history.ProfileScore
	if compareWith != "" {
		other = find(compareWith)
	}

	stat := func(label, value string, mine float64, better func(float64) bool) archStat {
		s := archStat{Label: label, Value: value}
		if other == nil {
			return s
		}
		// The delta is against the other profile's same figure, coloured by
		// whether this profile is better or worse on it.
		var theirs float64
		switch label {
		case "Score":
			theirs = other.Score
		case "Pass rate":
			theirs = other.PassRate * 100
		case "Cost / solved":
			theirs = other.CostPerSolved
		case "Median tokens":
			theirs = float64(other.MedianTokens)
		}
		d := mine - theirs
		if d == 0 {
			s.Delta = "±0"
			return s
		}
		sign := "+"
		if d < 0 {
			sign = "−"
		}
		switch label {
		case "Cost / solved":
			s.Delta = sign + fmt.Sprintf("%.3f", abs(d))
			if abs(d) < 0.0005 {
				// Below the displayed precision: a signed zero reads as noise.
				s.Delta = "±0"
				return s
			}
		case "Median tokens":
			s.Delta = sign + tokensText(int64(abs(d)))
			if int64(abs(d)) == 0 {
				s.Delta = "±0"
				return s
			}
		default:
			s.Delta = sign + fmt.Sprintf("%.2f", abs(d))
			if abs(d) < 0.005 {
				s.Delta = "±0"
				return s
			}
		}
		if better(d) {
			s.Class = "good"
		} else {
			s.Class = "bad"
		}
		return s
	}

	out := []archStat{
		stat("Score", fmt.Sprintf("%.2f", self.Score), self.Score, func(d float64) bool { return d > 0 }),
		stat("Pass rate", fmt.Sprintf("%.0f%%", self.PassRate*100), self.PassRate*100, func(d float64) bool { return d > 0 }),
	}
	if self.CostPerSolvedOK {
		out = append(out, stat("Cost / solved", fmt.Sprintf("$%.3f", self.CostPerSolved),
			self.CostPerSolved, func(d float64) bool { return d < 0 }))
	} else {
		out = append(out, archStat{Label: "Cost / solved", Value: "—"})
	}
	out = append(out, stat("Median tokens", tokensText(self.MedianTokens),
		float64(self.MedianTokens), func(d float64) bool { return d < 0 }))
	return out
}

// abs returns the magnitude of a float.
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// treeGroups groups the edges by their primary agent, keeping the sorted order
// the edges already have.
func treeGroups(edges []edgeRow) []treeGroup {
	var out []treeGroup
	for _, e := range edges {
		if len(out) == 0 || out[len(out)-1].Primary != e.From {
			out = append(out, treeGroup{Primary: e.From})
		}
		out[len(out)-1].Subs = append(out[len(out)-1].Subs, e.To)
	}
	return out
}

// instructionRows joins the configured instruction files with their text.
func instructionRows(view profile.View, set profile.CaptureSet) []instructionRow {
	text := map[string]string{}
	for _, i := range set.Instructions {
		text[i.Scope] = i.Text
	}
	out := make([]instructionRow, 0, len(view.Instructions))
	for _, i := range view.Instructions {
		body, ok := text[i.Scope]
		out = append(out, instructionRow{
			Scope: i.Scope, Path: i.Path, SHA: i.SHA, Text: body, Captured: ok,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out
}

// skillRows joins the configured skills with their captured bodies.
func skillRows(view profile.View, set profile.CaptureSet) []skillRow {
	body := map[string]profile.CapturedSkill{}
	for _, s := range set.Skills {
		body[s.Name] = s
	}
	out := make([]skillRow, 0, len(view.Skills))
	for _, s := range view.Skills {
		row := skillRow{Name: s.Name, Description: s.Description}
		if cap, ok := body[s.Name]; ok {
			row.Location = cap.Location
			row.SHA = cap.ContentSHA256
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
