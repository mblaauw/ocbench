package web

import (
	"fmt"
	"net/http"
	"strings"

	"mbl/ocbench/internal/experiment"
	"mbl/ocbench/internal/profile"
)

type cohortListRow struct {
	ID, Name, CreatedAt, Gate, Status, Href string
	Arms                                    int
}

type cohortsPage struct {
	layout
	Rows         []cohortListRow
	Uncontrolled int
}

type cohortArmView struct {
	Label, ProfileHash, Cost, Tokens, Environments, Status string
	Tasks, MinRepeats                                      int
	Pass, Score                                            string
	// Changes and ChangeNote render how this arm's configuration differs from
	// the baseline. ChangeNote is non-empty when Changes is, so the template can
	// tell "identical to the baseline" from "could not be compared".
	Changes    []profile.ChangeNote
	ChangeNote string
}

type cohortPage struct {
	layout
	ID, Name, CreatedAt, Gate, Environments, Status string
	Arms                                            []cohortArmView
	Warnings                                        []string
	Baseline                                        string
	HasChanges                                      bool
}

// hasControlledArms reports whether an experiment has at least two configured
// arms, which is what makes it a controlled cohort rather than a single-profile
// session. Every caller shares one ExperimentArmCounts read per request.
func hasControlledArms(counts map[string]int, id string) bool { return counts[id] >= 2 }

// handleCohorts lists persisted experiments as the controlled benchmark
// cohorts they already are. It never combines independent experiment sessions
// into a global winner.
func (h *handler) handleCohorts(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	experiments, err := h.store.ListExperiments(r.Context(), 0)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	armCounts, err := h.store.ExperimentArmCounts(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	page := cohortsPage{layout: h.page(r, "cohorts", "Cohorts", "Controlled efficiency cohorts", "Each cohort is one interleaved benchmark session. Historic runs are never pooled into a winner.")}
	for _, row := range experiments {
		if !hasControlledArms(armCounts, row.ID) {
			page.Uncontrolled++
			continue
		}
		summary, err := experiment.SummarizeCohort(r.Context(), h.store, row.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		status := "not rankable"
		if summary.Ranked {
			status = "eligible"
		}
		page.Rows = append(page.Rows, cohortListRow{
			ID: row.ID, Name: row.Name, CreatedAt: row.CreatedAt, Gate: summary.Gate,
			Status: status, Arms: len(summary.Arms), Href: "/cohorts/" + row.ID,
		})
	}
	render(w, cohortsTmpl, page)
}

// handleCohort renders one experiment's evidence-gated efficiency standing.
func (h *handler) handleCohort(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	id := r.PathValue("id")
	summary, err := experiment.SummarizeCohort(r.Context(), h.store, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	status := "not rankable"
	if summary.Ranked {
		status = "eligible"
	}
	warnings := summary.DriftWarnings
	if len(summary.Arms) < 2 {
		// A single-arm session is a `run` recorded as an experiment, not a
		// comparison. It stays reachable because links and `experiment show`
		// land here, but it must not read like a measured standing.
		warnings = append([]string{
			"This session is not a controlled cohort: it has one arm, so there is nothing to compare it against. " +
				"Run `ocbench experiment run <suite> --profile a=<overlay> --profile b=<overlay> --repeat 3` to measure one.",
		}, warnings...)
	}
	page := cohortPage{
		layout:       h.page(r, "cohorts", "Cohorts", "Efficiency standing", "Cost and token efficiency after deterministic validation."),
		ID:           summary.Experiment.ID,
		Name:         summary.Experiment.Name,
		CreatedAt:    summary.Experiment.CreatedAt,
		Gate:         summary.Gate,
		Environments: strings.Join(summary.RunnerEnvironments, ", "),
		Status:       status,
		Warnings:     warnings,
		Baseline:     summary.Baseline,
	}
	if page.Environments == "" {
		page.Environments = "unknown"
	}
	for _, arm := range summary.Arms {
		cost := "—"
		if arm.CostPerSolvedOK {
			cost = fmt.Sprintf("$%.6f", arm.CostPerSolved)
		}
		environments := strings.Join(arm.RunnerEnvironments, ", ")
		if environments == "" {
			environments = "unknown"
		}
		armStatus := "excluded"
		if arm.Eligible {
			armStatus = "eligible"
		}
		tokens := "—"
		if arm.MedianTokensOK {
			tokens = tokensText(int64(arm.MedianTokens))
		}
		score := "—"
		if arm.ScoreOK {
			score = fmt.Sprintf("%.2f", arm.Score)
		}
		view := cohortArmView{
			Label: arm.Label, ProfileHash: arm.ProfileHash, Tasks: arm.TaskCount, MinRepeats: arm.MinRepeats,
			Pass: fmt.Sprintf("%.0f%%", arm.PassRate*100), Score: score, Cost: cost, Tokens: tokens,
			Environments: environments, Status: armStatus,
		}
		if arm.Label != summary.Baseline {
			view.Changes = arm.Changes
			switch {
			case arm.Unavailable:
				view.ChangeNote = "Configuration could not be read, so no comparison is available."
			case len(arm.Changes) == 0:
				view.ChangeNote = "No configuration differences from the baseline."
			default:
				page.HasChanges = true
			}
		}
		page.Arms = append(page.Arms, view)
	}
	render(w, cohortTmpl, page)
}
