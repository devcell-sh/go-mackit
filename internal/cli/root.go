package cli

import (
	"github.com/spf13/cobra"
)

var version = "dev"

func NewRootCmd() *cobra.Command {
	var debug bool

	root := &cobra.Command{
		Use:     "mackit",
		Version: version,
		Short:   "Build macOS environments from code",
		Long: "mackit builds macOS environments from code: fetches install media,\n" +
			"assembles an OpenCore-based boot chain, and drives the install in\n" +
			"QEMU. Works on any host: macOS (HVF), Linux (KVM), or containers (TCG).",
		SilenceUsage:  true,
		SilenceErrors: false,
	}

	root.PersistentFlags().BoolVar(&debug, "debug", false,
		"enable debug-level logging to stderr")

	root.AddCommand(
		newInitCmd(),
		newBuildCmd(),
		newStartCmd(),
		newStopCmd(),
		newStatusCmd(),
		newSSHCmd(),
		newLogsCmd(),
		newImagesCmd(),
	)
	return root
}
