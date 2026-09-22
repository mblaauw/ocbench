package cli

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestDepsResolveDefaultsSuiteFS(t *testing.T) {
	d := cliTestDeps(t)
	got, err := d.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.SuiteFS == nil {
		t.Fatal("SuiteFS was not defaulted")
	}
	if _, err := fs.ReadFile(got.SuiteFS, "core/suite.yaml"); err != nil {
		t.Fatalf("default SuiteFS does not open core/suite.yaml: %v", err)
	}
}

func TestDepsResolveKeepsInjectedSuiteFS(t *testing.T) {
	d := cliTestDeps(t)
	d.SuiteFS = fstest.MapFS{"custom/suite.yaml": &fstest.MapFile{Data: []byte("name: custom\nversion: \"1\"\n")}}
	got, err := d.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := fs.ReadFile(got.SuiteFS, "custom/suite.yaml"); err != nil {
		t.Fatalf("injected SuiteFS was replaced: %v", err)
	}
}
