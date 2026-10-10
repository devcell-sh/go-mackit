package qemu

import (
	"context"
	"fmt"

	"github.com/devcell-sh/go-mackit/vm"
)

type Backend struct{}

func (b *Backend) DefaultDiskFormat() vm.DiskFormat {
	return vm.DiskFormatQcow2
}

func (b *Backend) CreateDisk(path string, sizeGB int, format vm.DiskFormat) error {
	switch format {
	case vm.DiskFormatRaw:
		return createRawDisk(path, sizeGB)
	default:
		return CreateDisk(path, sizeGB)
	}
}

func (b *Backend) CreateOverlay(path, base string) error {
	return CreateOverlay(path, base)
}

func (b *Backend) Flatten(src, dst string) error {
	return FlattenQcow2(src, dst)
}

func (b *Backend) StartInstall(ctx context.Context, cfg vm.VMInstallConfig) (vm.VM, error) {
	ic := InstallConfig{
		InstallerDMG: cfg.InstallerDMG,
		OpenCoreImg:  cfg.OpenCoreImg,
		DiskPath:     cfg.DiskPath,
		DiskSizeGB:   cfg.DiskSizeGB,
		GuestArch:    cfg.GuestArch,
		CPUs:         cfg.CPUs,
		MemoryGB:     cfg.MemoryGB,
		SSHPort:      cfg.SSHPort,
		SSHGuestPort: cfg.SSHGuestPort,
		VNCPort:      cfg.VNCPort,
		Accel:        cfg.Accel,
		OutputDir:    cfg.OutputDir,
	}
	if opts, ok := cfg.BackendExtra.(*InstallOptions); ok && opts != nil {
		ic.DisplayType = opts.DisplayType
		ic.StructuredLogPath = opts.StructuredLogPath
	}

	machine, err := StartInstall(ctx, ic)
	if err != nil {
		return nil, err
	}
	return &vmHandle{vm: machine, ssh: fmt.Sprintf("127.0.0.1:%d", cfg.SSHPort)}, nil
}

func (b *Backend) StartRun(ctx context.Context, cfg vm.VMRunConfig) (vm.VM, error) {
	rc := RunConfig{
		DiskPath:     cfg.DiskPath,
		GuestArch:    cfg.GuestArch,
		CPUs:         cfg.CPUs,
		MemoryGB:     cfg.MemoryGB,
		SSHPort:      cfg.SSHPort,
		SSHGuestPort: cfg.SSHGuestPort,
		SSHHost:      cfg.SSHHost,
		VNCPort:      cfg.VNCPort,
		Forwards:     cfg.Forwards,
		Accel:        cfg.Accel,
		OutputDir:    cfg.OutputDir,
		VMName:       cfg.VMName,
		SMBIOSSerial: cfg.SMBIOSSerial,
	}
	if opts, ok := cfg.BackendExtra.(*RunOptions); ok && opts != nil {
		rc.OpenCoreImg = opts.OpenCoreImg
		rc.VarsPath = opts.VarsPath
		rc.DisplayType = opts.DisplayType
		rc.Detach = opts.Detach
		rc.StructuredLogPath = opts.StructuredLogPath
	}

	machine, err := StartRun(ctx, rc)
	if err != nil {
		return nil, err
	}

	sshHost := cfg.SSHHost
	if sshHost == "" {
		sshHost = "127.0.0.1"
	}
	return &vmHandle{vm: machine, ssh: fmt.Sprintf("%s:%d", sshHost, cfg.SSHPort)}, nil
}

type vmHandle struct {
	vm  *InstallVM
	ssh string
}

func (h *vmHandle) Stop() error           { return h.vm.Stop() }
func (h *vmHandle) Wait() error           { return h.vm.Wait() }
func (h *vmHandle) SSHAddr() string       { return h.ssh }
func (h *vmHandle) OutputDir() string     { return h.vm.OutputDir() }
func (h *vmHandle) PID() int              { return h.vm.PID() }
func (h *vmHandle) Done() <-chan struct{} { return h.vm.Done() }
func (h *vmHandle) QMPSocket() string     { return h.vm.qmpSock }
