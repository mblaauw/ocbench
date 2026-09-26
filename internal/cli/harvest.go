package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/harvest"
)

// promptExcerpt is how much of a user turn the listing shows.
const promptExcerpt = 72

// newHarvestCmd builds `ocbench harvest`: it proposes benchmark tasks from the
// user's own OpenCode history.
//
// It exists because a synthetic corpus measures general capability, not whether
// a configuration suits the work someone actually does. Each candidate is a
// user turn paired with the commits that landed while it was being worked on,
// which is what a task needs: what was asked, and the change that answered it.
//
// It proposes and never publishes: a harvested task is built from a private
// repository, so the caller decides what to do with it. The session database is
// opened read-only.
func newHarvestCmd(d Deps) *cobra.Command {
	var (
		dbPath     string
		repo       string
		minPrompt  int
		limit      int
		maxCommits int
		maxFiles   int
		maxLines   int
		exportDir  string
		excludes   []string
		index      int
		asJSON     bool
	)
	cmd := &cobra.Command{
		Use:   "harvest",
		Short: "Propose benchmark tasks from your own OpenCode history",
		Long: "Propose benchmark tasks from real work recorded in an OpenCode session database.\n\n" +
			"Each candidate pairs one user turn with the commits that landed before the next " +
			"substantive instruction, because that pair is what a task needs: the request and the " +
			"change that answered it. Acknowledgements are not tasks, subagent sessions are " +
			"skipped, and a turn whose window contains no commit is not proposed.\n\n" +
			"The database is opened read-only and is never modified. Nothing is written: the " +
			"command only lists what it found, because a harvested task is built from private " +
			"code and only you can decide whether it should become a task.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			if dbPath == "" {
				dbPath = resolved.Paths.OpenCodeDB
			}
			if _, err := os.Stat(dbPath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return &UsageError{Err: fmt.Errorf(
						"no OpenCode database at %s; pass --db to point at one", dbPath)}
				}
				return err
			}
			candidates, err := harvest.Candidates(cmd.Context(), harvest.Options{
				DBPath: dbPath, Repo: repo, MinPrompt: minPrompt, Limit: limit,
				MaxCommits: maxCommits, MaxFiles: maxFiles, MaxLines: maxLines,
			})
			if err != nil {
				return err
			}
			if exportDir == "" {
				return renderHarvest(cmd.OutOrStdout(), dbPath, candidates, asJSON)
			}
			if index < 1 || index > len(candidates) {
				return &UsageError{Err: fmt.Errorf(
					"--index must be between 1 and %d, got %d", len(candidates), index)}
			}
			written, verification, err := harvest.Export(cmd.Context(), candidates[index-1], harvest.ExportOptions{
				Dir: exportDir, Exclude: excludes,
			})
			if err != nil {
				return err
			}
			return renderExport(cmd.OutOrStdout(), written, candidates[index-1], verification)
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "OpenCode session database (default config paths)")
	cmd.Flags().StringVar(&repo, "repo", "", "only sessions in this repository worktree")
	cmd.Flags().IntVar(&minPrompt, "min-prompt", harvest.DefaultMinPrompt,
		"shortest user turn to consider a task")
	cmd.Flags().IntVar(&limit, "limit", 0, "maximum number of candidates (0 for all)")
	cmd.Flags().IntVar(&maxCommits, "max-commits", 0,
		"drop candidates spanning more commits than this (0 for no cap)")
	cmd.Flags().IntVar(&maxFiles, "max-files", 0,
		"drop candidates touching more files than this (0 for no cap)")
	cmd.Flags().IntVar(&maxLines, "max-lines", 0,
		"drop candidates changing more lines than this (0 for no cap)")
	cmd.Flags().StringVar(&exportDir, "export", "",
		"write candidate --index as a task scaffold under this directory")
	cmd.Flags().StringArrayVar(&excludes, "exclude", nil,
		"drop this path (or tree) from the exported fixture; repeatable")
	cmd.Flags().IntVar(&index, "index", 0, "which candidate to export, counting from 1")
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	return cmd
}

// renderExport explains what was written and, just as importantly, what is
// still missing: a harvested task is a scaffold, not a finished task.
func renderExport(w io.Writer, dir string, c harvest.Candidate, v harvest.Verification) error {
	fmt.Fprintf(w, "Wrote a task scaffold to %s\n\n", dir)
	switch {
	case v.OK():
		fmt.Fprintf(w, "Checked: %s fails on the fixture and passes with the reference, which is\n",
			strings.Join(v.Command, " "))
		fmt.Fprintln(w, "the property every task is held to.")
	case !v.Checked:
		fmt.Fprintln(w, "Not checked: the scaffold has no runnable command validator, so nothing")
		fmt.Fprintln(w, "was proven. Write one before running the task.")
	default:
		fmt.Fprintf(w, "Check FAILED: %s\n", v.Detail)
		fmt.Fprintln(w, "The task is written, but it is not honest yet: fix it before using it.")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "It is NOT finished. Before running it:")
	fmt.Fprintln(w, "  1. Rewrite prompt.md as a standalone instruction. The recorded user turn was")
	fmt.Fprintf(w, "     %q,\n", excerpt(c.Prompt, 48))
	fmt.Fprintln(w, "     which is a continuation of a conversation and states no goal on its own.")
	fmt.Fprintln(w, "  2. Confirm the validator in task.yaml, and add a diff validator if the change")
	fmt.Fprintln(w, "     must stay inside particular files.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "It is built from private code: move it into a suite only if you are content for")
	fmt.Fprintln(w, "that code to live where the suite lives.")
	return nil
}

// renderHarvest writes the human or JSON candidate listing.
func renderHarvest(w io.Writer, dbPath string, candidates []harvest.Candidate, asJSON bool) error {
	if asJSON {
		encoded, err := json.MarshalIndent(struct {
			Database   string              `json:"database"`
			Candidates []harvest.Candidate `json:"candidates"`
		}{Database: dbPath, Candidates: candidates}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(encoded))
		return err
	}

	if len(candidates) == 0 {
		_, err := fmt.Fprintf(w, "No candidates in %s.\n\nA candidate needs a user turn of real "+
			"work followed by at least one commit in that repository. Sessions with no commits, "+
			"or whose turns were all acknowledgements, produce none.\n", dbPath)
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tREPO\tCOMMITS\tFILES\t+/-\tTESTS\tPROMPT")
	for _, c := range candidates {
		fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t+%d/-%d\t%s\t%s\n",
			c.AskedAt.Format("2006-01-02 15:04"), shortRepo(c.Repo), len(c.Commits),
			c.FilesChanged, c.LinesAdded, c.LinesRemoved, yesNo(c.TestsChanged),
			excerpt(c.Prompt, promptExcerpt))
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	repos := map[string]bool{}
	tests := 0
	for _, c := range candidates {
		repos[c.Repo] = true
		if c.TestsChanged {
			tests++
		}
	}
	fmt.Fprintf(w, "\n%d candidate(s) from %d repository(ies); %d changed a test file.\n",
		len(candidates), len(repos), tests)
	fmt.Fprintf(w, "Read from %s (read-only).\n", dbPath)
	fmt.Fprintln(w, "Nothing was written: a harvested task is built from private code, so turning "+
		"a candidate into a task is your decision.")
	return nil
}

// shortRepo names a repository by its last path element.
func shortRepo(repo string) string {
	if i := strings.LastIndex(strings.TrimRight(repo, "/"), "/"); i >= 0 {
		return strings.TrimRight(repo, "/")[i+1:]
	}
	return repo
}

// excerpt flattens a prompt onto one line and truncates it.
func excerpt(text string, limit int) string {
	flat := strings.Join(strings.Fields(text), " ")
	runes := []rune(flat)
	if len(runes) <= limit {
		return flat
	}
	return string(runes[:limit]) + "…"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
