package qemu

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/devcell-sh/go-mackit/vm"
)

type Spec struct {
	VMName      string
	CPUs        uint
	MemoryGB    uint64
	DiskPath    string
	OpenCoreImg string
	GuestArch   string

	FirmwarePath string
	VarsPath     string

	DisplayType  string
	QMPSocketDir string
	Accel        string
	NoReboot     bool

	MachineType string
	CPU         string

	SerialLogPath          string
	GuestStructuredLogPath string

	SSHPort      uint16
	SSHGuestPort uint16
	SSHHost      string
	VNCPort      uint16
	Forwards     []vm.PortForward

	DiskCacheMode string
	SMBIOSSerial  string
}

func (s *Spec) guestArch() string {
	if s.GuestArch != "" {
		return s.GuestArch
	}
	return runtime.GOARCH
}

func (s *Spec) ApplyDefaults() {
	if s.CPUs == 0 {
		s.CPUs = 4
	}
	if s.MemoryGB == 0 {
		s.MemoryGB = 4
	}
	if s.DisplayType == "" {
		s.DisplayType = "none"
	}
	if s.Accel == "" {
		s.Accel = defaultAccel()
	}
}

func (s *Spec) Validate() error {
	if s.DiskPath == "" {
		return fmt.Errorf("Spec.DiskPath is required")
	}
	if s.FirmwarePath == "" {
		return fmt.Errorf("Spec.FirmwarePath is required")
	}
	return nil
}

func (s Spec) effectiveAccel() string {
	if s.Accel != "" {
		return s.Accel
	}
	return defaultAccel()
}

func QMPSocketPath(spec Spec) string {
	dir := spec.QMPSocketDir
	if dir == "" {
		dir = os.TempDir()
	}
	name := "mackit-" + spec.VMName + "-qmp.sock"
	p := filepath.Join(dir, name)
	// Unix socket paths are limited to ~104 chars.
	if len(p) > 100 {
		h := sha256.Sum256([]byte(p))
		p = filepath.Join(os.TempDir(), fmt.Sprintf("mk-%x.sock", h[:8]))
	}
	return p
}

func defaultAccel() string {
	return defaultAccelForArch(runtime.GOARCH)
}

func defaultAccelForArch(guestArch string) string {
	hostArch := runtime.GOARCH
	crossArch := false
	if isX86Arch(guestArch) != isX86Arch(hostArch) {
		crossArch = true
	}
	if crossArch {
		return "tcg,thread=multi"
	}
	switch runtime.GOOS {
	case "darwin":
		return "hvf"
	case "linux":
		if _, err := os.Stat("/dev/kvm"); err == nil {
			return "kvm"
		}
		return "tcg,thread=multi"
	default:
		return "tcg,thread=multi"
	}
}

func isX86Arch(arch string) bool {
	return arch == "amd64" || arch == "x86_64"
}

func DefaultAccel() string {
	return defaultAccel()
}

func DefaultAccelForArch(guestArch string) string {
	return defaultAccelForArch(guestArch)
}

func accelBase(accel string) string {
	if i := strings.IndexByte(accel, ','); i >= 0 {
		return accel[:i]
	}
	return accel
}

func QEMUBinaryPath() string {
	return QEMUBinaryForArch(runtime.GOARCH)
}

func QEMUBinaryForArch(guestArch string) string {
	name := "qemu-system-aarch64"
	if guestArch == "amd64" || guestArch == "x86_64" {
		name = "qemu-system-x86_64"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return name
	}
	return p
}

func FirmwarePath() string {
	return FirmwareForArch(runtime.GOARCH)
}

func FirmwareForArch(guestArch string) string {
	bin := QEMUBinaryForArch(guestArch)
	fwName := "edk2-aarch64-code.fd"
	if guestArch == "amd64" || guestArch == "x86_64" {
		fwName = "edk2-x86_64-code.fd"
	}

	candidates := []string{
		filepath.Join(filepath.Dir(bin), "..", "share", "qemu", fwName),
	}

	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".mackit", "cache", "qemu", fwName),
			filepath.Join(home, ".local", "state", "nix", "profiles", "profile", "share", "qemu", fwName),
		)
	}

	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates,
			filepath.Join("/opt/homebrew/share/qemu", fwName),
			filepath.Join("/usr/local/share/qemu", fwName),
		)
	case "linux":
		if guestArch == "arm64" || guestArch == "aarch64" {
			candidates = append(candidates,
				"/usr/share/AAVMF/AAVMF_CODE.fd",
				"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",
			)
		} else {
			candidates = append(candidates,
				"/usr/share/OVMF/OVMF_CODE.fd",
				"/usr/share/qemu/OVMF.fd",
			)
		}
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func VarsTemplatePath(firmwarePath string) string {
	dir := filepath.Dir(firmwarePath)
	base := filepath.Base(firmwarePath)

	varsCandidates := []string{
		strings.Replace(base, "-code.fd", "-vars.fd", 1),
	}
	if strings.Contains(base, "x86_64") {
		varsCandidates = append(varsCandidates, "edk2-i386-vars.fd")
	}

	for _, vf := range varsCandidates {
		p := filepath.Join(dir, vf)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func FreePort() uint16 {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return uint16(port)
}

func PrepareVarsFile(firmwarePath, varsPath string) error {
	template := VarsTemplatePath(firmwarePath)
	if template != "" {
		data, err := os.ReadFile(template)
		if err != nil {
			return fmt.Errorf("reading vars template: %w", err)
		}
		return os.WriteFile(varsPath, data, 0o644)
	}
	// No vars template found: create a 128KB empty vars store.
	empty := make([]byte, 128*1024)
	return os.WriteFile(varsPath, empty, 0o644)
}
