package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// mcpTarget describes one supported MCP host: where its JSON config lives
// (per-OS), or — for Claude Code — the CLI command that mutates the config
// for us. Adding a new target means appending to mcpInstallTargets.
type mcpTarget struct {
	id           string
	displayName  string
	configPath   func() (string, error)
	useClaudeCLI bool
}

var mcpInstallTargets = []mcpTarget{
	{
		id:           "claude-code",
		displayName:  "Claude Code",
		useClaudeCLI: true,
	},
	{
		id:          "claude-desktop",
		displayName: "Claude Desktop",
		configPath: func() (string, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			switch runtime.GOOS {
			case "darwin":
				return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json"), nil
			case "windows":
				appdata := os.Getenv("APPDATA")
				if appdata == "" {
					return "", errors.New("APPDATA not set")
				}
				return filepath.Join(appdata, "Claude", "claude_desktop_config.json"), nil
			case "linux":
				return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json"), nil
			default:
				return "", fmt.Errorf("claude-desktop install not supported on %s", runtime.GOOS)
			}
		},
	},
	{
		id:          "cursor",
		displayName: "Cursor",
		configPath: func() (string, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			return filepath.Join(home, ".cursor", "mcp.json"), nil
		},
	},
	{
		id:          "cline",
		displayName: "Cline (VS Code)",
		configPath: func() (string, error) {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			rel := filepath.Join("User", "globalStorage", "saoudrizwan.claude-dev", "settings", "cline_mcp_settings.json")
			switch runtime.GOOS {
			case "darwin":
				return filepath.Join(home, "Library", "Application Support", "Code", rel), nil
			case "windows":
				appdata := os.Getenv("APPDATA")
				if appdata == "" {
					return "", errors.New("APPDATA not set")
				}
				return filepath.Join(appdata, "Code", rel), nil
			case "linux":
				return filepath.Join(home, ".config", "Code", rel), nil
			default:
				return "", fmt.Errorf("cline install not supported on %s", runtime.GOOS)
			}
		},
	},
}

// mcpEntryNameRe restricts `--name` to a JSON-key-friendly identifier so the
// value can't smuggle ANSI escapes / shell metacharacters into stderr lines
// or the copy-paste fallback hint.
var mcpEntryNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func mcpTargetIDs() []string {
	ids := make([]string, 0, len(mcpInstallTargets))
	for _, t := range mcpInstallTargets {
		ids = append(ids, t.id)
	}
	return ids
}

// findMCPTarget returns the matching mcpTarget for an id, plus a boolean
// echoing the standard `, ok` lookup pattern.
func findMCPTarget(id string) (mcpTarget, bool) {
	for _, t := range mcpInstallTargets {
		if t.id == id {
			return t, true
		}
	}
	return mcpTarget{}, false
}

// newMCPInstallCmd registers the bron binary as an MCP server in one of the
// supported agent hosts. Idempotent: re-running with the same `--name`
// overwrites the entry rather than duplicating it.
func newMCPInstallCmd() *cobra.Command {
	var (
		target   string
		name     string
		readOnly bool
		dryRun   bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Register `bron mcp` with an MCP-aware agent host",
		Long: `Register the bron binary as an MCP server in one of the supported agent
hosts. Idempotent — re-running overwrites the existing entry under the same
name. Equivalent to hand-editing the host's mcp.json (or running
"claude mcp add" for Claude Code).

Supported targets:
  claude-code      - shells out to "claude mcp add" (requires the Claude Code CLI)
  claude-desktop   - edits ~/Library/Application Support/Claude/claude_desktop_config.json
                     (or the equivalent on Windows/Linux)
  cursor           - edits ~/.cursor/mcp.json
  cline            - edits Cline's cline_mcp_settings.json under the VS Code
                     globalStorage directory

The server entry is registered with the path returned by os.Executable() — on
Homebrew that's the symlink under /opt/homebrew/bin/, so future "brew upgrade"
swaps stay live without re-running install.`,
		Example: `  bron mcp install --target claude-desktop
  bron mcp install --target cursor --read-only
  bron mcp install --target cline --name bron-staging
  bron mcp install --target claude-code --dry-run`,
		RunE: func(c *cobra.Command, args []string) error {
			if !mcpEntryNameRe.MatchString(name) {
				return fmt.Errorf("invalid --name %q (allowed: letters, digits, _, -)", name)
			}
			t, ok := findMCPTarget(target)
			if !ok {
				return fmt.Errorf("unknown --target %q (one of: %s)", target, strings.Join(mcpTargetIDs(), ", "))
			}

			bronPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate bron binary: %w", err)
			}

			argsList := []string{"mcp"}
			if readOnly {
				argsList = append(argsList, "--read-only")
			}

			if t.useClaudeCLI {
				return installViaClaudeCLI(name, bronPath, argsList, dryRun)
			}
			path, err := t.configPath()
			if err != nil {
				return err
			}
			return installViaJSONFile(path, name, bronPath, argsList, t.displayName, dryRun)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", fmt.Sprintf("agent host to register with (one of: %s)", strings.Join(mcpTargetIDs(), ", ")))
	cmd.Flags().StringVar(&name, "name", "bron", "name of the MCP server entry (so multiple bron profiles can coexist)")
	cmd.Flags().BoolVar(&readOnly, "read-only", false, "register the server with --read-only (GET endpoints + tx dry-run only)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the planned change without writing the config file")
	_ = cmd.MarkFlagRequired("target")
	return cmd
}

// installViaClaudeCLI delegates registration to `claude mcp add`. We don't
// reach into Claude Code's settings.json directly — its config model is
// versioned and the CLI is the documented entry point.
func installViaClaudeCLI(name, bronPath string, serverArgs []string, dryRun bool) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude CLI not found on PATH; install Claude Code or run: claude mcp add %q -- %q %s",
			name, bronPath, strings.Join(serverArgs, " "))
	}
	cmdArgs := append([]string{"mcp", "add", name, "--", bronPath}, serverArgs...)
	if dryRun {
		fmt.Printf("would run: claude %s\n", strings.Join(cmdArgs, " "))
		return nil
	}
	c := exec.Command("claude", cmdArgs...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("claude mcp add failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, "registered %q with Claude Code\n", name)
	return nil
}

// mcpServerEntry mirrors the {command, args} shape every JSON-config host
// uses ({"mcpServers": {"<name>": {...}}}).
type mcpServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// installViaJSONFile edits the host's mcp.json in place, preserving any
// non-bron entries. Atomic write: temp file → rename, so a kill mid-write
// can't truncate the existing config.
func installViaJSONFile(path, name, bronPath string, serverArgs []string, hostName string, dryRun bool) error {
	cfg := map[string]any{}
	existingMode := os.FileMode(0o600)
	fileExists := false
	if info, err := os.Stat(path); err == nil {
		fileExists = true
		existingMode = info.Mode().Perm()
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return fmt.Errorf("parse %s: %w (file is not valid JSON — fix or delete it before installing)", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	if raw, present := cfg["mcpServers"]; present && raw != nil {
		if _, ok := raw.(map[string]any); !ok {
			return fmt.Errorf("parse %s: %q is %T, expected object", path, "mcpServers", raw)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[name] = mcpServerEntry{Command: bronPath, Args: serverArgs}
	cfg["mcpServers"] = servers

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	out = append(out, '\n')

	if dryRun {
		fmt.Printf("would write %s:\n%s", path, out)
		return nil
	}
	mode := existingMode
	if !fileExists {
		mode = 0o600
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
	}
	if err := writeFileAtomically(path, out, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "registered %q with %s at %s\n", name, hostName, path)
	return nil
}

// writeFileAtomically writes data to a sibling temp file, fsync, rename onto
// path. Avoids leaving a half-written config if the process is killed. The
// temp file is opened with O_EXCL + the target mode so it's never readable
// to other users between create and rename.
func writeFileAtomically(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmpName := filepath.Join(dir, fmt.Sprintf(".bron-mcp-%d-%d.json", os.Getpid(), time.Now().UnixNano()))
	tmp, err := os.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
