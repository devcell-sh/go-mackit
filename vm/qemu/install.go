package qemu

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devcell-sh/go-mackit/vm"
)

type InstallConfig struct {
	InstallerDMG string
	OpenCoreImg  string

	DiskPath   string
	DiskSizeGB int

	FirmwarePath string
	VarsPath     string
	GuestArch    string

	CPUs     uint
	MemoryGB uint64

	SSHPort      uint16
	SSHGuestPort uint16
	VNCPort      uint16

	Accel       string
	DisplayType string
	NIC         string

	OutputDir         string
	StructuredLogPath string
}

type RunConfig struct {
	DiskPath    string
	OpenCoreImg string

	FirmwarePath string
	VarsPath     string
	GuestArch    string

	CPUs     uint
	MemoryGB uint64

	SSHPort      uint16
	SSHGuestPort uint16
	VNCPort      uint16
	Forwards     []vm.PortForward
	SSHHost      string

	Accel       string
	DisplayType string
	NIC         string
	Detach      bool

	OutputDir         string
	VMName            string
	SMBIOSSerial      string
	StructuredLogPath string
}

type InstallOptions struct {
	DisplayType       string
	StructuredLogPath string
}

type RunOptions struct {
	OpenCoreImg       string
	VarsPath          string
	DisplayType       string
	NIC               string
	Detach            bool
	StructuredLogPath string
}

type InstallVM struct {
	cmd       *exec.Cmd
	qmpSock   string
	serialLog string
	outputDir string
	sshAddr   string

	mu     sync.Mutex
	doneCh chan struct{}
	pid    int
}

func (v *InstallVM) Stop() error {
	if v.qmpSock != "" {
		if err := QMPQuit(v.qmpSock); err == nil {
			select {
			case <-v.doneCh:
				return nil
			case <-time.After(10 * time.Second):
			}
		}
	}
	if v.cmd != nil && v.cmd.Process != nil {
		return v.cmd.Process.Kill()
	}
	return nil
}

func (v *InstallVM) Wait() error {
	<-v.doneCh
	return nil
}

func (v *InstallVM) SSHAddr() string       { return v.sshAddr }
func (v *InstallVM) OutputDir() string     { return v.outputDir }
func (v *InstallVM) PID() int              { return v.pid }
func (v *InstallVM) Done() <-chan struct{} { return v.doneCh }
func (v *InstallVM) QMPSocket() string     { return v.qmpSock }

func StartInstall(ctx context.Context, c InstallConfig) (*InstallVM, error) {
	if c.InstallerDMG == "" {
		return nil, fmt.Errorf("InstallConfig.InstallerDMG is required")
	}
	if c.DiskPath == "" && c.OutputDir == "" {
		return nil, fmt.Errorf("InstallConfig.DiskPath or OutputDir is required")
	}

	outDir := c.OutputDir
	if outDir == "" {
		outDir = filepath.Dir(c.DiskPath)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	guestArch := c.GuestArch
	if guestArch == "" {
		guestArch = "x86_64"
	}

	fw := c.FirmwarePath
	if fw == "" {
		fw = FirmwareForArch(guestArch)
	}
	if fw == "" {
		return nil, fmt.Errorf("no UEFI firmware found for %s", guestArch)
	}

	varsPath := c.VarsPath
	if varsPath == "" {
		varsPath = filepath.Join(outDir, "vars.fd")
		if err := PrepareVarsFile(fw, varsPath); err != nil {
			return nil, fmt.Errorf("preparing vars: %w", err)
		}
	}

	if c.DiskPath == "" {
		c.DiskPath = filepath.Join(outDir, "mackit.qcow2")
	}
	if _, err := os.Stat(c.DiskPath); os.IsNotExist(err) {
		sizeGB := c.DiskSizeGB
		if sizeGB == 0 {
			sizeGB = 64
		}
		if err := CreateDisk(c.DiskPath, sizeGB); err != nil {
			return nil, fmt.Errorf("creating disk: %w", err)
		}
	}

	accel := c.Accel
	if accel == "" {
		accel = DefaultAccelForArch(guestArch)
	}

	display := c.DisplayType
	if display == "" {
		display = "none"
	}

	spec := Spec{
		VMName:                 "mackit-install",
		GuestArch:              guestArch,
		CPUs:                   c.CPUs,
		MemoryGB:               c.MemoryGB,
		DiskPath:               c.DiskPath,
		FirmwarePath:           fw,
		VarsPath:               varsPath,
		DisplayType:            display,
		QMPSocketDir:           filepath.Join(outDir, "install"),
		Accel:                  accel,
		NIC:                    c.NIC,
		SSHPort:                c.SSHPort,
		SSHGuestPort:           c.SSHGuestPort,
		VNCPort:                c.VNCPort,
		SerialLogPath:          filepath.Join(outDir, "serial.log"),
		GuestStructuredLogPath: c.StructuredLogPath,
	}
	spec.ApplyDefaults()

	argv := BuildInstallCommand(spec, c.InstallerDMG, c.OpenCoreImg)
	return launchQEMU(ctx, argv, spec, outDir)
}

func StartRun(ctx context.Context, c RunConfig) (*InstallVM, error) {
	if c.DiskPath == "" {
		return nil, fmt.Errorf("RunConfig.DiskPath is required")
	}
	if c.OutputDir == "" {
		return nil, fmt.Errorf("RunConfig.OutputDir is required")
	}
	if err := os.MkdirAll(c.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	guestArch := c.GuestArch
	if guestArch == "" {
		guestArch = "x86_64"
	}

	fw := c.FirmwarePath
	if fw == "" {
		fw = FirmwareForArch(guestArch)
	}
	if fw == "" {
		return nil, fmt.Errorf("no UEFI firmware found for %s", guestArch)
	}

	varsPath := c.VarsPath
	if varsPath == "" {
		varsPath = filepath.Join(c.OutputDir, "vars.fd")
		if err := PrepareVarsFile(fw, varsPath); err != nil {
			return nil, fmt.Errorf("preparing vars: %w", err)
		}
	}

	accel := c.Accel
	if accel == "" {
		accel = DefaultAccelForArch(guestArch)
	}

	display := c.DisplayType
	if display == "" {
		display = "none"
	}

	name := c.VMName
	if name == "" {
		name = "mackit-run"
	}

	spec := Spec{
		VMName:                 name,
		GuestArch:              guestArch,
		CPUs:                   c.CPUs,
		MemoryGB:               c.MemoryGB,
		DiskPath:               c.DiskPath,
		OpenCoreImg:            c.OpenCoreImg,
		FirmwarePath:           fw,
		VarsPath:               varsPath,
		DisplayType:            display,
		QMPSocketDir:           c.OutputDir,
		Accel:                  accel,
		NIC:                    c.NIC,
		SSHPort:                c.SSHPort,
		SSHGuestPort:           c.SSHGuestPort,
		SSHHost:                c.SSHHost,
		VNCPort:                c.VNCPort,
		Forwards:               c.Forwards,
		SMBIOSSerial:           c.SMBIOSSerial,
		SerialLogPath:          filepath.Join(c.OutputDir, "serial.log"),
		GuestStructuredLogPath: c.StructuredLogPath,
	}
	spec.ApplyDefaults()

	// Pre-launch diagnostic log.
	diagPath := filepath.Join(c.OutputDir, "qemu.log")
	if diagFile, err := os.Create(diagPath); err == nil {
		fmt.Fprintf(diagFile, "binary: %s\naccel: %s\ndisk: %s\n", QEMUBinaryPath(), accel, c.DiskPath)
		if fi, err := os.Stat(c.DiskPath); err == nil {
			fmt.Fprintf(diagFile, "disk_size: %d\n", fi.Size())
		}
		diagFile.Close()
	}

	argv := BuildRunCommand(spec)
	return launchQEMU(ctx, argv, spec, c.OutputDir)
}

func launchQEMU(ctx context.Context, argv []string, spec Spec, outDir string) (*InstallVM, error) {
	if err := os.MkdirAll(filepath.Dir(QMPSocketPath(spec)), 0o755); err != nil {
		return nil, fmt.Errorf("creating QMP socket dir: %w", err)
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = outDir

	qemuLog := filepath.Join(outDir, "qemu.log")
	logFile, err := os.Create(qemuLog)
	if err != nil {
		return nil, fmt.Errorf("creating QEMU log: %w", err)
	}

	// Pre-launch diagnostic header: exact command, accel, and file status.
	// Written BEFORE launch so even an instant death leaves evidence.
	fmt.Fprintf(logFile, "=== mackit QEMU launch (%s/%s) ===\n", runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(logFile, "binary:   %s\n", argv[0])
	fmt.Fprintf(logFile, "accel:    %s\n", spec.effectiveAccel())
	fmt.Fprintf(logFile, "guest:    %s\n", spec.guestArch())
	for _, f := range []struct{ label, path string }{
		{"firmware", spec.FirmwarePath},
		{"vars", spec.VarsPath},
		{"disk", spec.DiskPath},
		{"opencore", spec.OpenCoreImg},
	} {
		if f.path == "" {
			continue
		}
		state := "OK"
		if fi, statErr := os.Stat(f.path); statErr != nil {
			state = "MISSING: " + statErr.Error()
		} else {
			state = fmt.Sprintf("%d bytes (%.1f MB)", fi.Size(), float64(fi.Size())/(1024*1024))
		}
		fmt.Fprintf(logFile, "%-9s %s  [%s]\n", f.label+":", f.path, state)
	}
	fmt.Fprintf(logFile, "argv:     %s\n=== QEMU output follows ===\n", strings.Join(argv, " "))

	// Fail early if a required input file is missing.
	for _, req := range []struct{ label, path string }{
		{"firmware", spec.FirmwarePath},
		{"vars", spec.VarsPath},
		{"disk", spec.DiskPath},
	} {
		if req.path == "" {
			continue
		}
		if _, statErr := os.Stat(req.path); statErr != nil {
			logFile.Close()
			return nil, fmt.Errorf("QEMU %s not found: %s (%v); see %s", req.label, req.path, statErr, qemuLog)
		}
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		tail, _ := os.ReadFile(qemuLog)
		return nil, fmt.Errorf("starting QEMU: %w; see %s:\n%s", err, qemuLog, lastLines(string(tail), 20))
	}

	sshHost := spec.SSHHost
	if sshHost == "" {
		sshHost = "127.0.0.1"
	}

	v := &InstallVM{
		cmd:       cmd,
		qmpSock:   QMPSocketPath(spec),
		serialLog: spec.SerialLogPath,
		outputDir: outDir,
		sshAddr:   fmt.Sprintf("%s:%d", sshHost, spec.SSHPort),
		doneCh:    make(chan struct{}),
		pid:       cmd.Process.Pid,
	}

	go func() {
		cmd.Wait()
		logFile.Close()
		close(v.doneCh)
	}()

	// Wait for QMP socket. A live VM answers a QMP dial; a QEMU that
	// aborted at launch leaves no socket or a stale one. Requiring an
	// actual connection stops a died-at-launch from masquerading as slow boot.
	qmpSock := QMPSocketPath(spec)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", qmpSock, time.Second)
		if err == nil {
			conn.Close()
			break
		}
		select {
		case <-v.doneCh:
			logFile.Close()
			tail, _ := os.ReadFile(qemuLog)
			return nil, fmt.Errorf("QEMU died at launch (no QMP connection within 30s); see %s:\n%s",
				qemuLog, lastLines(string(tail), 30))
		case <-time.After(500 * time.Millisecond):
		}
	}

	return v, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func QMPSocketFromVM(v vm.VM) string {
	if h, ok := v.(interface{ QMPSocket() string }); ok {
		return h.QMPSocket()
	}
	return ""
}
