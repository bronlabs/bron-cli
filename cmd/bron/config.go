package main

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bronlabs/bron-cli/internal/config"
	"github.com/bronlabs/bron-cli/internal/output"
)

func newConfigCmd() *cobra.Command {
	show := newConfigShowCmd()
	c := &cobra.Command{
		Use:   "config",
		Short: "Manage CLI profiles (~/.config/bron/config.yaml)",
		Long:  "Without a subcommand: prints the active profile (same as `bron config show`).",
		RunE:  show.RunE,
	}
	c.Flags().AddFlagSet(show.Flags())
	c.AddCommand(
		newConfigInitCmd(),
		newConfigUseProfileCmd(),
		newConfigSetCmd(),
		show,
		newConfigListCmd(),
		newConfigPathCmd(),
	)
	return c
}

func newConfigInitCmd() *cobra.Command {
	var (
		name      string
		workspace string
		baseURL   string
		keyFile   string
		setActive bool
	)
	c := &cobra.Command{
		Use:   "init",
		Short: "Create or update a profile interactively (or via flags)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			r := bufio.NewReader(os.Stdin)
			if name == "" {
				name = prompt(r, "Profile name", firstNonEmpty(cfg.ActiveProfile, "default"))
			}
			existing := cfg.Profiles[name]
			if workspace == "" {
				workspace = prompt(r, "Workspace ID", existing.Workspace)
			}
			if baseURL == "" {
				baseURL = prompt(r, "Base URL", firstNonEmpty(existing.BaseURL, "https://api.bron.org"))
			}
			if keyFile == "" {
				keyFile = prompt(r, "Path to private JWK file", existing.KeyFile)
			}

			cfg.Profiles[name] = config.Profile{
				Workspace: workspace,
				BaseURL:   baseURL,
				KeyFile:   keyFile,
			}
			if setActive || cfg.ActiveProfile == "" {
				cfg.ActiveProfile = name
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Saved profile %q to %s (active=%s).\n", name, cfg.FilePath(), cfg.ActiveProfile)
			return nil
		},
	}
	c.Flags().StringVar(&name, "name", "", "profile name (default: prompt)")
	c.Flags().StringVar(&workspace, "workspace", "", "workspace id")
	c.Flags().StringVar(&baseURL, "base-url", "", "API base URL")
	c.Flags().StringVar(&keyFile, "key-file", "", "path to private JWK file")
	c.Flags().BoolVar(&setActive, "set-active", false, "set this profile as active")
	return c
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
		Short: "Set fields on a profile (workspace, base_url, key_file)",
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
				case "workspace", "workspace_id":
					p.Workspace = v
				case "base_url", "baseURL", "base-url":
					p.BaseURL = v
				case "key_file", "keyFile", "key-file":
					p.KeyFile = v
				default:
					return fmt.Errorf("unknown key %q (allowed: workspace, base_url, key_file)", k)
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
		Short: "Show the active profile (with env overrides applied)",
		Long:  "Prints the profile as it will be used by the HTTP client (env overrides applied). Use --raw to see the unmodified YAML entry.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if raw {
				name := profile
				if name == "" {
					name = firstNonEmpty(cfg.ActiveProfile, "default")
				}
				p, ok := cfg.Profiles[name]
				if !ok {
					return fmt.Errorf("profile %q not found", name)
				}
				return output.Print(map[string]interface{}{
					"name":      name,
					"workspace": p.Workspace,
					"base_url":  p.BaseURL,
					"key_file":  p.KeyFile,
				})
			}
			p, err := cfg.Resolve(profile)
			if err != nil {
				return err
			}
			return output.Print(map[string]interface{}{
				"workspace": p.Workspace,
				"base_url":  p.BaseURL,
				"key_file":  p.KeyFile,
			})
		},
	}
	c.Flags().BoolVar(&raw, "raw", false, "print unmodified YAML entry without env overrides")
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
					"base_url":  p.BaseURL,
					"key_file":  p.KeyFile,
				})
			}
			return output.Print(rows)
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the resolved config file path",
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := config.Path()
			if err != nil {
				return err
			}
			fmt.Println(p)
			return nil
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
