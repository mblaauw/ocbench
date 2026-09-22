package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/profile"
	"mbl/ocbench/internal/store"
)

func newSnapshotCmd(d Deps) *cobra.Command {
	var (
		asJSON bool
		opts   profile.Options
	)
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture and persist the resolved OpenCode execution profile",
		Long: "Discover the locally resolved OpenCode execution profile, persist it content-addressed " +
			"(identical profiles are reused), and report the component differences against the previous profile.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := d.resolve()
			if err != nil {
				return err
			}
			// The environment component records the effective sandbox mode
			// (spec 5.1).
			if resolved.Config.Sandbox.InheritEnvironment {
				opts.SandboxMode = "inherit"
			} else {
				opts.SandboxMode = "default"
			}
			dir := opts.Dir
			if dir == "" {
				dir, err = os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve working directory: %w", err)
				}
			}
			opts.Dir = dir

			if err := config.EnsureDirs(resolved.Paths); err != nil {
				return err
			}
			st, err := store.Open(resolved.Paths.DB)
			if err != nil {
				return err
			}
			defer st.Close()
			if _, err := st.Migrate(cmd.Context()); err != nil {
				return err
			}

			sources, err := profile.Discover(cmd.Context(), resolved.Adapter, dir)
			if err != nil {
				return err
			}
			p, err := profile.Fingerprint(sources, opts)
			if err != nil {
				return err
			}

			// Resolve the comparison profile while the snapshot is not yet in
			// the store, so "latest" means the last profile before this run.
			previous, err := comparisonProfile(cmd.Context(), st, p.Hash)
			if err != nil {
				return err
			}
			created, err := profile.Persist(cmd.Context(), st, resolved.Paths, p)
			if err != nil {
				return err
			}

			var changes []profile.Change
			if previous != nil {
				changes = profile.Diff(previous, p)
			}
			// Only a newly created profile gets change rows; a repeat snapshot
			// would otherwise duplicate the same rows against the same target.
			if created && previous != nil {
				if err := st.InsertProfileChanges(cmd.Context(), previous.ID, p.ID, changeRows(changes)); err != nil {
					return err
				}
			}
			return renderSnapshot(cmd.OutOrStdout(), p, created, previous, changes, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "output JSON")
	cmd.Flags().StringVar(&opts.Dir, "dir", "", "directory to resolve the profile against (default: working directory)")
	cmd.Flags().StringVar(&opts.Agent, "agent", "", "agent override")
	cmd.Flags().StringVar(&opts.Model, "model", "", "model override")
	cmd.Flags().StringVar(&opts.Variant, "variant", "", "variant override")
	return cmd
}

// comparisonProfile returns the most recent persisted profile to diff the
// current snapshot against, before the snapshot itself is stored. When the
// latest profile differs from currentHash it is the comparison. When the
// snapshot is a repeat of the latest, the most recent *different* profile is
// used so a repeat still shows what changed historically; if no different
// profile exists the identical latest is returned, which renders "no changes".
// On the very first snapshot there is nothing to compare against and nil is
// returned.
func comparisonProfile(ctx context.Context, st *store.Store, currentHash string) (*profile.Profile, error) {
	row, comps, err := st.LatestProfile(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.ProfileHash == currentHash {
		different, differentComps, err := st.PreviousProfile(ctx, currentHash)
		switch {
		case err == nil:
			row, comps = different, differentComps
		case errors.Is(err, sql.ErrNoRows):
			// Only this profile exists in the store; it is identical to the
			// snapshot being taken, so report "no changes".
		default:
			return nil, err
		}
	}
	return profile.FromRows(row, comps)
}

// changeRows maps profile changes onto the store's insert format.
func changeRows(changes []profile.Change) []store.ChangeRow {
	rows := make([]store.ChangeRow, 0, len(changes))
	for _, c := range changes {
		rows = append(rows, store.ChangeRow{
			ComponentKind: c.Kind,
			ComponentName: c.Name,
			Change:        c.Change,
			FromHash:      c.FromHash,
			ToHash:        c.ToHash,
		})
	}
	return rows
}

// renderSnapshot writes the human or JSON snapshot report.
func renderSnapshot(w io.Writer, p *profile.Profile, created bool, previous *profile.Profile, changes []profile.Change, asJSON bool) error {
	if asJSON {
		return renderSnapshotJSON(w, p, created, changes)
	}
	state := "existing"
	if created {
		state = "new"
	}
	if _, err := fmt.Fprintf(w, "profile %s  (%s)\n", shortHash(p.Hash), state); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "opencode %s\n\n", p.OpenCodeVersion); err != nil {
		return err
	}
	if _, err := fmt.Fprint(w, profile.RenderSummary(p)); err != nil {
		return err
	}
	if previous == nil {
		// First profile: there is nothing to compare against.
		return nil
	}
	if _, err := fmt.Fprintf(w, "\nchanges vs %s\n", shortHash(previous.Hash)); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, profile.RenderChanges(changes))
	return err
}

type snapshotJSON struct {
	Profile    snapshotProfile     `json:"profile"`
	Components []snapshotComponent `json:"components"`
	Changes    []snapshotChange    `json:"changes,omitempty"`
}

type snapshotProfile struct {
	ID              string `json:"id"`
	Hash            string `json:"hash"`
	OpenCodeVersion string `json:"opencode_version"`
	Created         bool   `json:"created"`
}

type snapshotComponent struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type snapshotChange struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Change   string `json:"change"`
	FromHash string `json:"from_hash"`
	ToHash   string `json:"to_hash"`
}

// renderSnapshotJSON prints the stable machine-readable report. Full hashes are
// emitted for tooling; "changes" is omitted entirely when there is no diff.
func renderSnapshotJSON(w io.Writer, p *profile.Profile, created bool, changes []profile.Change) error {
	out := snapshotJSON{
		Profile: snapshotProfile{
			ID:              p.ID,
			Hash:            p.Hash,
			OpenCodeVersion: p.OpenCodeVersion,
			Created:         created,
		},
	}
	for _, c := range p.Components {
		out.Components = append(out.Components, snapshotComponent{Kind: c.Kind, Name: c.Name, Hash: c.Hash})
	}
	for _, c := range changes {
		out.Changes = append(out.Changes, snapshotChange{
			Kind:     c.Kind,
			Name:     c.Name,
			Change:   c.Change,
			FromHash: c.FromHash,
			ToHash:   c.ToHash,
		})
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// shortHash returns the 8-character display prefix of a profile hash.
func shortHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
