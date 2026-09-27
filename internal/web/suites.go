package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type suiteTaskView struct {
	ID                         string
	Name                       string
	Version                    string
	Tags                       []string
	Timeout                    string
	Difficulty, ExpectedTokens string
	Capabilities               []string
	Validators                 []suiteValidatorView
	HiddenTests                bool
}

type suiteValidatorView struct{ Kind, Name, Weight string }

type suiteView struct {
	Name, Version, Source string
	Tasks                 []suiteTaskView
}

type suitesPage struct {
	layout
	Suites []suiteView
	Tasks  int
}

// handleSuites renders definitions from persisted rows only; it never opens a
// suite source directory or fixture repository.
func (h *handler) handleSuites(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "store unavailable", http.StatusInternalServerError)
		return
	}
	rows, err := h.store.ListSuiteTasks(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	page := suitesPage{layout: h.page(r, "suites", "Catalogue", "Suites & tasks", "Persisted benchmark definitions and task limits.")}
	byID := map[string]int{}
	seenNames := map[string]bool{}
	for _, row := range rows {
		i, ok := byID[row.SuiteID]
		if !ok {
			// A suite revision is immutable. Show the latest persisted revision
			// for each name rather than duplicating historical definitions.
			if seenNames[row.SuiteName] {
				continue
			}
			seenNames[row.SuiteName] = true
			i = len(page.Suites)
			byID[row.SuiteID] = i
			source := "on-disk"
			if row.Source == "embedded" {
				source = "embedded"
			}
			page.Suites = append(page.Suites, suiteView{Name: row.SuiteName, Version: row.SuiteVersion, Source: source})
		}
		var tags []string
		if json.Unmarshal([]byte(row.TagsJSON), &tags) != nil {
			tags = nil
		}
		tags = safeTags(tags)
		var spec struct {
			Difficulty     string   `json:"difficulty"`
			Capabilities   []string `json:"capabilities"`
			ExpectedTokens int      `json:"expected_tokens"`
			HiddenTests    bool     `json:"hidden_tests"`
			Validators     []struct {
				Kind   string  `json:"Kind"`
				Name   string  `json:"Name"`
				Weight float64 `json:"Weight"`
			} `json:"validators"`
		}
		_ = json.Unmarshal([]byte(row.SpecJSON), &spec)
		task := suiteTaskView{
			ID: row.TaskID, Name: row.Name, Version: row.Version, Tags: tags,
			Timeout:    durationText(row.TimeoutSeconds),
			Difficulty: spec.Difficulty, Capabilities: spec.Capabilities, HiddenTests: spec.HiddenTests,
		}
		if spec.ExpectedTokens > 0 {
			task.ExpectedTokens = tokensText(int64(spec.ExpectedTokens))
		}
		for _, validator := range spec.Validators {
			weight := "1"
			if validator.Weight > 0 {
				weight = fmt.Sprintf("%g", validator.Weight)
			}
			task.Validators = append(task.Validators, suiteValidatorView{Kind: validator.Kind, Name: validator.Name, Weight: weight})
		}
		page.Suites[i].Tasks = append(page.Suites[i].Tasks, task)
		page.Tasks++
	}
	render(w, suitesTmpl, page)
}

// durationText renders a timeout without collapsing sub-minute values to "0m".
func durationText(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds%60 == 0 {
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
}

// safeTags prevents historical prompt-like values from being presented as tags.
func safeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > 32 || strings.ContainsAny(tag, "\n\r\t") || len(strings.Fields(tag)) > 2 {
			continue
		}
		out = append(out, tag)
	}
	return out
}
