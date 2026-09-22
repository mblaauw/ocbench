package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

// runRecord is one execution as reported by `run` and embedded in its JSON.
type runRecord struct {
	RunID            string `json:"run_id"`
	TaskID           string `json:"task_id"`
	RepeatIndex      int    `json:"repeat_index"`
	Status           string `json:"status"`
	DurationMS       int64  `json:"duration_ms"`
	TokensTotal      int64  `json:"tokens_total"`
	ToolCallsTotal   int64  `json:"tool_calls_total"`
	ValidatorsFailed int    `json:"validators_failed"`
}

// runAggregate is the per-task summary printed when --repeat > 1 and always
// present (possibly empty) in the JSON report.
type runAggregate struct {
	TaskID           string  `json:"task_id"`
	Executions       int     `json:"executions"`
	Successes        int     `json:"successes"`
	SuccessRate      float64 `json:"success_rate"`
	MedianTokens     float64 `json:"median_tokens"`
	MinTokens        int64   `json:"min_tokens"`
	MaxTokens        int64   `json:"max_tokens"`
	MedianTools      float64 `json:"median_tools"`
	MinTools         int64   `json:"min_tools"`
	MaxTools         int64   `json:"max_tools"`
	MedianDurationMS float64 `json:"median_duration_ms"`
}

// runReportJSON is the stable machine-readable `run` report.
type runReportJSON struct {
	ProfileHash  string         `json:"profile_hash"`
	ExperimentID string         `json:"experiment_id"`
	Runs         []runRecord    `json:"runs"`
	Aggregates   []runAggregate `json:"aggregates"`
}

// renderRunHuman prints one line per execution, the overall success count, and
// (with --repeat > 1) a per-task aggregate block.
func renderRunHuman(w io.Writer, records []runRecord, repeat int, aggregates []runAggregate) error {
	for i, r := range records {
		if _, err := fmt.Fprintf(w, "%d %s  %s  %s  %d tokens  %d tools\n",
			i+1, r.TaskID, statusLabel(r.Status), formatRunDuration(r.DurationMS),
			r.TokensTotal, r.ToolCallsTotal); err != nil {
			return err
		}
	}
	successes := 0
	for _, r := range records {
		if r.Status == "passed" {
			successes++
		}
	}
	if _, err := fmt.Fprintf(w, "%d/%d successful\n", successes, len(records)); err != nil {
		return err
	}
	if repeat <= 1 {
		return nil
	}
	for _, a := range aggregates {
		if _, err := fmt.Fprintf(w, "\ntask %s: %d/%d successful (%.0f%%)\n",
			a.TaskID, a.Successes, a.Executions, a.SuccessRate*100); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  tokens_total  median %.0f  min %d  max %d\n",
			a.MedianTokens, a.MinTokens, a.MaxTokens); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  tool_calls    median %.0f  min %d  max %d\n",
			a.MedianTools, a.MinTools, a.MaxTools); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  duration      median %s\n",
			formatRunDuration(int64(math.Round(a.MedianDurationMS)))); err != nil {
			return err
		}
	}
	return nil
}

// renderRunJSON prints the stable JSON report. Both slices are always emitted;
// an empty aggregate list is `[]`, never null.
func renderRunJSON(w io.Writer, profileHash, experimentID string, records []runRecord, aggregates []runAggregate) error {
	if records == nil {
		records = []runRecord{}
	}
	if aggregates == nil {
		aggregates = []runAggregate{}
	}
	out := runReportJSON{
		ProfileHash:  profileHash,
		ExperimentID: experimentID,
		Runs:         records,
		Aggregates:   aggregates,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// aggregateRuns summarises records per task in the supplied task order.
func aggregateRuns(records []runRecord, taskOrder []string) []runAggregate {
	byTask := make(map[string][]runRecord, len(taskOrder))
	for _, r := range records {
		byTask[r.TaskID] = append(byTask[r.TaskID], r)
	}
	out := make([]runAggregate, 0, len(taskOrder))
	for _, id := range taskOrder {
		rs := byTask[id]
		if len(rs) == 0 {
			continue
		}
		tokens := make([]int64, 0, len(rs))
		tools := make([]int64, 0, len(rs))
		durations := make([]int64, 0, len(rs))
		successes := 0
		for _, r := range rs {
			tokens = append(tokens, r.TokensTotal)
			tools = append(tools, r.ToolCallsTotal)
			durations = append(durations, r.DurationMS)
			if r.Status == "passed" {
				successes++
			}
		}
		out = append(out, runAggregate{
			TaskID:           id,
			Executions:       len(rs),
			Successes:        successes,
			SuccessRate:      float64(successes) / float64(len(rs)),
			MedianTokens:     medianInts(tokens),
			MinTokens:        minInts(tokens),
			MaxTokens:        maxInts(tokens),
			MedianTools:      medianInts(tools),
			MinTools:         minInts(tools),
			MaxTools:         maxInts(tools),
			MedianDurationMS: medianInts(durations),
		})
	}
	return out
}

// statusLabel maps a runner status to its short display label.
func statusLabel(status string) string {
	switch status {
	case "passed":
		return "PASS"
	case "failed":
		return "FAIL"
	case "error":
		return "ERROR"
	case "timeout":
		return "TIMEOUT"
	case "dry_run":
		return "DRY"
	default:
		return strings.ToUpper(status)
	}
}

// formatRunDuration renders milliseconds as a compact Go duration.
func formatRunDuration(ms int64) string {
	if ms <= 0 {
		return "0s"
	}
	return (time.Duration(ms) * time.Millisecond).String()
}

// medianInts returns the middle value for an odd count and the mean of the two
// middle values for an even count.
func medianInts(values []int64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	if n%2 == 1 {
		return float64(sorted[n/2])
	}
	return float64(sorted[n/2-1]+sorted[n/2]) / 2
}

func minInts(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

func maxInts(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	max := values[0]
	for _, v := range values[1:] {
		if v > max {
			max = v
		}
	}
	return max
}
