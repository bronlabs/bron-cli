package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bronlabs/bron-cli/internal/auth"
	"github.com/bronlabs/bron-cli/internal/util"
)

func newAuthCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "auth",
		Short: "Authentication helpers",
	}
	c.AddCommand(newAuthKeygenCmd())
	return c
}

func newAuthKeygenCmd() *cobra.Command {
	var (
		out    string
		stdout bool
	)
	c := &cobra.Command{
		Use:   "keygen",
		Short: "Generate a P-256 JWK keypair (public to send to Bron, private to keep)",
		RunE: func(cmd *cobra.Command, args []string) error {
			pair, err := auth.GenerateKeyPair()
			if err != nil {
				return err
			}

			pub, err := pair.Public.MarshalIndent()
			if err != nil {
				return err
			}
			priv, err := pair.Private.MarshalCompact()
			if err != nil {
				return err
			}

			if stdout || out == "" {
				fmt.Fprint(os.Stderr, "\n-------------------------------------\n\n")
				fmt.Fprint(os.Stderr, "Public JWK (send to Bron):\n\n")
				fmt.Println(pub)
				fmt.Fprint(os.Stderr, "\n-------------------------------------\n\n")
				fmt.Fprint(os.Stderr, "Private JWK (keep safe):\n\n")
				fmt.Println(priv)
				fmt.Fprint(os.Stderr, "\n-------------------------------------\n\n")
				return nil
			}

			path, err := util.Expand(out)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return fmt.Errorf("create dir for %s: %w", path, err)
			}
			if err := os.WriteFile(path, []byte(priv+"\n"), 0o600); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			fmt.Fprintf(os.Stderr, "Wrote private JWK to %s (mode 0600).\n\n", path)
			fmt.Fprintln(os.Stderr, "Public JWK (send to Bron):")
			fmt.Println(pub)
			fmt.Fprintf(os.Stderr, "\nKid: %s\n", pair.Kid)
			return nil
		},
	}
	c.Flags().StringVarP(&out, "out", "o", "", "write private JWK to this file (mode 0600); leave empty to print both keys to stdout")
	c.Flags().BoolVar(&stdout, "stdout", false, "print both keys to stdout (default if --out is empty)")
	return c
}
