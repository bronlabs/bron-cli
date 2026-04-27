package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newCompletionInstallCmd() *cobra.Command {
	var shell string
	c := &cobra.Command{
		Use:   "install",
		Short: "Install shell completion to a standard location (auto-detects shell)",
		Long: `Generates a completion script and writes it to the standard location for
the detected shell. Falls back to --shell when $SHELL is unrecognized.

Layout:
  zsh   ~/.zsh/completions/_bron      (must be in $fpath)
  bash  ~/.local/share/bash-completion/completions/bron
  fish  ~/.config/fish/completions/bron.fish`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if shell == "" {
				shell = detectShell()
			}
			if shell == "" {
				return fmt.Errorf("could not detect shell from $SHELL; pass --shell zsh|bash|fish")
			}
			path, inFpath, err := completionPath(shell)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("create dir for %s: %w", path, err)
			}
			f, err := os.Create(path)
			if err != nil {
				return fmt.Errorf("create %s: %w", path, err)
			}
			defer f.Close()

			root := cmd.Root()
			switch shell {
			case "zsh":
				if err := root.GenZshCompletion(f); err != nil {
					return err
				}
			case "bash":
				if err := root.GenBashCompletionV2(f, true); err != nil {
					return err
				}
			case "fish":
				if err := root.GenFishCompletion(f, true); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unsupported shell %q (zsh|bash|fish)", shell)
			}

			fmt.Fprintf(os.Stderr, "Installed %s completion to %s\n", shell, path)
			if msg := postInstallHint(shell, inFpath); msg != "" {
				fmt.Fprintln(os.Stderr, msg)
			}
			return nil
		},
	}
	c.Flags().StringVar(&shell, "shell", "", "override shell (zsh|bash|fish; default: detect from $SHELL)")
	return c
}

func detectShell() string {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return "zsh"
	case "bash":
		return "bash"
	case "fish":
		return "fish"
	}
	return ""
}

// completionPath returns the path to write the completion script to and a
// boolean reporting whether the parent directory is already in the shell's
// default fpath / completion search. When false, the user must add the path
// to fpath manually (returned via postInstallHint).
func completionPath(shell string) (string, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	switch shell {
	case "zsh":
		// zsh autoloads completions from $fpath. Homebrew adds its
		// site-functions dir automatically; that's writable on macOS
		// without sudo, so prefer it when present.
		for _, dir := range zshSiteFunctionsCandidates() {
			if isWritableDir(dir) {
				return filepath.Join(dir, "_bron"), true, nil
			}
		}
		return filepath.Join(home, ".zsh", "completions", "_bron"), false, nil
	case "bash":
		// bash-completion@2 watches XDG_DATA_HOME/bash-completion/completions/.
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "bash-completion", "completions", "bron"), true, nil
		}
		return filepath.Join(home, ".local", "share", "bash-completion", "completions", "bron"), true, nil
	case "fish":
		if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
			return filepath.Join(d, "fish", "completions", "bron.fish"), true, nil
		}
		return filepath.Join(home, ".config", "fish", "completions", "bron.fish"), true, nil
	}
	return "", false, fmt.Errorf("unsupported shell %q", shell)
}

// zshSiteFunctionsCandidates returns common system-wide locations that zsh
// has in its default $fpath (Homebrew on macOS, distro defaults on Linux).
// The first writable one wins.
func zshSiteFunctionsCandidates() []string {
	var out []string
	if p := os.Getenv("HOMEBREW_PREFIX"); p != "" {
		out = append(out, filepath.Join(p, "share", "zsh", "site-functions"))
	}
	out = append(out,
		"/opt/homebrew/share/zsh/site-functions", // Apple Silicon Homebrew
		"/usr/local/share/zsh/site-functions",    // Intel Homebrew / Linuxbrew
		"/usr/share/zsh/site-functions",          // Linux distro packages
		"/usr/share/zsh/vendor-completions",      // Debian / Ubuntu
	)
	return out
}

func isWritableDir(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	f, err := os.CreateTemp(dir, ".bron-completion-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

func postInstallHint(shell string, inFpath bool) string {
	switch shell {
	case "zsh":
		base := "Restart your shell (or run `exec zsh`)."
		if !inFpath {
			return "This path is not in zsh's default $fpath. Add to ~/.zshrc:\n" +
				"  fpath=(~/.zsh/completions $fpath)\n" +
				"  autoload -U compinit && compinit\n" +
				"Then restart the shell."
		}
		// Stale ~/.zcompdump can keep showing the old completion list.
		return base + " If completions seem stale, also run: rm -f ~/.zcompdump*"
	case "bash":
		return "Restart your shell (or run `exec bash`) to pick up completion. Requires the bash-completion package."
	case "fish":
		return "Completions will be picked up automatically by fish."
	}
	return ""
}
