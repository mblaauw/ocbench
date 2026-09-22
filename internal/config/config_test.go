package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(Paths{ConfigFile: filepath.Join(t.TempDir(), "nope.yaml")})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, DefaultsConfig()) {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.OpenCodeBin != "opencode" || cfg.Defaults.TimeoutSeconds != 900 || cfg.Defaults.Repeat != 1 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Sandbox.InheritEnvironment {
		t.Fatal("sandbox must default to allowlist mode")
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
}

func TestLoadParsesYAMLAndFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	body := "opencode_bin: /usr/local/bin/opencode\nsandbox:\n  pass_env:\n    - NODE_EXTRA_CA_CERTS\ndefaults:\n  timeout_seconds: 120\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(Paths{ConfigFile: file})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OpenCodeBin != "/usr/local/bin/opencode" {
		t.Fatalf("OpenCodeBin = %q", cfg.OpenCodeBin)
	}
	if cfg.Defaults.TimeoutSeconds != 120 {
		t.Fatalf("TimeoutSeconds = %d", cfg.Defaults.TimeoutSeconds)
	}
	if cfg.Defaults.Repeat != 1 {
		t.Fatalf("Repeat default not filled: %d", cfg.Defaults.Repeat)
	}
	if len(cfg.Sandbox.PassEnv) != 1 || cfg.Sandbox.PassEnv[0] != "NODE_EXTRA_CA_CERTS" {
		t.Fatalf("PassEnv = %v", cfg.Sandbox.PassEnv)
	}
	if cfg.Server.Listen != "127.0.0.1:8787" {
		t.Fatalf("Listen default not filled: %q", cfg.Server.Listen)
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte(": : :"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(Paths{ConfigFile: file}); err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
