//go:build darwin && arm64

package vz

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Code-Hex/vz/v3"

	"github.com/devcell-sh/go-mackit/media"
	"github.com/devcell-sh/go-mackit/vm"
)

type Backend struct {
	Logger *slog.Logger
}

func (b *Backend) log() *slog.Logger {
	if b.Logger != nil {
		return b.Logger
	}
	return slog.Default()
}

func (b *Backend) DefaultDiskFormat() vm.DiskFormat { return vm.DiskFormatRaw }

func (b *Backend) CreateDisk(path string, sizeGB int, format vm.DiskFormat) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := f.Truncate(int64(sizeGB) * 1024 * 1024 * 1024); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (b *Backend) CreateOverlay(path, base string) error {
	return fmt.Errorf("vz backend does not support overlays; use raw disks")
}

func (b *Backend) Flatten(src, dst string) error {
	return copyFile(src, dst)
}

func (b *Backend) StartInstall(ctx context.Context, cfg vm.VMInstallConfig) (vm.VM, error) {
	ipswPath := cfg.InstallerDMG
	if ipswPath == "" || ipswPath == "latest" {
		var err error
		cacheDir := ""
		if cfg.OutputDir != "" {
			cacheDir = filepath.Dir(cfg.OutputDir)
		}
		ipswPath, err = FetchIPSW(ctx, cacheDir, b.log())
		if err != nil {
			return nil, fmt.Errorf("auto-fetching IPSW: %w", err)
		}
	}
	return installFromIPSW(ctx, ipswPath, cfg, b.log())
}

func (b *Backend) StartRun(ctx context.Context, cfg vm.VMRunConfig) (vm.VM, error) {
	return bootDisk(ctx, cfg, b.log())
}

// FetchIPSW finds or downloads the latest supported IPSW.
// Search order: mackit cache, tart cache (~/.tart/cache/IPSWs/),
// then VZ framework download. Returns the local path.
func FetchIPSW(ctx context.Context, cacheDir string, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "vz")

	if cacheDir == "" {
		home, _ := os.UserHomeDir()
		cacheDir = filepath.Join(home, ".cache", "mackit")
	}

	// 1. Check mackit's own cache (any .ipsw file).
	ipswDir := filepath.Join(cacheDir, "ipsw")
	_ = os.MkdirAll(ipswDir, 0o755)
	if entries, err := os.ReadDir(ipswDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".ipsw") {
				p := filepath.Join(ipswDir, e.Name())
				if info, err := os.Stat(p); err == nil && info.Size() > 1024*1024*1024 {
					logger.Info("using cached IPSW", "path", p, "size_gb", info.Size()/(1024*1024*1024))
					return p, nil
				}
			}
		}
	}

	// 2. Check tart's cache (shared IPSW pool).
	if home, err := os.UserHomeDir(); err == nil {
		tartDir := filepath.Join(home, ".tart", "cache", "IPSWs")
		if entries, err := os.ReadDir(tartDir); err == nil {
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".ipsw" {
					p := filepath.Join(tartDir, e.Name())
					if info, err := os.Stat(p); err == nil && info.Size() > 1024*1024*1024 {
						logger.Info("using tart-cached IPSW", "path", p)
						return p, nil
					}
				}
			}
		}
	}

	// 3. Resolve URL via VZ framework, download ourselves (matches tart's approach).
	logger.Info("resolving latest supported IPSW URL via Virtualization.framework")
	ipswURL, err := vz.GetLatestSupportedMacOSRestoreImageURL()
	if err != nil {
		return "", fmt.Errorf("IPSW URL lookup failed: %w\n\nDownload manually:\n  1. Visit https://developer.apple.com/downloads (search \"restore\")\n  2. Run: MACKIT_IPSW=/path/to/file.ipsw <your command>", err)
	}

	filename := "restore.ipsw"
	if u, err := url.Parse(ipswURL); err == nil {
		if base := filepath.Base(u.Path); strings.HasSuffix(base, ".ipsw") {
			filename = base
		}
	}
	destPath := filepath.Join(ipswDir, filename)

	logger.Info("downloading IPSW", "url", ipswURL, "dest", destPath)
	if err := media.DownloadFile(ctx, ipswURL, destPath, logger); err != nil {
		return "", fmt.Errorf("IPSW download failed: %w", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return "", fmt.Errorf("IPSW download verification: %w", err)
	}
	logger.Info("IPSW downloaded", "path", destPath, "size_gb", info.Size()/(1024*1024*1024))
	return destPath, nil
}

func installFromIPSW(ctx context.Context, ipswPath string, cfg vm.VMInstallConfig, logger *slog.Logger) (vm.VM, error) {
	logger = logger.With("component", "vz")
	restore, err := vz.LoadMacOSRestoreImageFromPath(ipswPath)
	if err != nil {
		return nil, fmt.Errorf("loading IPSW: %w", err)
	}
	reqs := restore.MostFeaturefulSupportedConfiguration()
	if reqs == nil {
		return nil, fmt.Errorf("IPSW does not support this hardware")
	}

	outDir := cfg.OutputDir
	if outDir == "" {
		return nil, fmt.Errorf("VMInstallConfig.OutputDir is required for vz backend")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}

	nvramPath := outDir + "/nvram.bin"
	aux, err := vz.NewMacAuxiliaryStorage(nvramPath, vz.WithCreatingMacAuxiliaryStorage(reqs.HardwareModel()))
	if err != nil {
		return nil, fmt.Errorf("creating NVRAM: %w", err)
	}

	diskPath := cfg.DiskPath
	if diskPath == "" {
		diskPath = outDir + "/mackit-install.raw"
	}
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		sizeGB := cfg.DiskSizeGB
		if sizeGB == 0 {
			sizeGB = 64
		}
		f, err := os.Create(diskPath)
		if err != nil {
			return nil, err
		}
		if err := f.Truncate(int64(sizeGB) * 1024 * 1024 * 1024); err != nil {
			f.Close()
			return nil, err
		}
		f.Close()
	}

	ecid, err := vz.NewMacMachineIdentifier()
	if err != nil {
		return nil, fmt.Errorf("creating machine identifier: %w", err)
	}

	// Persist platform identity so the disk can boot later.
	if err := savePlatformData(outDir, ecid, reqs.HardwareModel()); err != nil {
		return nil, err
	}

	cpus := cfg.CPUs
	if cpus == 0 {
		cpus = max(4, uint(reqs.MinimumSupportedCPUCount()))
	}
	mem := cfg.MemoryGB
	if mem == 0 {
		mem = max(8, reqs.MinimumSupportedMemorySize()/(1024*1024*1024))
	}

	serialLogPath := filepath.Join(outDir, "serial.log")
	logger.Info("installing from IPSW", "ipsw", ipswPath, "disk", diskPath, "cpus", cpus, "memory_gb", mem)

	vzCfg, err := buildConfig(configParams{
		cpus:          cpus,
		memoryGB:      mem,
		diskPath:      diskPath,
		hardwareModel: reqs.HardwareModel(),
		ecid:          ecid,
		aux:           aux,
		sshPort:       cfg.SSHPort,
		vncPort:       cfg.VNCPort,
		serialLogPath: serialLogPath,
	})
	if err != nil {
		return nil, fmt.Errorf("building VM config: %w", err)
	}

	machine, err := vz.NewVirtualMachine(vzCfg)
	if err != nil {
		return nil, fmt.Errorf("creating VM: %w", err)
	}

	h := newVMHandle(machine, outDir, cfg.SSHPort)

	installer, err := vz.NewMacOSInstaller(machine, ipswPath)
	if err != nil {
		return nil, fmt.Errorf("creating installer: %w", err)
	}

	go func() {
		defer h.markDone()
		logger.Info("IPSW install started")

		done := make(chan error, 1)
		go func() { done <- installer.Install(ctx) }()

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		lastPct := -1
		for {
			select {
			case err := <-done:
				pct := int(installer.FractionCompleted() * 100)
				if err != nil {
					logger.Error("IPSW install failed", "error", err, "progress", pct, "uom", "pct")
					h.setErr(err)
					return
				}
				logger.Info("IPSW install completed", "progress", 100, "uom", "pct")
				return
			case <-ticker.C:
				pct := int(installer.FractionCompleted() * 100)
				if pct != lastPct {
					lastPct = pct
					logger.Info("installing macOS", "progress", pct, "uom", "pct")
				}
			}
		}
	}()

	return h, nil
}

func bootDisk(ctx context.Context, cfg vm.VMRunConfig, logger *slog.Logger) (vm.VM, error) {
	logger = logger.With("component", "vz")
	outDir := cfg.OutputDir
	if outDir == "" {
		return nil, fmt.Errorf("VMRunConfig.OutputDir is required for vz backend")
	}

	logger.Debug("loading platform data", "dir", outDir)
	ecid, hwModel, err := loadPlatformData(outDir)
	if err != nil {
		return nil, fmt.Errorf("loading platform data: %w", err)
	}
	logger.Debug("platform data loaded")

	nvramPath := outDir + "/nvram.bin"
	logger.Debug("loading NVRAM", "path", nvramPath)
	aux, err := vz.NewMacAuxiliaryStorage(nvramPath)
	if err != nil {
		return nil, fmt.Errorf("loading NVRAM: %w", err)
	}
	logger.Debug("NVRAM loaded")

	cpus := cfg.CPUs
	if cpus == 0 {
		cpus = 4
	}
	mem := cfg.MemoryGB
	if mem == 0 {
		mem = 8
	}

	serialLogPath := filepath.Join(outDir, "serial.log")
	logger.Info("booting disk", "disk", cfg.DiskPath, "cpus", cpus, "memory_gb", mem)

	vzCfg, err := buildConfig(configParams{
		cpus:          cpus,
		memoryGB:      mem,
		diskPath:      cfg.DiskPath,
		hardwareModel: hwModel,
		ecid:          ecid,
		aux:           aux,
		sshPort:       cfg.SSHPort,
		vncPort:       cfg.VNCPort,
		recovery:      false,
		serialLogPath: serialLogPath,
	})
	if err != nil {
		return nil, fmt.Errorf("building VM config: %w", err)
	}

	machine, err := vz.NewVirtualMachine(vzCfg)
	if err != nil {
		return nil, fmt.Errorf("creating VM: %w", err)
	}

	h := newVMHandle(machine, outDir, cfg.SSHPort)

	go func() {
		defer h.markDone()
		logger.Info("VM starting")
		if err := machine.Start(); err != nil {
			logger.Error("VM start failed", "error", err)
			h.setErr(err)
			return
		}
		logger.Info("VM started")
		<-ctx.Done()
	}()

	return h, nil
}

// StartRecovery boots the installed disk into macOS Recovery.
func StartRecovery(ctx context.Context, cfg vm.VMRunConfig, logger *slog.Logger) (vm.VM, error) {
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "vz")

	outDir := cfg.OutputDir
	if outDir == "" {
		return nil, fmt.Errorf("VMRunConfig.OutputDir is required for vz backend")
	}

	logger.Debug("loading platform data for recovery", "dir", outDir)
	ecid, hwModel, err := loadPlatformData(outDir)
	if err != nil {
		return nil, fmt.Errorf("loading platform data: %w", err)
	}
	logger.Debug("platform data loaded")

	nvramPath := outDir + "/nvram.bin"
	logger.Debug("loading NVRAM", "path", nvramPath)
	aux, err := vz.NewMacAuxiliaryStorage(nvramPath)
	if err != nil {
		return nil, fmt.Errorf("loading NVRAM: %w", err)
	}
	logger.Debug("NVRAM loaded")

	cpus := cfg.CPUs
	if cpus == 0 {
		cpus = 4
	}
	mem := cfg.MemoryGB
	if mem == 0 {
		mem = 8
	}

	serialLogPath := filepath.Join(outDir, "serial.log")
	logger.Info("booting Recovery", "disk", cfg.DiskPath, "cpus", cpus, "memory_gb", mem)

	vzCfg, err := buildConfig(configParams{
		cpus:          cpus,
		memoryGB:      mem,
		diskPath:      cfg.DiskPath,
		hardwareModel: hwModel,
		ecid:          ecid,
		aux:           aux,
		sshPort:       cfg.SSHPort,
		vncPort:       cfg.VNCPort,
		recovery:      true,
		serialLogPath: serialLogPath,
	})
	if err != nil {
		return nil, fmt.Errorf("building VM config: %w", err)
	}

	machine, err := vz.NewVirtualMachine(vzCfg)
	if err != nil {
		return nil, fmt.Errorf("creating VM: %w", err)
	}

	h := newVMHandle(machine, outDir, cfg.SSHPort)

	go func() {
		defer h.markDone()
		logger.Info("Recovery VM starting")
		if err := machine.Start(vz.WithStartUpFromMacOSRecovery(true)); err != nil {
			logger.Error("Recovery VM start failed", "error", err)
			h.setErr(err)
			return
		}
		logger.Info("Recovery VM started")
		<-ctx.Done()
	}()

	return h, nil
}
