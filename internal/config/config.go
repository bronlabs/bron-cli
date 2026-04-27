package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/bronlabs/bron-cli/internal/util"
)

type Profile struct {
	Workspace string `yaml:"workspace,omitempty"`
	BaseURL   string `yaml:"base_url,omitempty"`
	KeyFile   string `yaml:"key_file,omitempty"`
	Proxy     string `yaml:"proxy,omitempty"`
}

type Config struct {
	ActiveProfile string             `yaml:"active_profile"`
	Profiles      map[string]Profile `yaml:"profiles"`
	path          string
}

const DefaultBaseURL = "https://api.bron.org"

func Path() (string, error) {
	if p := os.Getenv("BRON_CONFIG"); p != "" {
		return util.Expand(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "bron", "config.yaml"), nil
	}
	return filepath.Join(home, ".config", "bron", "config.yaml"), nil
}

func Load() (*Config, error) {
	p, err := Path()
	if err != nil {
		return nil, err
	}
	cfg := &Config{Profiles: map[string]Profile{}, path: p}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", p, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", p, err)
	}
	cfg.path = p
	return cfg, nil
}

// Save writes the config back to disk, creating parent directories as needed.
// File mode is 0600 — it may contain workspace IDs and key-file paths.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := Path()
		if err != nil {
			return err
		}
		c.path = p
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(c.path), err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(c.path, data, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", c.path, err)
	}
	return nil
}

// FilePath returns the resolved on-disk location of this config.
func (c *Config) FilePath() string { return c.path }

// Resolve returns the profile to use, applying env overrides on top of YAML.
// Precedence (highest first): explicit name → BRON_PROFILE → ActiveProfile → "default".
// Env vars BRON_WORKSPACE_ID, BRON_BASE_URL, BRON_API_KEY_FILE override individual fields.
func (c *Config) Resolve(name string) (*Profile, error) {
	if name == "" {
		name = os.Getenv("BRON_PROFILE")
	}
	if name == "" {
		name = c.ActiveProfile
	}
	if name == "" {
		name = "default"
	}

	p, ok := c.Profiles[name]
	if !ok {
		p = Profile{}
	}

	if v := os.Getenv("BRON_WORKSPACE_ID"); v != "" {
		p.Workspace = v
	}
	if v := os.Getenv("BRON_BASE_URL"); v != "" {
		p.BaseURL = v
	}
	if v := os.Getenv("BRON_API_KEY_FILE"); v != "" {
		p.KeyFile = v
	}
	if v := os.Getenv("BRON_PROXY"); v != "" {
		p.Proxy = v
	}
	if p.BaseURL == "" {
		p.BaseURL = DefaultBaseURL
	}

	if !ok && p.Workspace == "" && p.KeyFile == "" {
		return nil, fmt.Errorf("profile %q not found and no env overrides set", name)
	}
	return &p, nil
}
