package cli

import (
	"fmt"
	"os"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-mackit/vm/vmstate"
)

func newStopCmd() *cobra.Command {
	var stateDir string

	cmd := &cobra.Command{
		Use:   "stop <name>",
		Short: "Stop a running macOS VM",
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
				return fmt.Errorf("VM %q not found in %s", name, stateDir)
			}

			if !vmstate.IsAlive(target.PID) {
				_ = vmstate.Remove(stateDir, name)
				fmt.Fprintf(cmd.OutOrStdout(), "VM %q was not running (PID %d); state cleaned up\n", name, target.PID)
				return nil
			}

			p, err := os.FindProcess(target.PID)
			if err != nil {
				return fmt.Errorf("finding process %d: %w", target.PID, err)
			}
			if err := p.Signal(syscall.SIGTERM); err != nil {
				return fmt.Errorf("sending SIGTERM to PID %s: %w", strconv.Itoa(target.PID), err)
			}

			_ = vmstate.Remove(stateDir, name)
			fmt.Fprintf(cmd.OutOrStdout(), "VM %q stopped (PID %d)\n", name, target.PID)
			return nil
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default: ~/.mackit/run)")
	return cmd
}
