package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-mackit/vm/vmstate"
)

func newLogsCmd() *cobra.Command {
	var (
		stateDir string
		follow   bool
	)

	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show logs from a macOS VM",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}
			name := args[0]

			states, err := vmstate.List(stateDir)
			if err != nil {
				return fmt.Errorf("listing VMs: %w", err)
			}

			var target *vmstate.State
			for _, st := range states {
				if st.Name == name {
					target = st
					break
				}
			}
			if target == nil {
				return fmt.Errorf("VM %q not found", name)
			}

			logFile := filepath.Join(target.OutputDir, "serial.log")
			if _, statErr := os.Stat(logFile); statErr != nil {
				return fmt.Errorf("log file not found: %s", logFile)
			}

			if follow {
				proc := exec.Command("tail", "-f", logFile)
				proc.Stdout = os.Stdout
				proc.Stderr = os.Stderr
				return proc.Run()
			}

			data, err := os.ReadFile(logFile)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default: ~/.mackit/run)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "follow log output")
	return cmd
}
