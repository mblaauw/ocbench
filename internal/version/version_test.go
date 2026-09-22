package version

import "testing"

func TestInfoDefaults(t *testing.T) {
	info := Info()
	if info.Version == "" {
		t.Fatal("Version must never be empty")
	}
	if info.Commit == "" {
		t.Fatal("Commit must never be empty")
	}
	if info.Date == "" {
		t.Fatal("Date must never be empty")
	}
}

func TestStringContainsVersion(t *testing.T) {
	Version = "1.2.3"
	Commit = "abc1234"
	if got := String(); got != "ocbench 1.2.3 (abc1234)" {
		t.Fatalf("String() = %q", got)
	}
}
