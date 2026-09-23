package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"mbl/ocbench/internal/store"
)

// runCompareCmd executes the compare command with injected deps.
func runCompareCmd(t *testing.T, d Deps, args ...string) (string, error) {
	t.Helper()
	cmd := newCompareCmd(d)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return out.String(), err
}

// compareFixture seeds a compatible before/after pair. buildBefore/buildAfter
// vary only the agent/build component hash so the profile diff is controllable.
func compareFixture(t *testing.T) (Deps, *store.Store) {
	t.Helper()
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build-1", CanonicalJSON: `{"mode":"primary"}`},
	})
	seedHistoryProfile(t, st, "p2", "hash-2", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build-2", CanonicalJSON: `{"mode":"primary"}`},
	})
	seedHistoryRun(t, st, historyBaseRun("run-before", "2026-01-01T00:00:00Z", cliTaskID, "p1", "hash-1"))
	seedHistoryRun(t, st, historyBaseRun("run-after", "2026-01-02T00:00:00Z", cliTaskID, "p2", "hash-2"))
	return d, st
}

func TestCompareLatestPrevious(t *testing.T) {
	d, _ := compareFixture(t)
	out, err := runCompareCmd(t, d, "latest", "previous")
	if err != nil {
		t.Fatalf("compare latest previous: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run-after") || !strings.Contains(out, "run-before") {
		t.Fatalf("compare output missing run ids:\n%s", out)
	}
	if !strings.Contains(out, "profile changes") || !strings.Contains(out, "agent/build") {
		t.Fatalf("compare output missing profile changes:\n%s", out)
	}
}

func TestCompareFullUUID(t *testing.T) {
	d, _ := compareFixture(t)
	out, err := runCompareCmd(t, d, "run-before", "run-after")
	if err != nil {
		t.Fatalf("compare full uuid: %v\n%s", err, out)
	}
	if !strings.Contains(out, "run-before") || !strings.Contains(out, "run-after") {
		t.Fatalf("compare full uuid output:\n%s", out)
	}
}

func TestCompareMissingSelectorIsUsageError(t *testing.T) {
	d, _ := compareFixture(t)
	out, err := runCompareCmd(t, d, "no-such-run", "latest")
	if err == nil {
		t.Fatalf("compare missing selector: expected error\n%s", out)
	}
	if !isUsageError(err) {
		t.Fatalf("compare missing selector error = %v, want UsageError", err)
	}
}

func TestCompareEmptyStoreIsUsageError(t *testing.T) {
	d, _ := historyTestDeps(t)
	out, err := runCompareCmd(t, d, "latest", "previous")
	if err == nil {
		t.Fatalf("compare empty store: expected error\n%s", out)
	}
	if !isUsageError(err) {
		t.Fatalf("compare empty store error = %v, want UsageError", err)
	}
}

func TestCompareIncompatibleRunsIsUsageError(t *testing.T) {
	d, st := compareFixture(t)
	other := historyBaseRun("run-other", "2026-01-03T00:00:00Z", cliTaskID, "p1", "hash-1")
	other.FixtureSHA = "different-fixture"
	seedHistoryRun(t, st, other)

	out, err := runCompareCmd(t, d, "run-before", "run-other")
	if err == nil {
		t.Fatalf("compare incompatible: expected error\n%s", out)
	}
	if !isUsageError(err) {
		t.Fatalf("compare incompatible error = %v, want UsageError", err)
	}
}

func TestCompareWrongArgCountIsUsageError(t *testing.T) {
	d, _ := compareFixture(t)
	out, err := runCompareCmd(t, d, "latest")
	if err == nil {
		t.Fatalf("compare one arg: expected error\n%s", out)
	}
	if !isUsageError(err) {
		t.Fatalf("compare one arg error = %v, want UsageError", err)
	}
}

func TestCompareZeroBaselineDisplay(t *testing.T) {
	d, st := compareFixture(t)
	ctx := context.Background()
	if err := st.InsertRunMetrics(ctx, "run-before", map[string]float64{"only_after": 0}); err != nil {
		t.Fatalf("insert before metrics: %v", err)
	}
	if err := st.InsertRunMetrics(ctx, "run-after", map[string]float64{"only_after": 3}); err != nil {
		t.Fatalf("insert after metrics: %v", err)
	}

	out, err := runCompareCmd(t, d, "run-before", "run-after")
	if err != nil {
		t.Fatalf("compare zero baseline: %v\n%s", err, out)
	}
	if !strings.Contains(out, "only_after") {
		t.Fatalf("compare zero baseline missing metric:\n%s", out)
	}
	if !strings.Contains(out, "n/a") {
		t.Fatalf("compare zero baseline must not divide by zero:\n%s", out)
	}
}

func TestCompareMultipleProfileChangesWarnsWithoutCausation(t *testing.T) {
	d, st := historyTestDeps(t)
	seedHistorySuiteTask(t, st)
	seedHistoryProfile(t, st, "p1", "hash-1", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary-1", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build-1", CanonicalJSON: `{"mode":"primary"}`},
	})
	seedHistoryProfile(t, st, "p2", "hash-2", []store.ComponentRow{
		{Kind: "primary", Name: "primary", Hash: "h-primary-2", CanonicalJSON: `{}`},
		{Kind: "agent", Name: "build", Hash: "h-build-2", CanonicalJSON: `{"mode":"primary"}`},
	})
	seedHistoryRun(t, st, historyBaseRun("run-before", "2026-01-01T00:00:00Z", cliTaskID, "p1", "hash-1"))
	seedHistoryRun(t, st, historyBaseRun("run-after", "2026-01-02T00:00:00Z", cliTaskID, "p2", "hash-2"))

	out, err := runCompareCmd(t, d, "run-before", "run-after")
	if err != nil {
		t.Fatalf("compare warning: %v\n%s", err, out)
	}
	if !strings.Contains(out, "2 profile components changed") {
		t.Fatalf("compare warning missing count:\n%s", out)
	}
	lower := strings.ToLower(out)
	for _, bad := range []string{"because", "caused", "due to"} {
		if strings.Contains(lower, bad) {
			t.Fatalf("compare output claims causation (%q):\n%s", bad, out)
		}
	}
}

func TestCompareSingleProfileChangeHasNoWarning(t *testing.T) {
	d, _ := compareFixture(t)
	out, err := runCompareCmd(t, d, "run-before", "run-after")
	if err != nil {
		t.Fatalf("compare single change: %v\n%s", err, out)
	}
	if strings.Contains(out, "profile components changed") {
		t.Fatalf("single profile change must not warn:\n%s", out)
	}
}

func TestCompareValidationStatusChanges(t *testing.T) {
	d, st := compareFixture(t)
	ctx := context.Background()
	if err := st.InsertRunValidations(ctx, "run-before", []store.ValidationRow{
		{Seq: 1, Kind: "test", Name: "unit", Status: "passed"},
	}); err != nil {
		t.Fatalf("insert before validations: %v", err)
	}
	if err := st.InsertRunValidations(ctx, "run-after", []store.ValidationRow{
		{Seq: 1, Kind: "test", Name: "unit", Status: "failed"},
	}); err != nil {
		t.Fatalf("insert after validations: %v", err)
	}

	out, err := runCompareCmd(t, d, "run-before", "run-after")
	if err != nil {
		t.Fatalf("compare validations: %v\n%s", err, out)
	}
	if !strings.Contains(out, "test/unit") || !strings.Contains(out, "passed") || !strings.Contains(out, "failed") {
		t.Fatalf("compare validation change missing:\n%s", out)
	}
}

func TestCompareJSONShape(t *testing.T) {
	d, st := compareFixture(t)
	ctx := context.Background()
	if err := st.InsertRunMetrics(ctx, "run-before", map[string]float64{"a": 4, "zero": 0}); err != nil {
		t.Fatalf("insert before metrics: %v", err)
	}
	if err := st.InsertRunMetrics(ctx, "run-after", map[string]float64{"a": 6, "zero": 3}); err != nil {
		t.Fatalf("insert after metrics: %v", err)
	}
	if err := st.InsertRunValidations(ctx, "run-before", []store.ValidationRow{
		{Seq: 1, Kind: "test", Name: "unit", Status: "passed"},
	}); err != nil {
		t.Fatalf("insert before validations: %v", err)
	}
	if err := st.InsertRunValidations(ctx, "run-after", []store.ValidationRow{
		{Seq: 1, Kind: "test", Name: "unit", Status: "failed"},
	}); err != nil {
		t.Fatalf("insert after validations: %v", err)
	}

	out, err := runCompareCmd(t, d, "run-before", "run-after", "--json")
	if err != nil {
		t.Fatalf("compare --json: %v\n%s", err, out)
	}

	type runJSON struct {
		RunID       string `json:"run_id"`
		ProfileHash string `json:"profile_hash"`
	}
	type metricJSON struct {
		Name    string   `json:"name"`
		Before  float64  `json:"before"`
		After   float64  `json:"after"`
		Delta   float64  `json:"delta"`
		Percent *float64 `json:"percent"`
	}
	type validationJSON struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Before string `json:"before"`
		After  string `json:"after"`
	}
	type changeJSON struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Change string `json:"change"`
	}
	var doc struct {
		Before         runJSON          `json:"before"`
		After          runJSON          `json:"after"`
		Metrics        []metricJSON     `json:"metrics"`
		Validations    []validationJSON `json:"validations"`
		ProfileChanges []changeJSON     `json:"profile_changes"`
		Warning        string           `json:"warning"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("decode compare JSON: %v\n%s", err, out)
	}
	if doc.Before.RunID != "run-before" || doc.After.RunID != "run-after" {
		t.Fatalf("before/after = %+v / %+v", doc.Before, doc.After)
	}
	if doc.Before.ProfileHash != "hash-1" || doc.After.ProfileHash != "hash-2" {
		t.Fatalf("profile hashes = %q / %q", doc.Before.ProfileHash, doc.After.ProfileHash)
	}
	if len(doc.Metrics) != 2 {
		t.Fatalf("metrics = %+v, want 2", doc.Metrics)
	}
	var zero metricJSON
	for _, m := range doc.Metrics {
		if m.Name == "zero" {
			zero = m
		}
	}
	if zero.Percent != nil {
		t.Fatalf("zero.Percent = %v, want null", *zero.Percent)
	}
	if len(doc.Validations) != 1 || doc.Validations[0].Name != "unit" ||
		doc.Validations[0].Before != "passed" || doc.Validations[0].After != "failed" {
		t.Fatalf("validations = %+v", doc.Validations)
	}
	if len(doc.ProfileChanges) != 1 || doc.ProfileChanges[0].Name != "build" {
		t.Fatalf("profile_changes = %+v", doc.ProfileChanges)
	}
	if doc.Warning != "" {
		t.Fatalf("warning = %q, want empty for one change", doc.Warning)
	}
}

func TestCompareJSONEmptyArraysAreNotNull(t *testing.T) {
	d, _ := compareFixture(t)
	out, err := runCompareCmd(t, d, "run-before", "run-after", "--json")
	if err != nil {
		t.Fatalf("compare --json: %v\n%s", err, out)
	}
	for _, key := range []string{`"metrics": []`, `"validations": []`} {
		if !strings.Contains(out, key) {
			t.Fatalf("compare JSON missing empty array %s:\n%s", key, out)
		}
	}
}
