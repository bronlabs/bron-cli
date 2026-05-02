package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/bronlabs/bron-cli/internal/auth"
	"github.com/bronlabs/bron-cli/internal/client"
	"github.com/bronlabs/bron-cli/internal/config"
	"github.com/bronlabs/bron-cli/internal/output"
	"github.com/bronlabs/bron-cli/internal/util"
)

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI profiles (~/.config/bron/config.yaml)",
		Long:  "Manage CLI profiles. Run a subcommand: `init`, `show`, `list`, `set`, `use-profile`.",
		Args:  cobra.NoArgs,
		Run:   func(cmd *cobra.Command, _ []string) { _ = cmd.Help() },
	}
	c.AddCommand(
		newConfigInitCmd(),
		newConfigUseProfileCmd(),
		newConfigSetCmd(),
		newConfigShowCmd(),
		newConfigListCmd(),
	)
	return c
}

func newConfigInitCmd() *cobra.Command {
	var (
		name        string
		workspace   string
		baseURL     string
		keyFile     string
		generateKey bool
	)
	c := &cobra.Command{
		Use:   "init",
		Short: "Create or update a profile, generate a JWK keypair if needed, print the public JWK",
		Long: `Creates or updates a profile and makes it active.

If --key-file points to an existing private JWK, the public half is derived
from it and printed (so you can re-register the same key in another
workspace, or re-fetch it if you lost the original).

If --key-file points to a non-existent path:
  - In interactive mode (TTY stdin), a fresh P-256 JWK keypair is generated,
    the private half is written with mode 0600, and the public half is
    printed for you to paste into Bron Settings → API keys.
  - In non-interactive mode (CI / scripts), generation requires an explicit
    --generate-key flag. Without it the command errors instead of silently
    producing an unregistered key on a typo'd path.

Switch back to a previous profile with ` + "`bron config use-profile <name>`" + `.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			r := bufio.NewReader(os.Stdin)
			isTTY := term.IsTerminal(int(os.Stdin.Fd()))
			hadPrompts := false

			if name == "" {
				// Only suggest "default" when no profile by that name exists yet.
				// Otherwise leave the default empty so a stray Enter can't silently
				// overwrite the active profile.
				var suggested string
				if _, exists := cfg.Profiles["default"]; !exists {
					suggested = "default"
				}
				name = prompt(r, "Profile name", suggested)
				if name == "" {
					return errors.New("profile name is required (pass --name or type one at the prompt)")
				}
				hadPrompts = true
			}
			existing, existed := cfg.Profiles[name]

			// Non-interactive (no TTY) requires --workspace explicitly. The
			// auto-discovery path needs to wait for the user to register the
			// public JWK in the Bron UI, which has no scripted equivalent.
			if workspace == "" && !isTTY {
				return errors.New("--workspace is required in non-interactive mode (auto-discovery via GET /workspaces only runs in TTY init)")
			}
			workspaceWasExplicit := workspace != ""

			if baseURL == "" {
				baseURL = firstNonEmpty(existing.BaseURL, "https://api.bron.org")
			}
			if keyFile == "" {
				keyFile = firstNonEmpty(existing.KeyFile, defaultKeyPathFor(name))
			}

			keyPath, err := util.Expand(keyFile)
			if err != nil {
				return err
			}
			allowGenerate := generateKey || isTTY
			generated, pubJWK, err := ensureKeyFile(keyPath, allowGenerate)
			if err != nil {
				return err
			}

			// Auto-discovery branch: print the public JWK now (so the user can
			// paste it in the Bron UI), wait for them to register it, then hit
			// GET /workspaces to validate the registration and pick up the
			// workspace ID from the response.
			if !workspaceWasExplicit {
				if generated {
					fmt.Fprintf(os.Stderr, "\nGenerated new JWK keypair → %s (mode 0600).\n", keyPath)
				}
				printPublicJWK(pubJWK)
				fmt.Fprintln(os.Stderr, "Once you've added the public JWK in Bron Settings → API keys, press Enter to continue (or 'q' + Enter to abort and set workspace later):")
				line, readErr := r.ReadString('\n')
				hadPrompts = true
				if errors.Is(readErr, io.EOF) && strings.TrimSpace(line) == "" {
					return fmt.Errorf("aborted: stdin closed before workspace resolved. Re-run with --workspace, or `bron config init --name %s --workspace <id>` once you have it", name)
				}
				if strings.EqualFold(strings.TrimSpace(line), "q") {
					return fmt.Errorf("aborted: workspace not resolved. Re-run with --workspace, or `bron config init --name %s --workspace <id>` once you have it", name)
				}

				bootstrap := &config.Profile{
					BaseURL: baseURL,
					KeyFile: keyPath,
					Proxy:   existing.Proxy,
				}
				ws, err := resolveWorkspaceForKey(cmd.Context(), bootstrap, r)
				if err != nil {
					return err
				}
				workspace = ws
			}

			// Activation policy: first profile (or sole existing one) auto-activates;
			// re-running on the already-active profile keeps it active. Otherwise,
			// only ask in fully-interactive sessions (TTY stdin AND we showed prompts) —
			// flag-driven runs (CI / scripts) never get a surprise question and never
			// auto-switch the active profile.
			isOnlyOne := len(cfg.Profiles) == 0 || (len(cfg.Profiles) == 1 && existed)
			wasActive := cfg.ActiveProfile == name
			isInteractive := hadPrompts && isTTY

			makeActive := false
			switch {
			case isOnlyOne, wasActive:
				makeActive = true
			case isInteractive:
				answer := strings.ToLower(strings.TrimSpace(prompt(r, "Make this the active profile? [y/N]", "n")))
				makeActive = answer == "y" || answer == "yes"
			}

			previous := cfg.ActiveProfile
			if baseURL == config.DefaultBaseURL {
				baseURL = ""
			}
			cfg.Profiles[name] = config.Profile{
				Workspace: workspace,
				BaseURL:   baseURL,
				KeyFile:   keyFile,
				Proxy:     existing.Proxy,
			}
			if makeActive {
				cfg.ActiveProfile = name
			}
			if err := cfg.Save(); err != nil {
				return err
			}

			switch {
			case makeActive && previous != "" && previous != name:
				fmt.Fprintf(os.Stderr, "Saved profile %q to %s and made it active (was %q; switch back with `bron config use-profile %s`).\n",
					name, cfg.FilePath(), previous, previous)
			case makeActive:
				fmt.Fprintf(os.Stderr, "Saved profile %q to %s and made it active.\n", name, cfg.FilePath())
			default:
				fmt.Fprintf(os.Stderr, "Saved profile %q to %s. Active profile remains %q (switch with `bron config use-profile %s`).\n",
					name, cfg.FilePath(), previous, name)
			}

			// Print the public JWK only if we didn't already print it during
			// auto-discovery (i.e. workspace was passed explicitly). The
			// auto-discovery branch prints earlier so the user can register
			// in the UI before we resolve the workspace.
			if workspaceWasExplicit {
				if generated {
					fmt.Fprintf(os.Stderr, "\nGenerated new JWK keypair → %s (mode 0600).\n", keyPath)
				}
				printPublicJWK(pubJWK)
			}
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "profile name; if omitted, the CLI prompts for one (default: \"default\")")
	c.Flags().StringVar(&workspace, "workspace", "", "workspace id")
	c.Flags().BoolVar(&generateKey, "generate-key", false, "in non-interactive mode, opt in to generating a fresh JWK keypair when --key-file points to a non-existent path (avoids silent keygen on typo'd paths in CI)")
	c.Flags().StringVar(&baseURL, "base-url", "", "API base URL (defaults to https://api.bron.org)")
	_ = c.Flags().MarkHidden("base-url")
	c.Flags().StringVar(&keyFile, "key-file", "", "path to JWK private key; non-existent path → a new keypair is generated and written there (mode 0600)")
	return c
}

// printPublicJWK renders the public JWK in a copy-friendly way:
//   - TTY stdout → ANSI-colorised JSON (same scheme as `--output json`),
//     blank-line padding above/below, flush against the left margin so it's
//     trivial to triple-click select. The instruction line names the exact UI
//     field the user must paste into.
//   - non-TTY stdout → bare JSON to stdout (so `bron config init … | jq`
//     keeps working), with a one-line prompt to stderr.
func printPublicJWK(jwk string) {
	const promptLine = "Public JWK — paste into Bron Settings → API keys, tick ✓ \"Input public key (JWK)\":"
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, promptLine)
		fmt.Println(jwk)
		return
	}
	fmt.Println()
	fmt.Println(promptLine)
	fmt.Println()
	if output.UseColor(os.Stdout) {
		_, _ = os.Stdout.Write(output.ColorizeJSON([]byte(jwk)))
		fmt.Println()
	} else {
		fmt.Println(jwk)
	}
	fmt.Println()
}

// colorizeWorkspaceName wraps a workspace name in green ANSI codes when
// stderr is a TTY (and NO_COLOR isn't set). Falls through to the bare name
// otherwise so the output stays grep/log-file friendly.
func colorizeWorkspaceName(name string) string {
	if !output.UseColor(os.Stderr) {
		return name
	}
	const (
		green = "\x1b[32m"
		reset = "\x1b[0m"
	)
	return green + name + reset
}

// resolveWorkspaceForKey calls GET /workspaces with the freshly registered
// API key to discover the workspace it's bound to. Doubles as a registration
// check — if the user hasn't pasted the public JWK in the Bron UI yet, the
// request 401s and the user gets a precise hint instead of a useless empty
// list.
//
// Single-workspace API keys (the common case) auto-resolve. If the response
// somehow contains multiple workspaces (e.g. the key was attached to a
// shared user account), the user picks one interactively.
//
// The endpoint is intentionally raw — `GET /workspaces` exists in
// public-api but isn't yet annotated for OpenAPI export, so we hand-call it
// here instead of waiting for a generated SDK method.
func resolveWorkspaceForKey(ctx context.Context, p *config.Profile, r *bufio.Reader) (string, error) {
	cli, err := client.NewForBootstrap(p)
	if err != nil {
		return "", err
	}
	var resp struct {
		Workspaces []struct {
			WorkspaceID string `json:"workspaceId"`
			Name        string `json:"name"`
		} `json:"workspaces"`
	}
	if err := cli.Do(ctx, "GET", "/workspaces", nil, nil, nil, &resp); err != nil {
		return "", fmt.Errorf("validate API key against /workspaces: %w (is the public JWK registered in Bron Settings → API keys?)", err)
	}
	switch len(resp.Workspaces) {
	case 0:
		return "", errors.New("the API key is not bound to any workspace yet — register the public JWK in Bron Settings → API keys, then re-run")
	case 1:
		ws := resp.Workspaces[0]
		safeName := output.SanitizeForTerminal(ws.Name)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Resolved workspace from /workspaces: %s (%s).\n", colorizeWorkspaceName(safeName), output.SanitizeForTerminal(ws.WorkspaceID))
		fmt.Fprintln(os.Stderr)
		return ws.WorkspaceID, nil
	default:
		fmt.Fprintln(os.Stderr, "The API key sees multiple workspaces. Pick one:")
		for i, ws := range resp.Workspaces {
			fmt.Fprintf(os.Stderr, "  [%d] %s (%s)\n", i+1, colorizeWorkspaceName(output.SanitizeForTerminal(ws.Name)), output.SanitizeForTerminal(ws.WorkspaceID))
		}
		choice := strings.TrimSpace(prompt(r, "Number", "1"))
		n, err := strconv.Atoi(choice)
		if err != nil || n < 1 || n > len(resp.Workspaces) {
			return "", fmt.Errorf("invalid choice %q (expected 1..%d)", choice, len(resp.Workspaces))
		}
		return resp.Workspaces[n-1].WorkspaceID, nil
	}
}

// defaultKeyPathFor returns the suggested location for a freshly generated
// private JWK, scoped per profile so multiple profiles never share a key by
// accident: ~/.config/bron/keys/<profile>.jwk.
func defaultKeyPathFor(profile string) string {
	home, err := os.UserHomeDir()
	if err != nil || profile == "" {
		return ""
	}
	return filepath.Join(home, ".config", "bron", "keys", profile+".jwk")
}

// ensureKeyFile guarantees the JWK private key at `path` exists on disk and
// returns the corresponding public JWK as pretty-printed JSON. If the file
// is missing, a fresh P-256 keypair is generated when `allowGenerate` is true;
// otherwise the call errors out with a hint to pass --generate-key. If the
// file already exists, it is read and the public half is derived by
// stripping the `d` field.
//
// `allowGenerate` is set to true automatically for fully interactive sessions
// (TTY stdin) and must be opted into explicitly via --generate-key in
// non-interactive contexts. This prevents the silent-key-creation footgun
// where a typo in --key-file inside a CI script would leave the caller with
// an unregistered key and every API call returning 401.
//
// The boolean return reports whether a new keypair was generated this call —
// callers use it to message the user appropriately.
//
// Security notes:
//   - Lstat is used so a symlink at `path` is rejected on the read path
//     instead of silently following to (e.g.) /etc/passwd.
//   - The write path uses O_EXCL so a pre-placed symlink or file cannot win
//     a TOCTOU race between Lstat and OpenFile — EEXIST is surfaced to the
//     caller instead of clobbering the target.
func ensureKeyFile(path string, allowGenerate bool) (generated bool, publicJWK string, err error) {
	if path == "" {
		return false, "", fmt.Errorf("key file path is required")
	}

	info, statErr := os.Lstat(path)
	if statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return false, "", fmt.Errorf("refusing to follow symlink at %s; point --key-file at a regular file", path)
		}
		if !info.Mode().IsRegular() {
			return false, "", fmt.Errorf("%s is not a regular file (mode %s)", path, info.Mode())
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return false, "", fmt.Errorf("read %s: %w", path, readErr)
		}
		var priv auth.JWK
		if jsonErr := json.Unmarshal(raw, &priv); jsonErr != nil {
			return false, "", fmt.Errorf("parse %s as JWK (note: cannot include the file body, may contain secrets): %v", path, jsonErr.Error())
		}
		pub := auth.JWK{Kty: priv.Kty, Crv: priv.Crv, X: priv.X, Y: priv.Y, Kid: priv.Kid}
		s, marshalErr := pub.MarshalIndent()
		if marshalErr != nil {
			return false, "", marshalErr
		}
		return false, s, nil
	} else if !os.IsNotExist(statErr) {
		return false, "", statErr
	}

	if !allowGenerate {
		return false, "", fmt.Errorf("key file %s does not exist; pass --generate-key to create one (non-interactive runs require explicit opt-in to avoid silently producing an unregistered key on a typo'd path)", path)
	}

	pair, genErr := auth.GenerateKeyPair()
	if genErr != nil {
		return false, "", genErr
	}
	priv, marshalErr := pair.Private.MarshalCompact()
	if marshalErr != nil {
		return false, "", marshalErr
	}
	parent := filepath.Dir(path)
	if mkErr := os.MkdirAll(parent, 0o700); mkErr != nil {
		return false, "", fmt.Errorf("create dir for %s: %w", path, mkErr)
	}
	// MkdirAll only sets perms on directories it creates. If the parent existed
	// already (typical: ~/.config is 0755), tighten the immediate parent to 0700
	// so the keys directory itself is private even on a shared host. We do not
	// walk up the chain — modifying ~/.config / $HOME perms would surprise the
	// user — but we warn if the parent is wider than 0700.
	if pInfo, pErr := os.Stat(parent); pErr == nil {
		if pInfo.Mode().Perm()&^0o700 != 0 {
			if chErr := os.Chmod(parent, 0o700); chErr != nil {
				fmt.Fprintf(os.Stderr, "warning: parent directory %s has permissions %s; chmod 0700 failed: %v\n", parent, pInfo.Mode().Perm(), chErr)
			}
		}
	}
	f, openErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if openErr != nil {
		if errors.Is(openErr, os.ErrExist) {
			return false, "", fmt.Errorf("key file %s appeared between the existence check and the create call (concurrent process or symlink race); aborting to avoid clobbering the target", path)
		}
		return false, "", fmt.Errorf("create %s: %w", path, openErr)
	}
	if _, writeErr := f.WriteString(priv + "\n"); writeErr != nil {
		_ = f.Close()
		return false, "", fmt.Errorf("write %s: %w", path, writeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		return false, "", fmt.Errorf("close %s: %w", path, closeErr)
	}
	pub, marshalErr := pair.Public.MarshalIndent()
	if marshalErr != nil {
		return true, "", marshalErr
	}
	return true, pub, nil
}

func newConfigUseProfileCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use-profile <name>",
		Short: "Set the active profile",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if _, ok := cfg.Profiles[args[0]]; !ok {
				return fmt.Errorf("profile %q does not exist (run `bron config init --name %s` first)", args[0], args[0])
			}
			cfg.ActiveProfile = args[0]
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Active profile: %s\n", cfg.ActiveProfile)
			return nil
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	var profile string
	c := &cobra.Command{
		Use:   "set <key>=<value> [<key>=<value> ...]",
		Short: "Set fields on a profile (workspace, keyFile, proxy, baseUrl)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			name := profile
			if name == "" {
				name = firstNonEmpty(cfg.ActiveProfile, "default")
			}
			p := cfg.Profiles[name]
			for _, kv := range args {
				eq := strings.IndexByte(kv, '=')
				if eq < 0 {
					return fmt.Errorf("expected key=value, got %q", kv)
				}
				k, v := strings.TrimSpace(kv[:eq]), strings.TrimSpace(kv[eq+1:])
				switch k {
				case "workspace", "workspaceId", "workspace_id":
					p.Workspace = v
				case "baseUrl", "base_url", "baseURL", "base-url":
					if v == config.DefaultBaseURL {
						v = ""
					}
					p.BaseURL = v
				case "keyFile", "key_file", "key-file":
					p.KeyFile = v
				case "proxy", "http_proxy", "https_proxy":
					p.Proxy = v
				default:
					return fmt.Errorf("unknown key %q (allowed: workspace, keyFile, proxy, baseUrl)", k)
				}
			}
			cfg.Profiles[name] = p
			if cfg.ActiveProfile == "" {
				cfg.ActiveProfile = name
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Updated profile %q.\n", name)
			return nil
		},
	}
	c.Flags().StringVar(&profile, "profile", "", "profile to update (default: active)")
	return c
}

func newConfigShowCmd() *cobra.Command {
	var (
		raw     bool
		profile string
	)
	c := &cobra.Command{
		Use:   "show",
		Short: "Show the active profile and config file path (with env overrides applied)",
		Long: `Prints the active profile as the HTTP client will see it: workspace, baseUrl,
keyFile, and the resolved config file path. Env overrides (BRON_PROFILE,
BRON_WORKSPACE_ID, BRON_API_KEY, BRON_API_KEY_FILE, BRON_BASE_URL, BRON_PROXY)
are applied on top of the YAML — what's printed is the effective resolution.

The "keySource" field tells you where the JWK is actually being read from on
this run: "env BRON_API_KEY" (raw bytes), "env BRON_API_KEY_FILE" (path
override), or "file <path>" (the YAML's keyFile field).

Pass --raw to skip env-var resolution and print only the on-disk YAML entry
verbatim (useful for diffing / auditing the persisted config without ambient
state). The "keySource" field is omitted in --raw mode because it is derived,
not stored.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfgPath, err := config.Path()
			if err != nil {
				return err
			}
			activeName := profile
			if activeName == "" {
				activeName = firstNonEmpty(cfg.ActiveProfile, "default")
			}
			if raw {
				p, ok := cfg.Profiles[activeName]
				if !ok {
					return fmt.Errorf("profile %q not found", activeName)
				}
				out := map[string]interface{}{
					"profile":    activeName,
					"workspace":  p.Workspace,
					"keyFile":    p.KeyFile,
					"configPath": cfgPath,
				}
				if p.BaseURL != "" {
					out["baseUrl"] = p.BaseURL
				}
				if p.Proxy != "" {
					out["proxy"] = p.Proxy
				}
				return output.Print(out)
			}
			p, err := cfg.Resolve(profile)
			if err != nil {
				return err
			}
			out := map[string]interface{}{
				"profile":    activeName,
				"workspace":  p.Workspace,
				"baseUrl":    p.BaseURL,
				"keyFile":    p.KeyFile,
				"keySource":  describeKeySource(p),
				"configPath": cfgPath,
			}
			if p.Proxy != "" {
				out["proxy"] = p.Proxy
			}
			return output.Print(out)
		},
	}
	c.Flags().BoolVar(&raw, "raw", false, "print only the on-disk YAML entry verbatim (skip env overrides; omit derived 'keySource')")
	c.Flags().StringVar(&profile, "profile", "", "profile to show (default: active)")
	return c
}

func newConfigListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all profiles",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Profiles))
			for n := range cfg.Profiles {
				names = append(names, n)
			}
			sort.Strings(names)
			rows := make([]interface{}, 0, len(names))
			for _, n := range names {
				p := cfg.Profiles[n]
				rows = append(rows, map[string]interface{}{
					"name":      n,
					"active":    n == cfg.ActiveProfile,
					"workspace": p.Workspace,
					"baseUrl":   p.BaseURL,
					"keyFile":   p.KeyFile,
				})
			}
			return output.Print(rows)
		},
	}
}

func prompt(r *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// describeKeySource reports which slot in the resolved profile actually
// supplies the JWK at signing time. Useful when the user has both
// `BRON_API_KEY` set (e.g. via `op run`) and a `key_file:` in the YAML —
// without this hint the displayed `key_file` field looks active even though
// the env var wins.
func describeKeySource(p *config.Profile) string {
	switch {
	case p.APIKey != "":
		return "env BRON_API_KEY"
	case p.KeyFile != "":
		return "file " + p.KeyFile
	default:
		return "(none)"
	}
}
