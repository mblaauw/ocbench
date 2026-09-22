package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Sandbox struct {
	InheritEnvironment bool     `yaml:"inherit_environment"`
	PassEnv            []string `yaml:"pass_env"`
}

type Defaults struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	Repeat         int `yaml:"repeat"`
}

type Server struct {
	Listen string `yaml:"listen"`
}

type Config struct {
	OpenCodeBin string   `yaml:"opencode_bin"`
	Sandbox     Sandbox  `yaml:"sandbox"`
	Defaults    Defaults `yaml:"defaults"`
	Server      Server   `yaml:"server"`
}

func DefaultsConfig() Config {
	return Config{
		OpenCodeBin: "opencode",
		Sandbox:     Sandbox{},
		Defaults:    Defaults{TimeoutSeconds: 900, Repeat: 1},
		Server:      Server{Listen: "127.0.0.1:8787"},
	}
}

func Load(paths Paths) (Config, error) {
	cfg := DefaultsConfig()
	raw, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", paths.ConfigFile, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", paths.ConfigFile, err)
	}
	if cfg.OpenCodeBin == "" {
		cfg.OpenCodeBin = "opencode"
	}
	if cfg.Defaults.TimeoutSeconds <= 0 {
		cfg.Defaults.TimeoutSeconds = 900
	}
	if cfg.Defaults.Repeat <= 0 {
		cfg.Defaults.Repeat = 1
	}
	if cfg.Server.Listen == "" {
		cfg.Server.Listen = "127.0.0.1:8787"
	}
	return cfg, nil
}
