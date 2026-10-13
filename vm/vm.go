package vm

import "context"

type DiskFormat int

const (
	DiskFormatDefault DiskFormat = iota
	DiskFormatQcow2
	DiskFormatRaw
)

func (f DiskFormat) String() string {
	switch f {
	case DiskFormatQcow2:
		return "qcow2"
	case DiskFormatRaw:
		return "raw"
	default:
		return "default"
	}
}

type VMBackend interface {
	StartInstall(ctx context.Context, cfg VMInstallConfig) (VM, error)
	StartRun(ctx context.Context, cfg VMRunConfig) (VM, error)
	CreateDisk(path string, sizeGB int, format DiskFormat) error
	CreateOverlay(path, base string) error
	Flatten(src, dst string) error
	DefaultDiskFormat() DiskFormat
}

type VM interface {
	Stop() error
	Wait() error
	SSHAddr() string
	OutputDir() string
	PID() int
	Done() <-chan struct{}
}

type PortForward struct {
	Host  uint16
	Guest uint16
}

type VMInstallConfig struct {
	InstallerDMG string
	OpenCoreImg  string

	DiskPath   string
	DiskSizeGB int
	DiskFormat DiskFormat

	GuestArch string

	CPUs     uint
	MemoryGB uint64
	OutputDir string

	SSHPort      uint16
	SSHGuestPort uint16
	VNCPort      uint16

	Accel string

	// Guest provisioning (macOS 27+ VZ only).
	// When set, VZ skips Setup Assistant and creates this user with SSH enabled.
	GuestUser     string
	GuestPassword string
	EnableSSH     bool

	BackendExtra any
}

type VMRunConfig struct {
	DiskPath   string
	DiskFormat DiskFormat

	OutputDir    string
	VMName       string
	OpenCoreImg  string
	GuestArch    string

	CPUs     uint
	MemoryGB uint64

	SSHPort      uint16
	SSHGuestPort uint16
	SSHHost      string
	VNCPort      uint16
	Forwards     []PortForward

	SharedDir    string
	Accel        string
	SMBIOSSerial string

	// Guest provisioning (macOS 27+ VZ only).
	// When set on first boot after install, VZ skips Setup Assistant.
	GuestUser     string
	GuestPassword string
	EnableSSH     bool

	BackendExtra any
}
