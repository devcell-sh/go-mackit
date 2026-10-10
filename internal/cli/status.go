package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	mackit "github.com/devcell-sh/go-mackit"
	"github.com/devcell-sh/go-mackit/vm/vmstate"
)

func newStatusCmd() *cobra.Command {
	var stateDir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "List running macOS VMs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			states, err := mackit.Status(stateDir)
			if err != nil {
				return err
			}
			if len(states) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No running VMs")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPID\tACCEL\tSSH\tVNC\tUPTIME\tSTATUS")
			for _, st := range states {
				status := "stopped"
				uptime := "-"
				if vmstate.IsAlive(st.PID) {
					status = "running"
					uptime = vmstate.Uptime(st)
				}
				fmt.Fprintf(w, "%s\t%d\t%s\t%d\t%d\t%s\t%s\n",
					st.Name, st.PID, st.Accel, st.SSHPort, st.VNCPort, uptime, status)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&stateDir, "state-dir", "", "state directory (default: ~/.mackit/run)")
	return cmd
}
