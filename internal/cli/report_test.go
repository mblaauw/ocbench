package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mbl/ocbench/internal/store"
)

func runReportCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newReportCmd(d)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return buf.String(), err
}

func seedReportExperiment(t *testing.T, d Deps) {
	t.Helper()
	st := openExperimentStore(t, d)
	ctx := context.Background()
	for _, profile := range []store.ProfileRow{
		{ID: "p-base", ProfileHash: "profile-base", OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", CanonicalJSON: `{}`, CreatedAt: "2026-03-01T00:00:00Z"},
		{ID: "p-lean", ProfileHash: "profile-lean", OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", CanonicalJSON: `{}`, CreatedAt: "2026-03-01T00:00:00Z"},
	} {
		if err := st.InsertProfile(ctx, profile, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.InsertExperiment(ctx, store.ExperimentRow{
		ID: "exp-report", Name: "report <test>", SpecJSON: `{"suite":"core","repeat":3}`, CreatedAt: "2026-03-01T00:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, arm := range []store.ExperimentArmRow{
		{ID: "arm-base", ExperimentID: "exp-report", Label: "base", ProfileID: ptr("p-base"), ProfileHash: "profile-base", OverlayKind: "none", CreatedAt: "2026-03-01T00:00:00Z"},
		{ID: "arm-lean", ExperimentID: "exp-report", Label: "lean", ProfileID: ptr("p-lean"), ProfileHash: "profile-lean", OverlayKind: "none", CreatedAt: "2026-03-01T00:00:00Z"},
	} {
		if err := st.InsertExperimentArm(ctx, arm); err != nil {
			t.Fatal(err)
		}
	}
	for _, arm := range []struct {
		id, profileID, hash string
		cost, tokens        float64
	}{
		{"arm-base", "p-base", "profile-base", 2, 200},
		{"arm-lean", "p-lean", "profile-lean", 1, 100},
	} {
		for repeat := 0; repeat < 3; repeat++ {
			armID := arm.id
			runID := fmt.Sprintf("%s-%d", arm.id, repeat)
			if err := st.InsertRun(ctx, store.RunRow{
				ID: runID, ExperimentID: "exp-report", ArmID: &armID, RepeatIndex: repeat,
				ProfileID: arm.profileID, ProfileHash: arm.hash,
				SuiteName: "core", SuiteVersion: "1", SuiteHash: "suite-h", TaskID: "task", TaskVersion: "1", FixtureSHA: "fixture-h",
				OpenCodeVersion: "1.18.32", OCBenchVersion: "dev", RunnerEnv: "linux/amd64 · 4 CPU",
				Status: "passed", StartedAt: fmt.Sprintf("2026-03-01T00:00:0%dZ", repeat), ArtifactsDir: "/private/run",
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.InsertRunMetrics(ctx, runID, map[string]float64{"success": 1, "cost": arm.cost, "tokens_total": arm.tokens}); err != nil {
				t.Fatal(err)
			}
		}
	}
	st.Close()
}

func ptr(value string) *string { return &value }

func TestReportRendersPortableCohortFormats(t *testing.T) {
	d := cliTestDeps(t)
	seedReportExperiment(t, d)

	markdown, err := runReportCmd(t, d, "exp-report", "--format", "md")
	if err != nil {
		t.Fatalf("markdown report: %v\n%s", err, markdown)
	}
	for _, want := range []string{"# ocbench efficiency cohort", "eligible:", "lean", "linux/amd64 · 4 CPU"} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("markdown missing %q:\n%s", want, markdown)
		}
	}

	html, err := runReportCmd(t, d, "exp-report", "--format", "html")
	if err != nil {
		t.Fatalf("html report: %v\n%s", err, html)
	}
	if !strings.Contains(html, "<!doctype html>") || !strings.Contains(html, "report &lt;test&gt;") {
		t.Fatalf("html report is not standalone and escaped:\n%s", html)
	}
}

func TestReportRejectsBadArguments(t *testing.T) {
	d := cliTestDeps(t)
	for _, args := range [][]string{{}, {"missing"}, {"x", "--format", "pdf"}} {
		_, err := runReportCmd(t, d, args...)
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Fatalf("%v err = %v, want *UsageError", args, err)
		}
	}
}

// The exchange path: one user exports a cohort summary as json, another imports
// it and renders the same standing and architecture differences — without any
// shared database.
func TestReportExportsAndImportsAPortableSummary(t *testing.T) {
	d := cliTestDeps(t)
	seedReportExperiment(t, d)

	exported, err := runReportCmd(t, d, "exp-report", "--format", "json")
	if err != nil {
		t.Fatalf("export: %v\n%s", err, exported)
	}
	for _, want := range []string{`"schema_version"`, "profile-base", "profile-lean"} {
		if !strings.Contains(exported, want) {
			t.Errorf("exported summary missing %q:\n%s", want, exported)
		}
	}

	// A second user's store, which has never seen this experiment.
	other := cliTestDeps(t)
	path := filepath.Join(t.TempDir(), "shared-cohort.json")
	if err := os.WriteFile(path, []byte(exported), 0o600); err != nil {
		t.Fatal(err)
	}
	markdown, err := runReportCmd(t, other, "--import", path)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, markdown)
	}
	if !strings.Contains(markdown, "profile-base") || !strings.Contains(markdown, "profile-lean") {
		t.Errorf("imported rendering is missing the arms:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Runner environments") {
		t.Errorf("imported rendering is missing the evidence header:\n%s", markdown)
	}

	if _, err := runReportCmd(t, other, "--import", filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Error("importing a missing file must fail")
	}
}
