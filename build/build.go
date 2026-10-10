package build

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/devcell-sh/go-mackit/build/buildopts"
	"github.com/devcell-sh/go-mackit/installer"
	"github.com/devcell-sh/go-mackit/installer/flow"
	"github.com/devcell-sh/go-mackit/internal/logx"
	"github.com/devcell-sh/go-mackit/media"
	"github.com/devcell-sh/go-mackit/vm"
	"github.com/devcell-sh/go-mackit/vm/qemu"
)

type Config struct {
	Dest         string
	CacheDir     string
	InstallerDMG string
	OpenCoreImg  string
	WorkDir      string
	Logger       *slog.Logger
	NoCache      bool
	Accel        string
	DisplayType  string
	GuestArch    string
	Opts         *buildopts.BuildOpts
	// RecoverySteps run on the macOS Recovery UI once the Utilities screen
	// is showing (Recovery mode only). They need OCR; see installer/flow.
	RecoverySteps []flow.Step
	// RecoveryTimeout bounds the wait for the macOS Utilities screen
	// (default 30m; TCG boots take 15 to 20 minutes).
	RecoveryTimeout time.Duration
}

// logger returns the build logger. Every record gets service=mackit
// unless it already names its source (serial-port lines carry the guest
// component, textshot carries its own), so build.jsonl lines are
// attributable.
func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return logx.Logger(c.Logger, logx.ServiceMackit)
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func Full(ctx context.Context, c Config) error {
	logger := c.logger()

	if c.Opts != nil {
		if err := c.Opts.Validate(); err != nil {
			return err
		}
	}

	if c.GuestArch == "" {
		c.GuestArch = "x86_64"
	}

	dmgPath, err := resolveMedia(ctx, c, logger)
	if err != nil {
		return err
	}

	ocImg, err := resolveOpenCore(ctx, c, logger)
	if err != nil {
		return err
	}

	workDir := c.WorkDir
	if workDir == "" {
		workDir = filepath.Join(filepath.Dir(c.Dest), ".mackit-build")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("creating work dir: %w", err)
	}

	patchedOC := filepath.Join(workDir, "OpenCore.qcow2")
	if err := patchOpenCore(ocImg, patchedOC, 5, logger); err != nil {
		logger.Info("could not patch OpenCore config, using original", "error", err)
	} else {
		ocImg = patchedOC
	}

	logger.Info("starting full macOS install", "dest", c.Dest)

	diskPath := filepath.Join(workDir, "mackit-install.qcow2")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		logger.Info("creating target disk", "path", diskPath, "size", "64G")
		if err := qemu.CreateDisk(diskPath, 64); err != nil {
			return fmt.Errorf("creating disk: %w", err)
		}
	}

	sshPort := buildopts.DefaultSSHPort
	vncPort := buildopts.DefaultVNCPort
	if c.Opts != nil {
		sshPort = c.Opts.Ports.SSHOrDefault()
		vncPort = c.Opts.Ports.VNCOrDefault()
	}
	if sshPort == 0 {
		sshPort = qemu.FreePort()
	}
	if vncPort == 0 {
		vncPort = qemu.FreePort()
	}

	guestArch := c.GuestArch

	accel := c.Accel
	if accel == "" {
		accel = qemu.DefaultAccelForArch(guestArch)
	}

	display := c.DisplayType
	if display == "" {
		display = fmt.Sprintf("vnc=:%d", vncPort-5900)
	}

	logger.Info("launching QEMU installer",
		"dmg", dmgPath,
		"opencore", ocImg,
		"accel", accel,
		"ssh_port", sshPort,
		"vnc_port", vncPort,
		"guest_arch", guestArch,
	)

	installCfg := qemu.InstallConfig{
		InstallerDMG: dmgPath,
		OpenCoreImg:  ocImg,
		DiskPath:     diskPath,
		GuestArch:    guestArch,
		CPUs:         4,
		MemoryGB:     8,
		SSHPort:      sshPort,
		VNCPort:      vncPort,
		Accel:        accel,
		DisplayType:  display,
		OutputDir:    workDir,
	}

	machine, err := qemu.StartInstall(ctx, installCfg)
	if err != nil {
		return fmt.Errorf("starting installer VM: %w", err)
	}
	defer machine.Stop()
	stopSerial := forwardSerial(ctx, workDir, logger)
	defer stopSerial()

	shotDir := filepath.Join(workDir, "screenshots")
	os.MkdirAll(shotDir, 0o755)
	shotStop := make(chan struct{})
	shotDone := make(chan struct{})
	go qemu.CaptureScreenshots(
		machine.QMPSocket(), shotDir, 30*time.Second,
		shotStop, shotDone,
		func(f string, a ...any) { logger.Debug(fmt.Sprintf(f, a...), "component", "screenshot") },
	)
	defer func() {
		close(shotStop)
		<-shotDone
	}()

	// OpenCore boot picker auto-select: confirm the picker when OCR sees
	// it, otherwise press Enter blindly after a fixed delay.
	if driver := newScreenDriver(ctx, c, machine.QMPSocket(), shotDir, logger); driver != nil {
		go func() {
			defer driver.Close()
			if err := driver.runner.Run(ctx, []flow.Step{pickerStep()}); err != nil {
				logger.Debug("OpenCore picker step", "error", err)
			}
		}()
	} else {
		go autoSelectPicker(machine.QMPSocket(), logger)
	}

	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)

	logger.Info("macOS installer VM running",
		"pid", machine.PID(),
		"ssh", sshAddr,
		"vnc", fmt.Sprintf("127.0.0.1:%d", vncPort),
		"screenshots", shotDir,
		"serial_log", filepath.Join(workDir, "serial.log"),
	)
	logger.Info("waiting for macOS install to complete and SSH to become available")
	logger.Info("connect via VNC to complete interactive setup if needed",
		"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", vncPort),
	)

	// macOS install is interactive: wait for SSH to become reachable.
	// The user must complete the installer via VNC, enable Remote Login,
	// and SSH will become available.
	if err := waitForSSH(ctx, sshAddr, 2*time.Hour, logger); err != nil {
		return fmt.Errorf("waiting for macOS install: %w", err)
	}

	if c.Opts != nil && len(c.Opts.Hooks) > 0 {
		logger.Info("running firstboot hooks")
		if err := runHooks(ctx, c.Opts.Hooks, buildopts.FirstBoot, sshAddr, logger); err != nil {
			return fmt.Errorf("firstboot hooks: %w", err)
		}

		logger.Info("running boot hooks")
		if err := runHooks(ctx, c.Opts.Hooks, buildopts.Boot, sshAddr, logger); err != nil {
			return fmt.Errorf("boot hooks: %w", err)
		}
	}

	logger.Info("shutting down guest")
	_ = runSSHCommand(ctx, sshAddr, "shutdown -h now", 30*time.Second)
	select {
	case <-machine.Done():
	case <-time.After(2 * time.Minute):
		logger.Info("guest did not shut down gracefully, stopping VM")
		machine.Stop()
	}

	logger.Info("flattening disk image", "src", diskPath, "dest", c.Dest)
	if err := qemu.FlattenQcow2(diskPath, c.Dest); err != nil {
		return fmt.Errorf("flattening disk: %w", err)
	}

	logger.Info("build complete", "image", c.Dest)
	return nil
}

func Recovery(ctx context.Context, c Config) error {
	logger := c.logger()

	if c.Opts != nil {
		if err := c.Opts.Validate(); err != nil {
			return err
		}
	}

	if c.GuestArch == "" {
		c.GuestArch = "x86_64"
	}

	dmgPath, err := resolveMedia(ctx, c, logger)
	if err != nil {
		return err
	}

	ocImg, err := resolveOpenCore(ctx, c, logger)
	if err != nil {
		return err
	}

	workDir := c.WorkDir
	if workDir == "" {
		workDir = filepath.Join(filepath.Dir(c.Dest), ".mackit-build")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("creating work dir: %w", err)
	}

	patchedOC := filepath.Join(workDir, "OpenCore.qcow2")
	if err := patchOpenCore(ocImg, patchedOC, 5, logger); err != nil {
		logger.Info("could not patch OpenCore config, using original", "error", err)
	} else {
		ocImg = patchedOC
	}

	logger.Info("starting recovery build", "dest", c.Dest)

	diskPath := filepath.Join(workDir, "mackit-recovery.qcow2")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		logger.Info("creating recovery disk", "path", diskPath, "size", "8G")
		if err := qemu.CreateDisk(diskPath, 8); err != nil {
			return fmt.Errorf("creating disk: %w", err)
		}
	}

	sshPort := buildopts.DefaultSSHPort
	vncPort := buildopts.DefaultVNCPort
	if c.Opts != nil {
		sshPort = c.Opts.Ports.SSHOrDefault()
		vncPort = c.Opts.Ports.VNCOrDefault()
	}
	if sshPort == 0 {
		sshPort = qemu.FreePort()
	}
	if vncPort == 0 {
		vncPort = qemu.FreePort()
	}

	guestArch := c.GuestArch

	accel := c.Accel
	if accel == "" {
		accel = qemu.DefaultAccelForArch(guestArch)
	}

	display := c.DisplayType
	if display == "" {
		display = fmt.Sprintf("vnc=:%d", vncPort-5900)
	}

	logger.Info("launching QEMU with recovery media",
		"dmg", dmgPath,
		"opencore", ocImg,
		"accel", accel,
		"vnc_port", vncPort,
		"guest_arch", guestArch,
	)

	installCfg := qemu.InstallConfig{
		InstallerDMG: dmgPath,
		OpenCoreImg:  ocImg,
		DiskPath:     diskPath,
		GuestArch:    guestArch,
		CPUs:         4,
		MemoryGB:     4,
		SSHPort:      sshPort,
		VNCPort:      vncPort,
		Accel:        accel,
		DisplayType:  display,
		OutputDir:    workDir,
	}

	machine, err := qemu.StartInstall(ctx, installCfg)
	if err != nil {
		return fmt.Errorf("starting recovery VM: %w", err)
	}
	defer machine.Stop()
	stopSerial := forwardSerial(ctx, workDir, logger)
	defer stopSerial()

	shotDir := filepath.Join(workDir, "screenshots")
	os.MkdirAll(shotDir, 0o755)
	shotStop := make(chan struct{})
	shotDone := make(chan struct{})
	go qemu.CaptureScreenshots(
		machine.QMPSocket(), shotDir, 15*time.Second,
		shotStop, shotDone,
		func(f string, a ...any) { logger.Debug(fmt.Sprintf(f, a...), "component", "screenshot") },
	)
	defer func() {
		close(shotStop)
		<-shotDone
	}()

	sshAddr := fmt.Sprintf("127.0.0.1:%d", sshPort)
	hasHooks := c.Opts != nil && len(c.Opts.Hooks) > 0

	driver := newScreenDriver(ctx, c, machine.QMPSocket(), shotDir, logger)
	defer driver.Close()
	if driver == nil && len(c.RecoverySteps) > 0 {
		return fmt.Errorf("recovery steps need OCR screen detection, which is unavailable")
	}

	if driver != nil {
		// Drive the UI: confirm the picker if shown, wait for macOS
		// Utilities, run the configured steps. The VM exiting on its own
		// cancels the wait.
		driveCtx, cancelDrive := context.WithCancel(ctx)
		go func() {
			select {
			case <-machine.Done():
				cancelDrive()
			case <-driveCtx.Done():
			}
		}()
		logger.Info("recovery mode: watching the screen for macOS Recovery",
			"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", vncPort),
			"screenshots", shotDir,
			"serial_log", filepath.Join(workDir, "serial.log"),
		)
		recoveryTimeout := c.RecoveryTimeout
		if recoveryTimeout <= 0 {
			recoveryTimeout = 30 * time.Minute
		}
		err := driver.driveRecovery(driveCtx, machine.QMPSocket(), c.RecoverySteps, recoveryTimeout)
		cancelDrive()
		if err != nil {
			select {
			case <-machine.Done():
				return fmt.Errorf("VM exited before reaching macOS Recovery: %w", err)
			default:
			}
			return err
		}
	} else {
		go autoSelectPicker(machine.QMPSocket(), logger)
	}

	if hasHooks {
		logger.Info("waiting for recovery SSH")
		if err := waitForSSH(ctx, sshAddr, 30*time.Minute, logger); err != nil {
			return fmt.Errorf("waiting for recovery SSH: %w", err)
		}

		logger.Info("running boot hooks")
		if err := runHooks(ctx, c.Opts.Hooks, buildopts.Boot, sshAddr, logger); err != nil {
			return fmt.Errorf("boot hooks: %w", err)
		}
	} else if driver == nil {
		logger.Info("recovery mode: waiting for VM to boot (stop with ctrl-c or context timeout)",
			"vnc", fmt.Sprintf("vnc://127.0.0.1:%d", vncPort),
			"screenshots", shotDir,
			"serial_log", filepath.Join(workDir, "serial.log"),
		)
		select {
		case <-ctx.Done():
			logger.Info("context timeout reached")
		case <-machine.Done():
			logger.Info("VM exited on its own")
		}
	}

	logger.Info("stopping recovery VM")
	machine.Stop()
	<-machine.Done()

	logger.Info("flattening recovery image", "src", diskPath, "dest", c.Dest)
	if err := qemu.FlattenQcow2(diskPath, c.Dest); err != nil {
		return fmt.Errorf("flattening disk: %w", err)
	}

	logger.Info("recovery build complete", "image", c.Dest)
	return nil
}

// forwardSerial tails <workDir>/serial.log into the build logger so the
// guest console (UEFI, OpenCore, kernel with -v serial=1) lands in the
// same log as mackit's own records, each line tagged with its source.
// The returned func stops the tailer and waits for it to flush.
func forwardSerial(ctx context.Context, workDir string, logger *slog.Logger) func() {
	tailCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		qemu.TailSerial(tailCtx, filepath.Join(workDir, "serial.log"), logger)
		close(done)
	}()
	return func() {
		cancel()
		<-done
	}
}

func ResolveBackend() (vm.VMBackend, string, error) {
	registry := map[string]vm.VMBackend{
		"qemu": &qemu.Backend{},
	}
	return vm.ResolveBackend("", registry)
}

func resolveMedia(ctx context.Context, c Config, logger *slog.Logger) (string, error) {
	var dmgPath string

	if c.InstallerDMG != "" {
		logger.Info("using provided installer media", "path", c.InstallerDMG)
		if err := installer.PrepareInstall(c.InstallerDMG); err != nil {
			return "", err
		}
		dmgPath = c.InstallerDMG
	} else {
		from := "sequoia"
		arch := c.GuestArch
		if c.Opts != nil && c.Opts.From != "" {
			kind := buildopts.DetectFrom(c.Opts.From)
			switch kind {
			case buildopts.FromDMG:
				logger.Info("using local DMG", "path", c.Opts.From)
				if err := installer.PrepareInstall(c.Opts.From); err != nil {
					return "", err
				}
				dmgPath = c.Opts.From
			case buildopts.FromIPSW:
				logger.Info("extracting BaseSystem.dmg from IPSW", "path", c.Opts.From)
				cacheDir := c.CacheDir
				if cacheDir == "" {
					cacheDir = filepath.Join(os.TempDir(), "mackit-ipsw")
				}
				p, err := installer.ExtractFromIPSW(c.Opts.From, cacheDir)
				if err != nil {
					return "", err
				}
				dmgPath = p
			case buildopts.FromCatalog:
				parsed, parsedArch, err := media.ParseFrom(c.Opts.From)
				if err != nil {
					return "", err
				}
				from = parsed
				if arch == "" {
					arch = parsedArch
				}
			}
		}

		if dmgPath == "" {
			result, err := media.FetchInstallerDMG(ctx, media.FetchOptions{
				CacheDir: c.CacheDir,
				Version:  from,
				Arch:     arch,
				NoCache:  c.NoCache,
				Logger:   logger,
			})
			if err != nil {
				return "", err
			}
			dmgPath = result.Path
		}
	}

	// Apple DMGs are UDIF-compressed. QEMU needs raw block images with
	// readable GPT partition tables so OpenCore can scan for boot entries.
	if installer.IsUDIFDMG(dmgPath) {
		rawPath := dmgPath + ".raw"
		logger.Info("converting UDIF DMG to raw image", "src", dmgPath, "dest", rawPath)
		converted, err := installer.ConvertDMGToRaw(dmgPath, rawPath)
		if err != nil {
			return "", fmt.Errorf("converting DMG: %w", err)
		}
		return converted, nil
	}

	return dmgPath, nil
}

func patchOpenCore(srcImg, destImg string, timeout int, logger *slog.Logger) error {
	files, err := qemu.ReadAllFilesFromFATQcow2(srcImg)
	if err != nil {
		return fmt.Errorf("reading OpenCore image: %w", err)
	}

	var configPath string
	for p := range files {
		if strings.HasSuffix(strings.ToUpper(p), "/EFI/OC/CONFIG.PLIST") {
			configPath = p
			break
		}
	}
	if configPath == "" {
		return fmt.Errorf("config.plist not found in OpenCore image")
	}

	logger.Info("patching OpenCore config.plist", "path", configPath, "timeout", timeout)
	patched, err := installer.PatchOpenCorePlist(files[configPath], timeout)
	if err != nil {
		return err
	}

	// Patch in place: the image keeps its GPT and ESP geometry, which the
	// firmware's FAT driver is picky about.
	if err := qemu.ReplaceFileInFATQcow2(srcImg, destImg, configPath, patched); err != nil {
		return fmt.Errorf("writing patched OpenCore image: %w", err)
	}
	return nil
}

func resolveOpenCore(ctx context.Context, c Config, logger *slog.Logger) (string, error) {
	if c.OpenCoreImg != "" {
		if _, err := os.Stat(c.OpenCoreImg); err != nil {
			return "", fmt.Errorf("OpenCore image not found: %w", err)
		}
		return c.OpenCoreImg, nil
	}

	cacheDir := c.CacheDir
	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache", "mackit")
	}

	ocVersion := ""
	if c.Opts != nil {
		ocVersion = c.Opts.OpenCoreVersion
	}

	return installer.EnsureOpenCore(ctx, installer.OpenCoreConfig{
		Version:  ocVersion,
		CacheDir: cacheDir,
		NoCache:  c.NoCache,
		Logger:   logger,
	})
}
