package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"gopkg.in/yaml.v3"

	"github.com/bronlabs/bron-cli/internal/util"
)

type Profile struct {
	Workspace string `yaml:"workspace,omitempty"`
	BaseURL   string `yaml:"base_url,omitempty"`
	KeyFile   string `yaml:"key_file,omitempty"`
	Proxy     string `yaml:"proxy,omitempty"`

	// APIKey is set transiently from $BRON_API_KEY in Resolve(). Never read
	// from or written to YAML — keeps the JWK out of on-disk config so secret
	// stores (1Password, Vault, sops) can pipe it via env without leaving a
	// file on disk. Takes precedence over KeyFile when both are present.
	APIKey string `yaml:"-"`
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
// Env vars BRON_WORKSPACE_ID, BRON_BASE_URL, BRON_API_KEY, BRON_API_KEY_FILE,
// BRON_PROXY override individual fields. BRON_API_KEY (raw JWK bytes) wins over
// BRON_API_KEY_FILE / key_file when both are set — it's the path used by
// secret-store wrappers like `op run -- bron …` that inject the key as env.
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
	if v := os.Getenv("BRON_API_KEY"); v != "" {
		p.APIKey = v
		// Strip from the env so child processes (e.g. `bron completion install`,
		// `op run -- bron …` shells, debugger spawns) don't inherit the JWK.
		// `op run` re-injects it for each run, so subsequent invocations still
		// work; for the lifetime of *this* process we already captured the value.
		_ = os.Unsetenv("BRON_API_KEY")
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

	if !ok && p.Workspace == "" && p.KeyFile == "" && p.APIKey == "" {
		return nil, fmt.Errorf("profile %q not found and no env overrides set", name)
	}
	return &p, nil
}

// LoadKey returns the raw JWK bytes for the profile. Source precedence:
//
//  1. APIKey (set from $BRON_API_KEY) — used as-is, no disk access.
//  2. KeyFile — read from disk after the standard 0600 permission check.
//
// Trailing whitespace is preserved; callers TrimSpace before handing the
// value to the SDK so a stray newline from `op read > file` doesn't corrupt
// the JWK parse.
func (p *Profile) LoadKey() ([]byte, error) {
	if p.APIKey != "" {
		return []byte(p.APIKey), nil
	}
	if p.KeyFile == "" {
		return nil, fmt.Errorf("api key not set: configure `key_file` in the profile, or set $BRON_API_KEY (raw JWK) / $BRON_API_KEY_FILE (path)")
	}
	keyPath, err := util.Expand(p.KeyFile)
	if err != nil {
		return nil, err
	}
	// Open the file first, then Stat the *open handle* — Stat-then-Open has a
	// TOCTOU window where the path can be swapped for a symlink between checks.
	// Lstat-then-Open is also wrong: Lstat sees the link (good for refusing
	// symlinks), but the read still follows it. Open + f.Stat() reads metadata
	// from the real file we'll actually consume.
	//
	// Lstat first to refuse symlinks outright — we never want to follow a
	// symlink for a private key (a 0600 symlink can point at a 0644 target on
	// shared hosts).
	if info, err := os.Lstat(keyPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("key file %s is a symlink; refusing to follow (move the JWK to a regular file at this path)", keyPath)
		}
	}

	f, err := os.Open(keyPath)
	if err != nil {
		return nil, fmt.Errorf("open key file %s: %w", keyPath, err)
	}
	defer func() { _ = f.Close() }()

	// Block (don't warn) on group/world-readable key files. SSH does the same;
	// for a CLI that signs withdrawals it's the only safe default. Permission
	// gives an attacker on a shared host enough to impersonate the workspace
	// and move funds. Fix is one chmod away.
	//
	// Skipped on Windows — POSIX permission bits are surfaced as a static
	// 0666/0444 there and the check produces false positives without
	// catching anything real. Windows ACLs would need a different code path
	// (golang.org/x/sys/windows + DACL parsing); out of scope for v0.x.
	if runtime.GOOS != "windows" {
		info, err := f.Stat()
		if err != nil {
			return nil, fmt.Errorf("stat key file %s: %w", keyPath, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf("key file %s has overly permissive mode %#o (group/world readable); run `chmod 600 %s` and retry",
				keyPath, info.Mode().Perm(), keyPath)
		}
	}

	keyBytes, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read key file %s: %w", keyPath, err)
	}
	return keyBytes, nil
}
