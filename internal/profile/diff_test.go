package profile

import (
	"testing"
)

func mkProfile(comps ...Component) *Profile {
	return &Profile{Components: comps}
}

func TestDiffChanged(t *testing.T) {
	from := mkProfile(
		Component{Kind: "skill", Name: "ruff", Hash: "a"},
		Component{Kind: "agent", Name: "build", Hash: "b"},
	)
	to := mkProfile(
		Component{Kind: "skill", Name: "ruff", Hash: "c"},
		Component{Kind: "agent", Name: "build", Hash: "b"},
	)
	got := Diff(from, to)
	if len(got) != 1 {
		t.Fatalf("changes = %+v, want 1", got)
	}
	if got[0] != (Change{Kind: "skill", Name: "ruff", Change: ChangeChanged, FromHash: "a", ToHash: "c"}) {
		t.Fatalf("change = %+v", got[0])
	}
}

func TestDiffAddedRemovedAndSorted(t *testing.T) {
	from := mkProfile(
		Component{Kind: "skill", Name: "ruff", Hash: "a"},
		Component{Kind: "agent", Name: "build", Hash: "b"},
		Component{Kind: "agent", Name: "plan", Hash: "p"},
	)
	to := mkProfile(
		Component{Kind: "skill", Name: "ruff", Hash: "a"},
		Component{Kind: "skill", Name: "docs", Hash: "d"},
		Component{Kind: "agent", Name: "build", Hash: "b"},
	)
	got := Diff(from, to)
	want := []Change{
		{Kind: "agent", Name: "plan", Change: ChangeRemoved, FromHash: "p"},
		{Kind: "skill", Name: "docs", Change: ChangeAdded, ToHash: "d"},
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("change[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestDiffIdenticalIsEmpty(t *testing.T) {
	p := mkProfile(Component{Kind: "primary", Name: "primary", Hash: "h"})
	if got := Diff(p, p); len(got) != 0 {
		t.Fatalf("Diff(p,p) = %+v, want empty", got)
	}
	if got := Diff(nil, nil); len(got) != 0 {
		t.Fatalf("Diff(nil,nil) = %+v, want empty", got)
	}
}

func TestDiffFingerprintSkillChange(t *testing.T) {
	s := loadTestSources(t)
	before, err := Fingerprint(s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	changed := loadTestSources(t)
	for i := range changed.Skills {
		if changed.Skills[i].Name == "ruff" {
			changed.Skills[i].Content += "\nchanged\n"
		}
	}
	after, err := Fingerprint(changed, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := Diff(before, after)
	if len(got) != 1 {
		t.Fatalf("changes = %+v, want exactly one", got)
	}
	if got[0].Kind != "skill" || got[0].Name != "ruff" || got[0].Change != ChangeChanged {
		t.Fatalf("change = %+v", got[0])
	}
	if got[0].FromHash == got[0].ToHash {
		t.Fatal("from and to hashes should differ")
	}
}
