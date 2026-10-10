package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-mackit/internal/config"
)

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init [filename]",
		Short: "Create a mackit.yaml in the current directory",
		Long:  "Creates a mackit.yaml (or mackit.yml) with all options documented.\n\nPass a filename to choose the extension: mackit init mackit.yml",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "mackit.yaml"
			if len(args) == 1 {
				path = args[0]
			}
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := os.WriteFile(path, []byte(config.Example), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Created %s — edit it, then run: mackit build\n", path)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing mackit.yaml")
	return cmd
}
