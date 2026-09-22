package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/opencode"
	"mbl/ocbench/internal/store"
)

// snapshotFakeAdapter is a mutable Adapter for snapshot command tests. Injecting
// it through Deps keeps the tests away from the real opencode binary; mutating
// its skills between runs simulates a configuration change.
type snapshotFakeAdapter struct {
	version    string
	config     []byte
	skills     []opencode.SkillInfo
	versionErr error
}

func (f *snapshotFakeAdapter) Version(context.Context) (string, error) {
	if f.versionErr != nil {
		return "", f.versionErr
	}
	return f.version, nil
}

func (f *snapshotFakeAdapter) ResolvedConfig(context.Context, string) ([]byte, error) {
	return f.config, nil
}

func (f *snapshotFakeAdapter) Skills(context.Context, string) ([]opencode.SkillInfo, error) {
	return append([]opencode.SkillInfo(nil), f.skills...), nil
}

func (f *snapshotFakeAdapter) Agent(_ context.Context, _, name string) (opencode.AgentInfo, error) {
	return opencode.AgentInfo{Name: name, Mode: "primary"}, nil
}

func (f *snapshotFakeAdapter) MCPStatus(context.Context, string) ([]opencode.MCPStatus, error) {
	return nil, nil
}

// Start and Export satisfy the opencode.Adapter interface; snapshot tests never
// stream a run or export a session.
func (f *snapshotFakeAdapter) Start(context.Context, opencode.RunRequest) (*opencode.Session, error) {
	return nil, errors.New("not implemented")
}

func (f *snapshotFakeAdapter) Export(context.Context, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func newSnapshotFake(t *testing.T) *snapshotFakeAdapter {
	t.Helper()
	skillDir := t.TempDir()
	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("# ruff\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &snapshotFakeAdapter{
		version: "1.18.32",
		config:  []byte(`{"default_agent":"build","agent":{"build":{}},"mcp":{"gitlab":{"enabled":true}}}`),
		skills: []opencode.SkillInfo{
			{Name: "ruff", Description: "lint", Location: skillFile, Content: "# ruff\n"},
		},
	}
}

// runSnapshotCmd runs the snapshot command with injected deps and returns its
// combined output.
func runSnapshotCmd(t *testing.T, d Deps, args ...string) string {
	t.Helper()
	cmd := newSnapshotCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("snapshot %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func countQuery(t *testing.T, dbPath, query string) int {
	t.Helper()
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err := st.DB().QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSnapshotDedupesIdenticalProfile(t *testing.T) {
	d := cliTestDeps(t)
	d.Adapter = newSnapshotFake(t)
	work := t.TempDir()

	first := runSnapshotCmd(t, d, "--dir", work)
	if !strings.Contains(first, "(new)") {
		t.Fatalf("first snapshot missing (new):\n%s", first)
	}
	if !strings.Contains(first, "opencode 1.18.32") {
		t.Fatalf("first snapshot missing version:\n%s", first)
	}
	if !strings.Contains(first, "agent/build") || !strings.Contains(first, "skill/ruff") {
		t.Fatalf("first snapshot missing components:\n%s", first)
	}
	if strings.Contains(first, "changes vs") {
		t.Fatalf("first snapshot unexpectedly showed a diff:\n%s", first)
	}

	second := runSnapshotCmd(t, d, "--dir", work)
	if !strings.Contains(second, "(existing)") {
		t.Fatalf("second snapshot missing (existing):\n%s", second)
	}
	if !strings.Contains(second, "no changes") {
		t.Fatalf("second snapshot missing no changes:\n%s", second)
	}

	if n := countQuery(t, d.Paths.DB, "SELECT COUNT(*) FROM profiles"); n != 1 {
		t.Fatalf("profiles = %d, want 1", n)
	}
	if n := countQuery(t, d.Paths.DB, "SELECT COUNT(*) FROM profile_changes"); n != 0 {
		t.Fatalf("profile_changes = %d, want 0", n)
	}
}

func TestSnapshotReportsAndRecordsOneChange(t *testing.T) {
	d := cliTestDeps(t)
	fake := newSnapshotFake(t)
	d.Adapter = fake
	work := t.TempDir()

	runSnapshotCmd(t, d, "--dir", work)

	fake.skills[0].Content += "\nchanged\n"

	out := runSnapshotCmd(t, d, "--dir", work)
	if !strings.Contains(out, "(new)") {
		t.Fatalf("changed snapshot missing (new):\n%s", out)
	}
	if !strings.Contains(out, "changes vs") {
		t.Fatalf("changed snapshot missing changes header:\n%s", out)
	}
	if !strings.Contains(out, "skill/ruff  changed  ") {
		t.Fatalf("changed snapshot missing change line:\n%s", out)
	}

	changeLines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  ") {
			changeLines++
		}
	}
	if changeLines != 1 {
		t.Fatalf("change lines = %d, want 1:\n%s", changeLines, out)
	}

	if n := countQuery(t, d.Paths.DB, "SELECT COUNT(*) FROM profiles"); n != 2 {
		t.Fatalf("profiles = %d, want 2", n)
	}
	if n := countQuery(t, d.Paths.DB, "SELECT COUNT(*) FROM profile_changes"); n != 1 {
		t.Fatalf("profile_changes = %d, want 1", n)
	}
}

func TestSnapshotJSONShape(t *testing.T) {
	d := cliTestDeps(t)
	fake := newSnapshotFake(t)
	d.Adapter = fake
	work := t.TempDir()

	type jsonChange struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Change   string `json:"change"`
		FromHash string `json:"from_hash"`
		ToHash   string `json:"to_hash"`
	}
	type jsonComponent struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Hash string `json:"hash"`
	}
	type jsonProfile struct {
		ID              string `json:"id"`
		Hash            string `json:"hash"`
		OpenCodeVersion string `json:"opencode_version"`
		Created         bool   `json:"created"`
	}
	type jsonReport struct {
		Profile    jsonProfile     `json:"profile"`
		Components []jsonComponent `json:"components"`
		Changes    []jsonChange    `json:"changes"`
	}

	decode := func(out string) jsonReport {
		t.Helper()
		var got jsonReport
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("decode JSON: %v\n%s", err, out)
		}
		return got
	}

	first := decode(runSnapshotCmd(t, d, "--dir", work, "--json"))
	if first.Profile.ID == "" || first.Profile.Hash == "" {
		t.Fatalf("profile identity missing: %+v", first.Profile)
	}
	if first.Profile.OpenCodeVersion != "1.18.32" || !first.Profile.Created {
		t.Fatalf("profile = %+v", first.Profile)
	}
	if len(first.Components) == 0 {
		t.Fatal("components missing")
	}
	for _, c := range first.Components {
		if c.Kind == "" || c.Name == "" || len(c.Hash) != 64 {
			t.Fatalf("component = %+v, want kind/name and full hash", c)
		}
	}
	if first.Changes != nil {
		t.Fatalf("first snapshot changes = %+v, want omitted", first.Changes)
	}

	fake.skills[0].Content += "\nchanged\n"
	second := decode(runSnapshotCmd(t, d, "--dir", work, "--json"))
	if !second.Profile.Created {
		t.Fatalf("changed snapshot created = false, want a new profile")
	}
	if len(second.Changes) != 1 {
		t.Fatalf("changes = %+v, want exactly one", second.Changes)
	}
	ch := second.Changes[0]
	if ch.Kind != "skill" || ch.Name != "ruff" || ch.Change != "changed" {
		t.Fatalf("change = %+v", ch)
	}
	if len(ch.FromHash) != 64 || len(ch.ToHash) != 64 || ch.FromHash == ch.ToHash {
		t.Fatalf("change hashes = %q -> %q", ch.FromHash, ch.ToHash)
	}
}

func TestSnapshotAdapterFailureReturnsError(t *testing.T) {
	d := cliTestDeps(t)
	fake := newSnapshotFake(t)
	fake.versionErr = errors.New("adapter boom")
	d.Adapter = fake

	cmd := newSnapshotCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--dir", t.TempDir()})
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatalf("snapshot with failing adapter succeeded:\n%s", out.String())
	}
}
