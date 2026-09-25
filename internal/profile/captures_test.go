package profile_test

import (
	"os"
	"path/filepath"
	"testing"

	"mbl/ocbench/internal/profile"
)

// writeCapture writes one capture file into dir.
func writeCapture(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestReadCapturesReadsAgentsSkillsAndInstructions(t *testing.T) {
	dir := t.TempDir()
	writeCapture(t, dir, "agents.json", `[
	  {"name":"build","mode":"primary","model":{"providerID":"opencode-go","modelID":"deepseek-v4.1-flash"},
	   "variant":"high","steps":60,"native":true,"temperature":0.1,"prompt":"You are the build agent.",
	   "tools":{"bash":true,"edit":true},
	   "permission":[{"permission":"task","pattern":"explore","action":"allow"}]},
	  {"name":"architect","mode":"subagent","model":{"providerID":"openai","modelID":"gpt-5.6-sol"},
	   "variant":"high","prompt":"Answer from evidence."}
	]`)
	writeCapture(t, dir, "skills.json", `[
	  {"name":"zeta","description":"second","location":"~/.agents/skills/zeta","content":"# Zeta","content_sha256":"aa","files_sha256":"bb"},
	  {"name":"alpha","description":"first","location":"~/.agents/skills/alpha","content":"# Alpha","content_sha256":"cc","files_sha256":"dd"}
	]`)
	writeCapture(t, dir, "instructions.json", `{"./AGENTS.md":"repo rules","global:AGENTS.md":"global rules"}`)

	set, err := profile.ReadCaptures(dir)
	if err != nil {
		t.Fatalf("ReadCaptures: %v", err)
	}
	if !set.Available {
		t.Fatal("Available = false, want true")
	}
	if len(set.Agents) != 2 || len(set.Skills) != 2 || len(set.Instructions) != 2 {
		t.Fatalf("counts = %d/%d/%d, want 2/2/2", len(set.Agents), len(set.Skills), len(set.Instructions))
	}

	// Agents, skills and instructions all render in a stable order.
	if set.Agents[0].Name != "architect" || set.Agents[1].Name != "build" {
		t.Errorf("agents not sorted: %s, %s", set.Agents[0].Name, set.Agents[1].Name)
	}
	if set.Skills[0].Name != "alpha" {
		t.Errorf("skills not sorted: %s first", set.Skills[0].Name)
	}
	if set.Instructions[0].Scope != "./AGENTS.md" {
		t.Errorf("instructions not sorted: %s first", set.Instructions[0].Scope)
	}

	build := set.Agents[1]
	if build.Model() != "opencode-go/deepseek-v4.1-flash" {
		t.Errorf("Model() = %q", build.Model())
	}
	if build.Prompt != "You are the build agent." {
		t.Errorf("prompt not captured: %q", build.Prompt)
	}
	if build.Temperature == nil || *build.Temperature != 0.1 {
		t.Errorf("temperature = %v, want 0.1", build.Temperature)
	}
	if !build.Native || build.Steps != 60 {
		t.Errorf("native/steps = %v/%d", build.Native, build.Steps)
	}
	if len(build.Permissions) != 1 || build.Permissions[0].Pattern != "explore" {
		t.Errorf("permissions = %+v", build.Permissions)
	}
	if got := set.Instructions[0].Text; got != "repo rules" {
		t.Errorf("instruction text = %q", got)
	}
	if set.Skills[0].Content != "# Alpha" {
		t.Errorf("skill content = %q", set.Skills[0].Content)
	}
}

func TestReadCapturesMissingDirectoryIsUnavailableNotAnError(t *testing.T) {
	set, err := profile.ReadCaptures(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatalf("ReadCaptures: %v", err)
	}
	if set.Available || len(set.Agents) != 0 {
		t.Errorf("set = %+v, want unavailable and empty", set)
	}
	if set, err := profile.ReadCaptures(""); err != nil || set.Available {
		t.Errorf("empty dir = %+v, %v", set, err)
	}
}

// A capture file that exists but is malformed must not be silently treated as
// an absent one: that would show an empty prompt for a configured agent.
func TestReadCapturesMalformedFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	writeCapture(t, dir, "agents.json", `{"not":"a list"}`)
	if _, err := profile.ReadCaptures(dir); err == nil {
		t.Fatal("ReadCaptures = nil error, want a parse error")
	}
}
