package config

import (
	"os"
	"path/filepath"
)

type Paths struct {
	Home       string
	ConfigFile string
	DB         string
	Profiles   string
	Runs       string
	Suites     string
	Cache      string
	// OpenCodeDB is where OpenCode keeps its own session history. ocbench reads
	// it only to harvest candidate tasks from real work, and only when asked.
	OpenCodeDB string
}

func Resolve(env func(string) string) Paths {
	home := env("HOME")
	configHome := env("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	dataHome := env("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	cacheHome := env("XDG_CACHE_HOME")
	if cacheHome == "" {
		cacheHome = filepath.Join(home, ".cache")
	}
	data := env("OCBENCH_HOME")
	if data == "" {
		data = filepath.Join(dataHome, "ocbench")
	}
	cache := filepath.Join(cacheHome, "ocbench")
	return Paths{
		Home:       data,
		ConfigFile: filepath.Join(configHome, "ocbench", "config.yaml"),
		DB:         filepath.Join(data, "ocbench.db"),
		Profiles:   filepath.Join(data, "profiles"),
		Runs:       filepath.Join(data, "runs"),
		Suites:     filepath.Join(data, "suites"),
		Cache:      cache,
		OpenCodeDB: filepath.Join(dataHome, "opencode", "opencode.db"),
	}
}

func ResolveOS() Paths { return Resolve(os.Getenv) }

func EnsureDirs(p Paths) error {
	for _, dir := range []string{p.Home, p.Profiles, p.Runs, p.Suites, p.Cache} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}
