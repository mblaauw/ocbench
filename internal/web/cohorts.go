package web

import (
	"fmt"
	"net/http"
	"strings"

	"mbl/ocbench/internal/experiment"
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
	Pass                                                   string
}

type cohortPage struct {
	layout
	ID, Name, CreatedAt, Gate, Environments, Status string
	Arms                                            []cohortArmView
	Warnings                                        []string
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
	page := cohortPage{
		layout:       h.page(r, "cohorts", "Cohorts", "Efficiency standing", "Cost and token efficiency after deterministic validation."),
		ID:           summary.Experiment.ID,
		Name:         summary.Experiment.Name,
		CreatedAt:    summary.Experiment.CreatedAt,
		Gate:         summary.Gate,
		Environments: strings.Join(summary.RunnerEnvironments, ", "),
		Status:       status,
		Warnings:     summary.DriftWarnings,
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
		page.Arms = append(page.Arms, cohortArmView{
			Label: arm.Label, ProfileHash: arm.ProfileHash, Tasks: arm.TaskCount, MinRepeats: arm.MinRepeats,
			Pass: fmt.Sprintf("%.0f%%", arm.PassRate*100), Cost: cost, Tokens: tokens,
			Environments: environments, Status: armStatus,
		})
	}
	render(w, cohortTmpl, page)
}
