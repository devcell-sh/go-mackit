package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	mackit "github.com/devcell-sh/go-mackit"
	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/vm"
)

func newStartCmd() *cobra.Command {
	var (
		name       string
		accel      string
		nic        string
		cacheDir   string
		cpus       uint
		memoryGB   uint64
		sshPort    uint16
		vncPort    uint16
		foreground bool
		forwards   []string
	)

	cmd := &cobra.Command{
		Use:   "start <image>",
		Short: "Start a macOS VM from a built image",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			debug, _ := cmd.Flags().GetBool("debug")
			logger := newLogger(debug)

			var fwds []vm.PortForward
			for _, spec := range forwards {
				h, g, err := parseForwardSpec(spec)
				if err != nil {
					return err
				}
				fwds = append(fwds, vm.PortForward{Host: h, Guest: g})
			}

			opts := mackit.StartOpts{
				Image:      args[0],
				Name:       name,
				Accel:      accel,
				CPUs:       cpus,
				MemoryGB:   memoryGB,
				SSHPort:    sshPort,
				VNCPort:    vncPort,
				Foreground: foreground,
				Forwards:   fwds,
				Logger:     logger,
				CacheDir:   cacheDir,
				NIC:        nic,
			}

			machine, err := mackit.Start(cmd.Context(), opts)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "VM started  pid=%d  ssh=%s\n", machine.PID(), machine.SSHAddr())

			if foreground {
				return machine.Wait()
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "VM name (default: image filename)")
	cmd.Flags().StringVar(&accel, "accel", "", "QEMU accelerator: hvf, kvm, or tcg")
	cmd.Flags().StringVar(&nic, "nic", "", "QEMU network device: vmxnet3 for Catalina, virtio-net-pci for macOS 11+ (default: from the image name)")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "cache directory for the OpenCore image (default: "+cache.DirEnv+")")
	cmd.Flags().UintVar(&cpus, "cpus", 0, "number of CPUs (default: 4)")
	cmd.Flags().Uint64Var(&memoryGB, "memory", 0, "memory in GB (default: 6)")
	cmd.Flags().Uint16Var(&sshPort, "ssh-port", 0, "host SSH port (default: 20022)")
	cmd.Flags().Uint16Var(&vncPort, "vnc-port", 0, "host VNC port (default: 25900)")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run in foreground (block until VM exits)")
	cmd.Flags().StringSliceVarP(&forwards, "forward", "p", nil, "port forward host:guest (repeatable)")
	return cmd
}

func parseForwardSpec(spec string) (uint16, uint16, error) {
	var h, g int
	if _, err := fmt.Sscanf(spec, "%d:%d", &h, &g); err != nil || h < 1 || h > 65535 || g < 1 || g > 65535 {
		return 0, 0, fmt.Errorf("invalid port forward %q: want host:guest (1-65535)", spec)
	}
	return uint16(h), uint16(g), nil
}
