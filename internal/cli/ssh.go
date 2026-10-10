package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/devcell-sh/go-mackit/vm/vmstate"
)

func newSSHCmd() *cobra.Command {
	var stateDir string

	cmd := &cobra.Command{
		Use:                "ssh <name> [-- extra-args...]",
		Short:              "SSH into a running macOS VM",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stateDir == "" {
				stateDir = vmstate.DefaultDir()
			}
			name := args[0]
			extraArgs := args[1:]

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
			if !vmstate.IsAlive(target.PID) {
				return fmt.Errorf("VM %q is not running (PID %d)", name, target.PID)
			}

			sshArgs := []string{
				"-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
				"-p", strconv.Itoa(int(target.SSHPort)),
				"localhost",
			}
			sshArgs = append(sshArgs, extraArgs...)

			sshBin, err := exec.LookPath("ssh")
			if err != nil {
				return fmt.Errorf("ssh not found in PATH")
			}

			proc := exec.Command(sshBin, sshArgs...)
			proc.Stdin = os.Stdin
			proc.Stdout = os.Stdout
			proc.Stderr = os.Stderr
			return proc.Run()
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default: ~/.mackit/run)")
	return cmd
}
