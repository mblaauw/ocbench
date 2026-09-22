package canon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactNestedSecrets(t *testing.T) {
	raw := `{
	  "mcp": {"gitlab": {"type": "remote", "url": "https://x/mcp",
	          "headers": {"Authorization": "Bearer abc", "X-Api-Key": "k"},
	          "environment": {"GITLAB_TOKEN": "glpat-xxx", "SAFE_VAR": "public"}}},
	  "provider": {"anthropic": {"apiKey": "sk-ant", "options": {"timeout": 30}}},
	  "plugin": ["superpowers@git+https://x"],
	  "tokens": {"input": 5}
	}`
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatal(err)
	}
	out, err := Redact(v)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, leaked := range []string{"Bearer abc", "glpat-xxx", "sk-ant", `"k"`} {
		if strings.Contains(s, leaked) {
			t.Fatalf("secret leaked: %s in %s", leaked, s)
		}
	}
	if !strings.Contains(s, `"SAFE_VAR":"public"`) {
		t.Fatalf("non-secret value was redacted: %s", s)
	}
	if !strings.Contains(s, `"timeout":30`) {
		t.Fatalf("non-secret key redacted: %s", s)
	}
	// "tokens" contains the sensitive substring "token", so spec 5.3 replaces
	// the whole subtree; the runtime metric must not survive redaction.
	if got := out.(map[string]any)["tokens"]; got != RedactedValue {
		t.Fatalf("tokens subtree should be redacted, got %v: %s", got, s)
	}
}

func TestRedactDoesNotMutateInput(t *testing.T) {
	v := map[string]any{"apiKey": "secret"}
	if _, err := Redact(v); err != nil {
		t.Fatal(err)
	}
	if v["apiKey"] != "secret" {
		t.Fatal("input mutated")
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	v := map[string]any{"a": map[string]any{"password": "p", "keep": 1}}
	once, err := Redact(v)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := Redact(once)
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(once)
	b2, _ := json.Marshal(twice)
	if string(b1) != string(b2) {
		t.Fatalf("not idempotent: %s vs %s", b1, b2)
	}
}
