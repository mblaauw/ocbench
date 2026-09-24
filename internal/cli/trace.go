package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/evaluation"
	"mbl/ocbench/internal/session"
	"mbl/ocbench/internal/trace"
)

func newTraceCmd(d Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "trace <run-id>",
		Short: "Render a run timeline with subagent spans",
		Long: "Render one persisted run as a timeline: steps in order with their tokens and " +
			"duration, each tool call with its status and duration, and subagent spans nested " +
			"under the `task` call that produced them. The trace is rebuilt from the run's stored " +
			"events and session files, so it works for runs recorded before subagent capture " +
			"existed. This command is read-only.",
		Args: usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			st, err := openHistoryStore(cmd.Context(), resolved)
			if err != nil {
				return err
			}
			defer st.Close()

			run, err := st.GetRun(cmd.Context(), args[0])
			if err != nil {
				// A missing run is a usage mistake (exit 2), matching compare;
				// sql.ErrNoRows distinguishes it from other store failures.
				if errors.Is(err, sql.ErrNoRows) {
					return &UsageError{Err: err}
				}
				return err
			}

			events, err := readTraceEvents(run.ArtifactsDir)
			if err != nil {
				return err
			}
			sessions, err := readTraceSessions(run.ArtifactsDir)
			if err != nil {
				return err
			}
			tr := trace.Build(run.ID, run.TaskID, events, sessions)
			return renderTrace(cmd.OutOrStdout(), tr, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// readTraceEvents parses the run's events.jsonl. A missing file is an
// infrastructure error naming the path; a malformed line is skipped, matching
// the runner's stored-event reader.
func readTraceEvents(artifactsDir string) ([]evaluation.Event, error) {
	path := filepath.Join(artifactsDir, "events.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read events %s: %w", path, err)
	}
	var events []evaluation.Event
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		e, perr := evaluation.ParseLine([]byte(line))
		if perr != nil {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

// readTraceSessions parses every captured child export under sessions/. A run
// with no sessions directory (recorded before subagent capture) yields an empty
// map, not an error. An unparseable child file is skipped rather than failing
// the whole command, matching readTraceEvents and the runner's readStoredEvents.
func readTraceSessions(artifactsDir string) (map[string]*session.Session, error) {
	out := make(map[string]*session.Session)
	matches, err := filepath.Glob(filepath.Join(artifactsDir, "sessions", "*.json"))
	if err != nil {
		return nil, err
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read session %s: %w", path, err)
		}
		s, err := session.ParseExport(data)
		if err != nil {
			continue
		}
		id := s.ID
		if id == "" {
			id = strings.TrimSuffix(filepath.Base(path), ".json")
		}
		out[id] = s
	}
	return out, nil
}

// renderTrace writes the human or JSON timeline.
func renderTrace(w io.Writer, tr trace.Trace, asJSON bool) error {
	if asJSON {
		return renderTraceJSON(w, tr)
	}
	return renderTraceHuman(w, tr)
}

// renderTraceHuman prints steps in order with their token total and duration,
// tool lines indented, and subagent spans indented under their `task` line.
func renderTraceHuman(w io.Writer, tr trace.Trace) error {
	if _, err := fmt.Fprintf(w, "run %s  task %s\n", tr.RunID, tr.TaskID); err != nil {
		return err
	}
	for _, s := range tr.Steps {
		if _, err := fmt.Fprintf(w, "step %d  %d tokens  %s", s.Index, s.Tokens.Total, formatRunDuration(s.DurationMS)); err != nil {
			return err
		}
		if s.Retries > 0 || s.Compactions > 0 {
			if _, err := fmt.Fprintf(w, "  retries %d  compactions %d", s.Retries, s.Compactions); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		for _, t := range s.Tools {
			label := t.Tool
			if t.Title != "" {
				label += "  " + t.Title
			}
			if _, err := fmt.Fprintf(w, "  %s  %s  %s\n", label, t.Status, formatRunDuration(t.DurationMS)); err != nil {
				return err
			}
			if t.Child != nil {
				if _, err := fmt.Fprintf(w, "    %s  %s  %d tokens  cost %g\n",
					t.Child.Agent, t.Child.SessionID, t.Child.Tokens.Total, t.Child.Cost); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// traceTokensJSON is the stable token object shared by steps and subagents.
type traceTokensJSON struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Total      int64 `json:"total"`
}

// traceToolCallJSON is one tool call inside a subagent span.
type traceToolCallJSON struct {
	Name       string `json:"name"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms"`
}

// traceSubagentJSON is a delegated child session's usage.
type traceSubagentJSON struct {
	SessionID string              `json:"session_id"`
	Agent     string              `json:"agent"`
	Tokens    traceTokensJSON     `json:"tokens"`
	Cost      float64             `json:"cost"`
	Tools     []traceToolCallJSON `json:"tools"`
}

// traceToolJSON is one tool call in a step; Child is null for non-task tools.
type traceToolJSON struct {
	Tool       string             `json:"tool"`
	Title      string             `json:"title"`
	Status     string             `json:"status"`
	DurationMS int64              `json:"duration_ms"`
	Child      *traceSubagentJSON `json:"child"`
}

// traceStepJSON is one model step.
type traceStepJSON struct {
	Index       int             `json:"index"`
	Tokens      traceTokensJSON `json:"tokens"`
	Cost        float64         `json:"cost"`
	DurationMS  int64           `json:"duration_ms"`
	Retries     int             `json:"retries"`
	Compactions int             `json:"compactions"`
	Tools       []traceToolJSON `json:"tools"`
}

// traceJSON is the stable machine-readable timeline. Every slice is emitted, as
// `[]` when empty.
type traceJSON struct {
	RunID     string              `json:"run_id"`
	TaskID    string              `json:"task_id"`
	Steps     []traceStepJSON     `json:"steps"`
	Subagents []traceSubagentJSON `json:"subagents"`
}

// renderTraceJSON prints the stable timeline report.
func renderTraceJSON(w io.Writer, tr trace.Trace) error {
	out := traceJSON{
		RunID:     tr.RunID,
		TaskID:    tr.TaskID,
		Steps:     make([]traceStepJSON, 0, len(tr.Steps)),
		Subagents: make([]traceSubagentJSON, 0, len(tr.Subagents)),
	}
	for _, s := range tr.Steps {
		step := traceStepJSON{
			Index:       s.Index,
			Tokens:      traceTokensJSONOf(s.Tokens),
			Cost:        s.Cost,
			DurationMS:  s.DurationMS,
			Retries:     s.Retries,
			Compactions: s.Compactions,
			Tools:       make([]traceToolJSON, 0, len(s.Tools)),
		}
		for _, t := range s.Tools {
			tool := traceToolJSON{
				Tool: t.Tool, Title: t.Title, Status: t.Status, DurationMS: t.DurationMS,
			}
			if t.Child != nil {
				child := traceSubagentJSONOf(*t.Child)
				tool.Child = &child
			}
			step.Tools = append(step.Tools, tool)
		}
		out.Steps = append(out.Steps, step)
	}
	for _, s := range tr.Subagents {
		out.Subagents = append(out.Subagents, traceSubagentJSONOf(s))
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func traceSubagentJSONOf(s trace.SubagentSpan) traceSubagentJSON {
	out := traceSubagentJSON{
		SessionID: s.SessionID,
		Agent:     s.Agent,
		Tokens:    traceTokensJSONOf(s.Tokens),
		Cost:      s.Cost,
		Tools:     make([]traceToolCallJSON, 0, len(s.Tools)),
	}
	for _, t := range s.Tools {
		out.Tools = append(out.Tools, traceToolCallJSON{
			Name: t.Name, Title: t.Title, Status: t.Status, DurationMS: t.DurationMS,
		})
	}
	return out
}

func traceTokensJSONOf(t session.Tokens) traceTokensJSON {
	return traceTokensJSON{
		Input:      t.Input,
		Output:     t.Output,
		Reasoning:  t.Reasoning,
		CacheRead:  t.CacheRead,
		CacheWrite: t.CacheWrite,
		Total:      t.Total,
	}
}
