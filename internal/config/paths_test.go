package config

import (
	"path/filepath"
	"testing"
)

func fakeEnv(vals map[string]string) func(string) string {
	return func(k string) string { return vals[k] }
}

func TestResolveDefaultsToXDG(t *testing.T) {
	env := fakeEnv(map[string]string{
		"HOME":            "/home/u",
		"XDG_CONFIG_HOME": "/home/u/.config",
		"XDG_DATA_HOME":   "/home/u/.local/share",
		"XDG_CACHE_HOME":  "/home/u/.cache",
	})
	p := Resolve(env)
	if p.Home != "/home/u/.local/share/ocbench" {
		t.Fatalf("Home = %q", p.Home)
	}
	if p.ConfigFile != "/home/u/.config/ocbench/config.yaml" {
		t.Fatalf("ConfigFile = %q", p.ConfigFile)
	}
	if p.DB != filepath.Join(p.Home, "ocbench.db") {
		t.Fatalf("DB = %q", p.DB)
	}
	if p.Profiles != filepath.Join(p.Home, "profiles") {
		t.Fatalf("Profiles = %q", p.Profiles)
	}
	if p.Runs != filepath.Join(p.Home, "runs") {
		t.Fatalf("Runs = %q", p.Runs)
	}
	if p.Suites != filepath.Join(p.Home, "suites") {
		t.Fatalf("Suites = %q", p.Suites)
	}
	if p.Cache != "/home/u/.cache/ocbench" {
		t.Fatalf("Cache = %q", p.Cache)
	}
}

func TestResolveHonoursOCBenchHome(t *testing.T) {
	env := fakeEnv(map[string]string{
		"HOME":         "/home/u",
		"OCBENCH_HOME": "/workspace/.ocbench",
	})
	p := Resolve(env)
	if p.Home != "/workspace/.ocbench" {
		t.Fatalf("Home = %q", p.Home)
	}
	if p.DB != "/workspace/.ocbench/ocbench.db" {
		t.Fatalf("DB = %q", p.DB)
	}
	if p.ConfigFile != "/home/u/.config/ocbench/config.yaml" {
		t.Fatalf("ConfigFile must not move with OCBENCH_HOME, got %q", p.ConfigFile)
	}
}

func TestResolveFallsBackWhenXDGUnset(t *testing.T) {
	p := Resolve(fakeEnv(map[string]string{"HOME": "/home/u"}))
	if p.Home != "/home/u/.local/share/ocbench" {
		t.Fatalf("Home = %q", p.Home)
	}
	if p.Cache != "/home/u/.cache/ocbench" {
		t.Fatalf("Cache = %q", p.Cache)
	}
}
