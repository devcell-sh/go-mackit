package mackit

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcell-sh/go-mackit/build"
	"github.com/devcell-sh/go-mackit/cache"
	"github.com/devcell-sh/go-mackit/internal/logx"
	"github.com/devcell-sh/go-mackit/media"
	"github.com/devcell-sh/go-mackit/vm"
	"github.com/devcell-sh/go-mackit/vm/qemu"
	"github.com/devcell-sh/go-mackit/vm/vmstate"
)

type StartOpts struct {
	Image      string
	Name       string
	Accel      string
	StateDir   string
	CPUs       uint
	MemoryGB   uint64
	SSHPort    uint16
	VNCPort    uint16
	Foreground bool
	Forwards   []vm.PortForward
	Logger     *slog.Logger
	// CacheDir holds the OpenCore download (default cache.Dir()).
	CacheDir string
	// OpenCoreImg is an explicit OpenCore boot image; when empty the
	// cached release is used, patched like builds do (hidden picker,
	// verbose boot, serial logging). OpenCoreVersion selects the release
	// ("" for the default).
	OpenCoreImg     string
	OpenCoreVersion string
	// NIC is the QEMU network device model. Empty picks it from the macOS
	// version named in the image file name ("macos-catalina.qcow2" gets
	// vmxnet3, see build.GuestNIC); an image whose name says nothing gets
	// the default, virtio-net-pci, which macOS 11 and later drive.
	NIC string
}

func Start(ctx context.Context, opts StartOpts) (vm.VM, error) {
	if opts.Image == "" {
		return nil, fmt.Errorf("StartOpts.Image is required")
	}
	absImage, err := filepath.Abs(opts.Image)
	if err != nil {
		return nil, fmt.Errorf("resolving image path: %w", err)
	}
	if _, err := os.Stat(absImage); err != nil {
		return nil, fmt.Errorf("image not found: %w", err)
	}

	name := opts.Name
	if name == "" {
		base := filepath.Base(absImage)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = vmstate.DefaultDir()
	}
	if existing, err := vmstate.FindByImage(stateDir, absImage); err == nil &&
		existing != nil && vmstate.IsAlive(existing.PID) {
		return nil, fmt.Errorf("VM %q is already running (PID %d) from image %s",
			existing.Name, existing.PID, absImage)
	}

	guestArch := "x86_64"

	accel := opts.Accel
	if accel == "" {
		accel = qemu.DefaultAccelForArch(guestArch)
	}
	cpus := opts.CPUs
	if cpus == 0 {
		cpus = 4
	}
	memoryGB := opts.MemoryGB
	if memoryGB == 0 {
		memoryGB = 8
	}
	sshPort := opts.SSHPort
	if sshPort == 0 {
		sshPort = qemu.FreePort()
		if sshPort == 0 {
			sshPort = 20022
		}
	}
	vncPort := opts.VNCPort
	if vncPort == 0 {
		vncPort = qemu.FreePort()
		if vncPort == 0 {
			vncPort = 25900
		}
	}

	backend, backendName, err := build.ResolveBackend()
	if err != nil {
		return nil, err
	}

	outDir := filepath.Join(filepath.Dir(absImage), ".mackit", "run", name)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	logger = logx.Logger(logger, logx.ServiceMackit)

	fwPath := qemu.FirmwareForArch(guestArch)
	if fwPath == "" {
		return nil, fmt.Errorf("no UEFI firmware found for %s", guestArch)
	}
	varsPath := filepath.Join(outDir, "vars.fd")
	if err := qemu.PrepareVarsFile(fwPath, varsPath); err != nil {
		return nil, fmt.Errorf("preparing vars: %w", err)
	}

	displayType := fmt.Sprintf("vnc=:%d", vncPort-5900)

	runCfg := vm.VMRunConfig{
		DiskPath:     absImage,
		OutputDir:    outDir,
		VMName:       "mackit-" + name,
		GuestArch:    guestArch,
		CPUs:         cpus,
		MemoryGB:     memoryGB,
		Accel:        accel,
		SSHPort:      sshPort,
		SSHGuestPort: 22,
		VNCPort:      vncPort,
		Forwards:     opts.Forwards,
	}
	if backendName == "qemu" {
		// macOS on QEMU boots only through OpenCore, so every start gets
		// the same patched image as a build.
		ocImg := opts.OpenCoreImg
		if ocImg == "" {
			cacheDir := opts.CacheDir
			if cacheDir == "" {
				cacheDir = cache.Dir()
			}
			ocImg, err = build.PrepareOpenCore(ctx, cacheDir, outDir, opts.OpenCoreVersion, logger)
			if err != nil {
				return nil, fmt.Errorf("preparing OpenCore: %w", err)
			}
		}
		nic := opts.NIC
		if nic == "" {
			nic = build.GuestNIC(versionInName(name))
		}
		runCfg.BackendExtra = &qemu.RunOptions{
			OpenCoreImg: ocImg,
			VarsPath:    varsPath,
			DisplayType: displayType,
			NIC:         nic,
			Detach:      !opts.Foreground,
		}
	}

	logger.Info("starting VM",
		"image", absImage,
		"accel", accel, "backend", backendName,
		"cpus", cpus, "memory_gb", memoryGB)

	machine, err := backend.StartRun(ctx, runCfg)
	if err != nil {
		return nil, fmt.Errorf("starting VM: %w", err)
	}

	// Forward the guest console (firmware, OpenCore, kernel -v) into the
	// logger for as long as the VM runs; `mackit logs -s serial` reads the
	// same file after a detached start.
	go func() {
		tailCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-machine.Done()
			cancel()
		}()
		qemu.TailSerial(tailCtx, filepath.Join(outDir, "serial.log"), logger)
	}()

	st := &vmstate.State{
		Name:      name,
		ImagePath: absImage,
		PID:       machine.PID(),
		Backend:   backendName,
		StartedAt: time.Now(),
		SSHPort:   sshPort,
		VNCPort:   vncPort,
		Accel:     accel,
		OutputDir: outDir,
	}
	for _, f := range opts.Forwards {
		st.Forwards = append(st.Forwards, fmt.Sprintf("%d:%d", f.Host, f.Guest))
	}
	if err := vmstate.Save(stateDir, st); err != nil {
		machine.Stop()
		return nil, fmt.Errorf("saving state: %w", err)
	}
	logger.Info("VM started", "pid", machine.PID(), "name", name)
	return machine, nil
}

func Status(stateDir string) ([]*vmstate.State, error) {
	if stateDir == "" {
		stateDir = vmstate.DefaultDir()
	}
	return vmstate.List(stateDir)
}

// versionInName returns the macOS version codename an image or VM name
// contains ("macos-catalina" gives "catalina"), or "".
func versionInName(name string) string {
	lower := strings.ToLower(name)
	for _, v := range media.VersionNames() {
		if strings.Contains(lower, v) {
			return v
		}
	}
	return ""
}
