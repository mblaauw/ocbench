package profile

import "testing"

func TestRenderSummaryGolden(t *testing.T) {
	p := &Profile{
		Hash:            "9a814d91aaaaaaaa",
		OpenCodeVersion: "1.18.32",
		Components: []Component{
			{Kind: "agent", Name: "build", Hash: "c788e3f1bbbbbbbb"},
			{Kind: "skill", Name: "pydantic", Hash: "fae71b2acccccccc"},
		},
	}
	want := "agent/build     c788e3f1\n" +
		"skill/pydantic  fae71b2a\n"
	if got := RenderSummary(p); got != want {
		t.Fatalf("RenderSummary():\ngot  %q\nwant %q", got, want)
	}
}

func TestRenderSummarySingletonKeyAndEmpty(t *testing.T) {
	p := &Profile{
		Components: []Component{
			{Kind: "primary", Name: "primary", Hash: "1234567890abcdef"},
		},
	}
	if got, want := RenderSummary(p), "primary  12345678\n"; got != want {
		t.Fatalf("RenderSummary() = %q, want %q", got, want)
	}
	if got := RenderSummary(nil); got != "" {
		t.Fatalf("RenderSummary(nil) = %q, want %q", got, "")
	}
	if got := RenderSummary(&Profile{}); got != "" {
		t.Fatalf("RenderSummary(empty) = %q, want %q", got, "")
	}
}

func TestRenderChangesGolden(t *testing.T) {
	changes := []Change{
		{Kind: "skill", Name: "kubernetes-debugging", Change: ChangeChanged,
			FromHash: "2e11f4a1deadbeef", ToHash: "671ab0c2cafebabe"},
		{Kind: "mcp", Name: "kubernetes", Change: ChangeAdded, ToHash: "a827ab19ffffffff"},
		{Kind: "agent", Name: "plan", Change: ChangeRemoved, FromHash: "b0b0b0b0ffffffff"},
	}
	want := "  skill/kubernetes-debugging  changed  2e11f4a -> 671ab0c\n" +
		"  mcp/kubernetes  added\n" +
		"  agent/plan  removed\n"
	if got := RenderChanges(changes); got != want {
		t.Fatalf("RenderChanges():\ngot  %q\nwant %q", got, want)
	}
}

func TestRenderChangesNoChanges(t *testing.T) {
	if got, want := RenderChanges(nil), "no changes\n"; got != want {
		t.Fatalf("RenderChanges(nil) = %q, want %q", got, want)
	}
	if got, want := RenderChanges([]Change{}), "no changes\n"; got != want {
		t.Fatalf("RenderChanges(empty) = %q, want %q", got, want)
	}
}
